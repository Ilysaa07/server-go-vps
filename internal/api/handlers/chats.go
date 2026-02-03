package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// GetChats handles GET /get-chats
func (h *Handler) GetChats(c *gin.Context) {
	if h.Repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "Chat storage (Firestore) is not configured",
		})
		return
	}

	chats, err := h.Repo.GetRecentChats(c.Request.Context(), 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch chats from storage",
			"details": err.Error(),
		})
		return
	}

	// Map to frontend format
	mappedChats := make([]map[string]interface{}, 0)

	// Fetch profile pics for those missing them (Async)
	botClient, _ := h.WAManager.GetClient("bot")
	canFetch := botClient != nil && botClient.IsReady()

	for _, chat := range chats {
		profilePic := chat.ProfilePicURL
		
		// If empty, try to fetch in background (only if client is ready)
		if profilePic == "" && canFetch {
			go func(jidStr string) {
				jid, err := types.ParseJID(jidStr)
				if err != nil {
					return
				}
				pic, err := botClient.WAClient.GetProfilePictureInfo(context.Background(), jid, &whatsmeow.GetProfilePictureParams{
					Preview: true, // Use thumbnail for list
				})
				if err != nil {
					// fmt.Printf("⚠️ Profile pic fetch failed for %s: %v\n", jidStr, err)
				}
				if err == nil && pic != nil && pic.URL != "" {
					fmt.Printf("📸 Profile pic fetched for %s\n", jidStr)
					// Update DB so next fetch has it
					_ = h.Repo.UpdateChatProfilePic(context.Background(), jidStr, pic.URL)
					
					// Broadcast update to frontend for instant display
					if h.WSHub != nil {
						h.WSHub.Broadcast("chat-update", gin.H{
							"id":            jidStr,
							"profilePicUrl": pic.URL,
						})
					}
				}
			}(chat.JID)
		}

		// Improve Name Resolution
		displayName := chat.Name
		if (displayName == "" || displayName == chat.Number || displayName == "Unknown") && canFetch {
			// Try to resolve name from contact store
			jid, _ := types.ParseJID(chat.JID)
			contact, err := botClient.WAClient.Store.Contacts.GetContact(c.Request.Context(), jid)
			if err == nil && contact.Found {
				newName := ""
				if contact.PushName != "" {
					newName = contact.PushName
				} else if contact.FullName != "" {
					newName = contact.FullName
				} else if contact.BusinessName != "" {
					newName = contact.BusinessName
				}

				if newName != "" {
					displayName = newName
					// Update DB async
					go func(id, name string) {
						// h.Repo.UpdateChatName(context.Background(), id, name) // Assuming method exists or just let it cache next time
						// For now we don't have UpdateChatName expose? 
						// Actually we just want live update on frontend.
						
						if h.WSHub != nil {
							h.WSHub.Broadcast("chat-update", gin.H{
								"id":   id,
								"name": name,
							})
						}
					}(chat.JID, newName)
				}
			}
		}

		mappedChats = append(mappedChats, map[string]interface{}{
			"id":            chat.JID,
			"name":          displayName,
			"number":        chat.Number,
			"unreadCount":   chat.UnreadCount,
			"profilePicUrl": profilePic,
			"timestamp":     chat.LastMessageAt.Unix(),
			"lastMessage": map[string]interface{}{
				"body":      chat.LastMessageBody,
				"timestamp": chat.LastMessageAt.Unix(),
				// "fromMe": chat.LastMessageFromMe, // Need to add this to WAChat struct if not exists
			},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"chats":   mappedChats,
		"total":   len(mappedChats),
	})
}

// GetMessages handles GET /get-messages/:chatId
func (h *Handler) GetMessages(c *gin.Context) {
	if h.Repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "Chat storage (Firestore) is not configured",
		})
		return
	}

	chatId := c.Param("chatId")
	messages, err := h.Repo.GetChatMessages(c.Request.Context(), chatId, 50)
	if err != nil {
		fmt.Printf("❌ Failed to fetch messages for %s: %v\n", chatId, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch messages",
			"details": err.Error(),
		})
		return
	}

	// Map to frontend format
	mappedMessages := make([]map[string]interface{}, 0)
	for _, msg := range messages {
		mappedMessages = append(mappedMessages, map[string]interface{}{
			"id":        msg.MessageID,
			"body":      msg.Body,
			"fromMe":    msg.FromMe,
			"timestamp": msg.Timestamp.Unix(),
			"type":      msg.Type,
			"ack":       msg.Ack,
			"hasMedia":  msg.HasMedia,
			"mediaUrl":  msg.MediaURL,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"messages": mappedMessages,
	})
}

// GetMedia handles GET /get-media/:messageId
func (h *Handler) GetMedia(c *gin.Context) {
	// ... (Media download logic needs whatsmeow helper not just firestore)
	c.JSON(http.StatusNotImplemented, gin.H{
		"success": false,
		"error":   "Media download is being implemented",
	})
}

// GetInvoiceChats handles GET /get-invoice-chats
func (h *Handler) GetInvoiceChats(c *gin.Context) {
	if h.Repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "Chat storage (Firestore) is not configured",
		})
		return
	}

	// For now, reuse GetRecentChats (filtering should happen in Repo or here)
	// Ideally we filter for chats containing "INV-"
	chats, err := h.Repo.GetInvoiceChats(c.Request.Context(), 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to fetch invoice chats",
			"details": err.Error(),
		})
		return
	}

	// Map to frontend format
	mappedChats := make([]map[string]interface{}, 0)
	for _, chat := range chats {
		mappedChats = append(mappedChats, map[string]interface{}{
			"id":            chat.JID,
			"name":          chat.Name,
			"number":        chat.Number,
			"unreadCount":   chat.UnreadCount,
			"profilePicUrl": chat.ProfilePicURL,
			"timestamp":     chat.LastMessageAt.Unix(),
			"lastMessage": map[string]interface{}{
				"body":      chat.LastMessageBody,
				"timestamp": chat.LastMessageAt.Unix(),
			},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"chats":   mappedChats,
	})
}
