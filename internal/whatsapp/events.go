package whatsapp

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"
	"wa-server-go/internal/firestore"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// handleEvent processes WhatsApp events and forwards to appropriate channels
func (m *Manager) handleEvent(clientID string, client *Client, evt interface{}) {
	switch v := evt.(type) {
	case *events.QR:
		if v.Codes != nil && len(v.Codes) > 0 {
			code := v.Codes[0]
			fmt.Printf("📸 [%s] QR Code received\n", clientID)
			m.qrChan <- QRImageEvent{Client: clientID, URL: code}
		}

	case *events.Connected:
		fmt.Printf("✅ [%s] Connected to WhatsApp\n", clientID)
		client.SetReady(true)
		m.statusChan <- StatusUpdate{Client: clientID, Ready: true}
		
		// For leads client, trigger app state sync to get labels
		if clientID == "leads" {
			fmt.Printf("🏷️ [%s] Triggering app state sync for labels on connect...\n", clientID)
		}

	case *events.AppStateSyncComplete:
		// App state sync completed - labels should now be available
		fmt.Printf("📱 [%s] AppStateSyncComplete for: %s\n", clientID, v.Name)
		if clientID == "leads" {
			fmt.Printf("🏷️ [%s] Current labels in store: %v\n", clientID, m.LabelStore.GetAllLabels())
			fmt.Printf("🏷️ [%s] Current associations in store: %v\n", clientID, m.LabelStore.GetAllAssociations())
		}

	case *events.Disconnected:
		fmt.Printf("⚠️ [%s] Disconnected from WhatsApp\n", clientID)
		client.SetReady(false)
		m.statusChan <- StatusUpdate{Client: clientID, Ready: false, Reason: "disconnected"}

	case *events.LoggedOut:
		fmt.Printf("🚪 [%s] Logged out from WhatsApp\n", clientID)
		client.SetReady(false)
		m.statusChan <- StatusUpdate{Client: clientID, Ready: false, Reason: "logged_out"}

	case *events.StreamReplaced:
		fmt.Printf("⚠️ [%s] Stream replaced (another session took over)\n", clientID)
		client.SetReady(false)
		m.statusChan <- StatusUpdate{Client: clientID, Ready: false, Reason: "stream_replaced"}

	case *events.PushName:
		fmt.Printf("📝 [%s] Push name set: %s\n", clientID, v.NewPushName)

	case *events.Message:
		// PRIVACY UPDATE: Ignore messages from "leads" client (Number B)
		// We only want to sync contacts, not view private chats.
		if clientID == "leads" {
			return
		}
		
		fmt.Printf("📩 [%s] New message from %s: %s (FromMe: %v)\n", clientID, v.Info.Sender.User, v.Info.ID, v.Info.IsFromMe)

		// Determine message type and download media
		msg := v.Message
		msgType := "text"
		hasMedia := false
		var mediaURL, mediaTypeStr string
		var err error

		if msg.ImageMessage != nil {
			msgType = "image"
			hasMedia = true
			mediaURL, mediaTypeStr, err = saveMedia(client, msg, v.Info.ID, v.Info.IsFromMe)
		} else if msg.DocumentMessage != nil {
			msgType = "document"
			hasMedia = true
			mediaURL, mediaTypeStr, err = saveMedia(client, msg, v.Info.ID, v.Info.IsFromMe)
		} else if msg.AudioMessage != nil {
			msgType = "audio"
			hasMedia = true
		} else if msg.VideoMessage != nil {
			msgType = "video"
			hasMedia = true
		} else if msg.StickerMessage != nil {
			msgType = "sticker"
			hasMedia = true
		}
		
		if err != nil {
			fmt.Printf("⚠️ Failed to download media: %v\n", err)
		}

		// Extract body
		body := ""
		if msg.Conversation != nil {
			body = *msg.Conversation
		} else if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
			body = *msg.ExtendedTextMessage.Text
		} else if msg.ImageMessage != nil {
			body = "[Image] " + msg.ImageMessage.GetCaption()
		} else if msg.DocumentMessage != nil {
			body = "[Document] " + msg.DocumentMessage.GetFileName()
		}

		// Resolve Contact Name
		senderName := resolveContactName(client, v.Info.Sender)
		if v.Info.PushName != "" && senderName == "" {
			senderName = v.Info.PushName
		}

		// Send to websocket
		m.msgChan <- NewMessageEvent{
			Client:    clientID,
			ID:        v.Info.ID,
			From:      v.Info.Sender.String(),
			To:        v.Info.Chat.String(),
			Body:      body,
			Timestamp: v.Info.Timestamp.Unix(),
			FromMe:    v.Info.IsFromMe,
			ChatID:    v.Info.Chat.String(),
			ChatName:  senderName,
			HasMedia:  hasMedia,
			Type:      msgType,
		}

		// Save to Firestore if Repo is configured
		if m.Repo != nil {
			go func() {
				waMsg := &firestore.WAMessage{
					MessageID: v.Info.ID,
					ChatID:    v.Info.Chat.String(),
					Body:      body,
					Timestamp: v.Info.Timestamp,
					FromMe:    v.Info.IsFromMe,
					HasMedia:  hasMedia,
					MediaType: mediaTypeStr,
					MediaURL:  mediaURL,
					Type:      msgType,
					Ack:       1,
				}

				if v.Info.IsFromMe {
					waMsg.From = v.Info.Sender.ToNonAD().String() // Use actual sender ID
					waMsg.To = v.Info.Chat.String()
				} else {
					waMsg.From = v.Info.Sender.User + "@" + v.Info.Sender.Server // Normalize
					if v.Info.Sender.Server == "lid" {
						waMsg.From = v.Info.Sender.String()
					}
					// Only use JID for remote
					if !strings.Contains(waMsg.From, "@") {
						waMsg.From = v.Info.Sender.ToNonAD().String()
					}
					waMsg.To = client.WAClient.Store.ID.ToNonAD().String()
				}

				if err := m.Repo.SaveMessage(context.Background(), waMsg); err != nil {
					fmt.Printf("❌ Failed to save message to Firestore: %v\n", err)
				} else {
					fmt.Printf("💾 Message saved to Firestore: %s\n", waMsg.MessageID)
					// Update Chat Name if provided
					if senderName != "" {
						_ = m.Repo.UpdateChatName(context.Background(), waMsg.ChatID, senderName)
					}
				}
			}()
		}

	case *events.Receipt:
		// Message delivery/read receipts

	case *events.HistorySync:
		// PRIVACY UPDATE: Ignore history sync from "leads" client
		if clientID == "leads" {
			return
		}

		fmt.Printf("📜 [%s] History sync received. Processing past messages...\n", clientID)

		if m.Repo != nil && v.Data != nil {
			go func() {
				count := 0
				for _, conv := range v.Data.Conversations {
					for _, histMsg := range conv.Messages {
						webMsg := histMsg.Message
						if webMsg == nil || webMsg.Message == nil {
							continue
						}
						msg := webMsg.Message

						// Type & Media
						msgType := "text"
						hasMedia := false
						var mediaURL, mediaTypeStr string

						// We don't download history media automatically to save bandwidth
						if msg.ImageMessage != nil {
							msgType = "image"
							hasMedia = true
						} else if msg.DocumentMessage != nil {
							msgType = "document"
							hasMedia = true
						}

						// Body
						body := ""
						if msg.Conversation != nil {
							body = *msg.Conversation
						} else if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
							body = *msg.ExtendedTextMessage.Text
						} else if msg.ImageMessage != nil {
							body = "[Image] " + msg.ImageMessage.GetCaption()
						} else if msg.DocumentMessage != nil {
							body = "[Document] " + msg.DocumentMessage.GetFileName()
						}

						ts := int64(webMsg.GetMessageTimestamp())
						waMsg := &firestore.WAMessage{
							MessageID: webMsg.Key.GetID(),
							ChatID:    conv.GetID(),
							Body:      body,
							Timestamp: time.Unix(ts, 0),
							FromMe:    webMsg.Key.GetFromMe(),
							HasMedia:  hasMedia,
							MediaType: mediaTypeStr,
							MediaURL:  mediaURL,
							Type:      msgType,
							Ack:       3, // Read/Played
						}

						if waMsg.FromMe {
							waMsg.From = m.clients[clientID].WAClient.Store.ID.ToNonAD().String()
							waMsg.To = conv.GetID()
						} else {
							waMsg.From = conv.GetID()
							waMsg.To = m.clients[clientID].WAClient.Store.ID.ToNonAD().String()
						}

						// Save without waiting
						_ = m.Repo.SaveMessage(context.Background(), waMsg)
						count++
					}
				}
				fmt.Printf("✅ [%s] History sync processed: Saved %d past messages\n", clientID, count)
			}()
		}

	case *events.AppState:
		// App state sync - trigger label sync for leads client
		fmt.Printf("📱 [%s] AppState sync received, labels may be updated\n", clientID)
		
		// Manual parsing of mutations as requested
		if v.SyncActionValue != nil {
			// SyncActionValue represents the value of the action directly
			
			// Try to get LabelEditAction
			if labelEdit := v.SyncActionValue.GetLabelEditAction(); labelEdit != nil {
				name := labelEdit.GetName()
				if name != "" {
					fmt.Printf("🏷️ [%s] Manual Parse: Label Edit found: Name=%s\n", clientID, name)
				}
			}
			
			// Try to get LabelAssociationAction
			if labelAssoc := v.SyncActionValue.GetLabelAssociationAction(); labelAssoc != nil {
				labeled := labelAssoc.GetLabeled()
				fmt.Printf("🏷️ [%s] Manual Parse: Label Association found: Labeled=%v\n", clientID, labeled)
			}
		}

	case *events.LabelEdit:
		// Track label definitions
		fmt.Printf("🏷️ [%s] LabelEdit event received! LabelID=%s, Action=%+v, FromFullSync=%v\n", 
			clientID, v.LabelID, v.Action, v.FromFullSync)
		if v.Action != nil {
			name := v.Action.GetName()
			color := v.Action.GetColor()
			fmt.Printf("🏷️ [%s] Label details: Name=%s, Color=%d\n", clientID, name, color)
			if name != "" {
				m.LabelStore.SetLabel(v.LabelID, name)
			}
		}

	case *events.LabelAssociationChat:
		// Track which contacts have which labels
		fmt.Printf("🏷️ [%s] LabelAssociationChat event received! LabelID=%s, JID=%s, Action=%+v\n", 
			clientID, v.LabelID, v.JID.String(), v.Action)
		
		// IMPORTANT: Store FULL JID (including @s.whatsapp.net or @lid)
		// This is crucial to distinguishing between Phone JIDs and Device LIDs
		jid := v.JID.String() 

		if v.Action != nil && v.Action.GetLabeled() {
			m.LabelStore.AddAssociation(v.LabelID, jid)
			fmt.Printf("🏷️ [%s] Label %s ADDED to contact %s\n", clientID, v.LabelID, jid)
		} else {
			m.LabelStore.RemoveAssociation(v.LabelID, jid)
			fmt.Printf("🏷️ [%s] Label %s REMOVED from contact %s\n", clientID, v.LabelID, jid)
		}

	default:
		// Log unknown events for leads client to debug what we're receiving
		if clientID == "leads" {
			// Only log certain types to avoid spam
			switch evt.(type) {
			case *events.OfflineSyncPreview, *events.OfflineSyncCompleted:
				fmt.Printf("📥 [%s] Sync event: %T\n", clientID, evt)
			}
		}
	}
}

// resolveContactName looks up a JID in the store
func resolveContactName(client *Client, jid types.JID) string {
	contacts, err := client.WAClient.Store.Contacts.GetContact(context.Background(), jid)
	if err != nil || (!contacts.Found) {
		return ""
	}
	if contacts.FullName != "" {
		return contacts.FullName
	}
	if contacts.PushName != "" {
		return contacts.PushName
	}
	if contacts.BusinessName != "" {
		return contacts.BusinessName
	}
	return ""
}

// saveMedia downloads media from message and saves to disk
func saveMedia(client *Client, msg *waProto.Message, id string, isFromMe bool) (string, string, error) {
	var ext string
	var mimeType string

	// 1. Determine extension and mime type
	if msg.ImageMessage != nil {
		ext = ".jpg"
		mimeType = msg.ImageMessage.GetMimetype()
	} else if msg.DocumentMessage != nil {
		// Extract extension from filename or mimetype
		fileName := msg.DocumentMessage.GetFileName()
		ext = filepath.Ext(fileName)
		if ext == "" {
			exts, _ := mime.ExtensionsByType(msg.DocumentMessage.GetMimetype())
			if len(exts) > 0 {
				ext = exts[0]
			} else {
				ext = ".bin"
			}
		}
		mimeType = msg.DocumentMessage.GetMimetype()
	} else {
		return "", "", fmt.Errorf("unsupported media type")
	}

	// 2. Prepare paths
	uploadsDir := "./uploads/media"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return "", "", err
	}

	filename := fmt.Sprintf("%s%s", id, ext)
	localPath := filepath.Join(uploadsDir, filename)

	// 3. Check if file already exists (e.g. from outgoing send)
	// Retry logic for outgoing messages to handle race condition
	attempts := 1
	if isFromMe {
		attempts = 3
	}

	for i := 0; i < attempts; i++ {
		if _, err := os.Stat(localPath); err == nil {
			fmt.Printf("📂 Media already exists locally for %s, skipping download (attempt %d).\n", id, i+1)
			return fmt.Sprintf("/uploads/media/%s", filename), mimeType, nil
		}
		if isFromMe && i < attempts-1 {
			fmt.Printf("⏳ Outgoing media not found yet, waiting... (attempt %d)\n", i+1)
			time.Sleep(500 * time.Millisecond)
		}
	}

	// 4. Download if not exists
	fmt.Printf("🎬 Start downloading media for msg %s\n", id)
	var payload []byte
	var err error

	if msg.ImageMessage != nil {
		payload, err = client.WAClient.Download(context.Background(), msg.ImageMessage)
	} else if msg.DocumentMessage != nil {
		payload, err = client.WAClient.Download(context.Background(), msg.DocumentMessage)
	}

	if err != nil {
		fmt.Printf("❌ Error downloading media %s: %v\n", id, err)
		return "", "", err
	}

	fmt.Printf("✅ Media downloaded successfully for %s, saving to disk...\n", id)

	if err := os.WriteFile(localPath, payload, 0644); err != nil {
		return "", "", err
	}

	// Return URL relative to server root
	return fmt.Sprintf("/uploads/media/%s", filename), mimeType, nil
}

// truncate shortens a string for logging
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
