package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"time"

	"wa-server-go/internal/firestore"
	"wa-server-go/internal/utils"
	"wa-server-go/internal/whatsapp"

	"github.com/gin-gonic/gin"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// SendInvoiceRequest represents the request body for /send-invoice
type SendInvoiceRequest struct {
	Number    string `json:"number" binding:"required"`
	Message   string `json:"message" binding:"required"`
	PdfURL    string `json:"pdfUrl,omitempty"`
	PdfBase64 string `json:"pdfBase64,omitempty"`
	FileName  string `json:"fileName,omitempty"`
	ClientName string `json:"clientName,omitempty"`
}

// SendMessageRequest represents the request body for /send-message
type SendMessageRequest struct {
	Number  string `json:"number,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Message string `json:"message" binding:"required"`
}

// SendMediaRequest represents the request body for /send-media
type SendMediaRequest struct {
	Number    string `json:"number" binding:"required"`
	MediaURL  string `json:"mediaUrl" binding:"required"`
	Caption   string `json:"caption,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

// SendInvoice handles POST /send-invoice
func (h *Handler) SendInvoice(c *gin.Context) {
	var req SendInvoiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	botClient, ok := h.WAManager.GetClient("bot")
	if !ok || !botClient.IsReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "WhatsApp Bot client is not ready",
		})
		return
	}

	ctx := context.Background()

	// Format phone number and create JID
	jid := utils.PhoneToJID(req.Number)

	// Normalize message newlines
	normalizedMessage := utils.NormalizeNewlines(req.Message)

	// Anti-bot: Simulate typing indicator to appear more human-like
	// 1. Send "composing" (typing) presence
	_ = botClient.WAClient.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	// 2. Wait based on message length (simulates typing time)
	typingDelay := len(normalizedMessage) * 50 // ~50ms per character
	if typingDelay < 2000 {
		typingDelay = 2000
	}
	if typingDelay > 8000 {
		typingDelay = 8000
	}
	utils.HumanizeDelay(typingDelay, typingDelay+2000)
	
	// 3. Stop typing indicator
	_ = botClient.WAClient.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)

	// Send text message
	resp, err := botClient.WAClient.SendMessage(ctx, jid, &waProto.Message{
		Conversation: proto.String(normalizedMessage),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to send message: %v", err),
		})
		return
	}

	// Update Chat Name if provided
	chatName := jid.User // Default to phone number
	if req.ClientName != "" {
		chatName = req.ClientName
		if h.Repo != nil {
			_ = h.Repo.UpdateChatName(ctx, jid.String(), req.ClientName)
		}
	}

	// Manual Save & Broadcast for Text (Fail-safe)
	go func() {
		dbMsg := &firestore.WAMessage{
			MessageID: resp.ID,
			ChatID:    jid.String(),
			From:      botClient.WAClient.Store.ID.ToNonAD().String(),
			To:        jid.String(),
			Body:      normalizedMessage,
			Timestamp: resp.Timestamp,
			FromMe:    true,
			HasMedia:  false,
			Type:      "text",
			Ack:       1,
		}
		if h.Repo != nil {
			_ = h.Repo.SaveMessage(context.Background(), dbMsg)
			_ = h.Repo.SetChatHasInvoice(context.Background(), jid.String(), true)
		}

		h.WAManager.BroadcastMessage(whatsapp.NewMessageEvent{
			Client:    "bot",
			ID:        resp.ID,
			From:      dbMsg.From,
			To:        dbMsg.To,
			Body:      dbMsg.Body,
			Timestamp: resp.Timestamp.Unix(),
			FromMe:    true,
			ChatID:    jid.String(),
			ChatName:  chatName,
			HasMedia:  false,
			Type:      "text",
		})
	}()

	// Send PDF if provided
	pdfSent := false
	if req.PdfBase64 != "" || req.PdfURL != "" {
		pdfSent = h.sendPDF(ctx, botClient, jid, req.PdfBase64, req.PdfURL, req.FileName, chatName)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Invoice sent successfully",
		"pdfSent": pdfSent,
	})
}

// sendPDF uploads and sends a PDF document
func (h *Handler) sendPDF(ctx context.Context, client *whatsapp.Client, jid types.JID, base64Data, url, fileName, chatName string) bool {
	var pdfData []byte
	var err error

	if base64Data != "" {
		pdfData, err = base64.StdEncoding.DecodeString(base64Data)
		if err != nil {
			fmt.Printf("❌ Error decoding PDF base64: %v\n", err)
			return false
		}
	} else if url != "" {
		resp, err := http.Get(url)
		if err != nil {
			fmt.Printf("❌ Error downloading PDF from URL: %v\n", err)
			return false
		}
		defer resp.Body.Close()

		pdfData, err = io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("❌ Error reading PDF response: %v\n", err)
			return false
		}
	}

	if len(pdfData) == 0 {
		return false
	}

	// Upload to WhatsApp
	uploaded, err := client.WAClient.Upload(ctx, pdfData, whatsmeow.MediaDocument)
	if err != nil {
		fmt.Printf("❌ Error uploading PDF: %v\n", err)
		return false
	}

	// Set filename
	if fileName == "" {
		fileName = fmt.Sprintf("Invoice-%d.pdf", time.Now().Unix())
	}

	// Send document message
	resp, err := client.WAClient.SendMessage(ctx, jid, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(uploaded.URL),
			Mimetype:      proto.String("application/pdf"),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(pdfData))),
			Caption:       proto.String("Berikut terlampir dokumen invoice Anda."),
		},
	})
	if err != nil {
		fmt.Printf("❌ Error sending PDF: %v\n", err)
		return false
	}

	// Save file locally for history display (using Message ID)
	// ext must match what events.go expects (.pdf for documents with 'pdf' mime/name)
	localFileName := fmt.Sprintf("%s.pdf", resp.ID)
	uploadsDir := "./uploads/media"
	_ = os.MkdirAll(uploadsDir, 0755)
	localPath := fmt.Sprintf("%s/%s", uploadsDir, localFileName)
	
	if err := os.WriteFile(localPath, pdfData, 0644); err != nil {
		fmt.Printf("⚠️ Failed to save outgoing PDF locally: %v\n", err)
	} else {
		fmt.Printf("✅ Outgoing PDF saved locally: %s\n", localFileName)
	}

	// Manually Save & Broadcast to ensure visibility (Bypass missing Echo)
	go func() {
		// 1. Save to DB
		dbMsg := &firestore.WAMessage{
			MessageID: resp.ID,
			ChatID:    jid.String(),
			From:      client.WAClient.Store.ID.ToNonAD().String(),
			To:        jid.String(),
			Body:      "[Document] " + fileName,
			Timestamp: resp.Timestamp,
			FromMe:    true,
			HasMedia:  true,
			MediaType: "application/pdf",
			MediaURL:  fmt.Sprintf("/uploads/media/%s", localFileName),
			Type:      "document",
			Ack:       1,
		}
		if h.Repo != nil {
			_ = h.Repo.SaveMessage(context.Background(), dbMsg)
			_ = h.Repo.SetChatHasInvoice(context.Background(), jid.String(), true)
		}

		// 2. Broadcast WS
		h.WAManager.BroadcastMessage(whatsapp.NewMessageEvent{
			Client:    "bot",
			ID:        resp.ID,
			From:      dbMsg.From,
			To:        dbMsg.To,
			Body:      dbMsg.Body,
			Timestamp: resp.Timestamp.Unix(),
			FromMe:    true,
			ChatID:    jid.String(),
			ChatName:  chatName,
			HasMedia:  true,
			Type:      "document",
		})
	}()

	fmt.Println("✅ PDF sent successfully")
	return true
}

// SendMessage handles POST /send-message
func (h *Handler) SendMessage(c *gin.Context) {
	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	// Support both "number" and "phone" field names
	targetPhone := req.Number
	if targetPhone == "" {
		targetPhone = req.Phone
	}
	if targetPhone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "number or phone is required"})
		return
	}

	botClient, ok := h.WAManager.GetClient("bot")
	if !ok || !botClient.IsReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "WhatsApp Bot client is not ready",
		})
		return
	}

	ctx := context.Background()
	jid := utils.PhoneToJID(targetPhone)
	normalizedMessage := utils.NormalizeNewlines(req.Message)

	// Anti-bot: Simulate typing indicator to appear more human-like
	_ = botClient.WAClient.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	// Wait based on message length
	typingDelay := len(normalizedMessage) * 40
	if typingDelay < 1000 {
		typingDelay = 1000
	}
	if typingDelay > 5000 {
		typingDelay = 5000
	}
	utils.HumanizeDelay(typingDelay, typingDelay+1000)
	
	_ = botClient.WAClient.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)

	resp, err := botClient.WAClient.SendMessage(ctx, jid, &waProto.Message{
		Conversation: proto.String(normalizedMessage),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Failed to send message: %v", err),
		})
		return
	}

	// Manual Save & Broadcast (Ensure "Live" Chat Visibility)
	go func() {
		dbMsg := &firestore.WAMessage{
			MessageID: resp.ID,
			ChatID:    jid.String(),
			From:      botClient.WAClient.Store.ID.ToNonAD().String(),
			To:        jid.String(),
			Body:      normalizedMessage,
			Timestamp: resp.Timestamp,
			FromMe:    true,
			HasMedia:  false,
			Type:      "text",
			Ack:       1,
		}
		if h.Repo != nil {
			_ = h.Repo.SaveMessage(context.Background(), dbMsg)
		}

		h.WAManager.BroadcastMessage(whatsapp.NewMessageEvent{
			Client:    "bot",
			ID:        resp.ID,
			From:      dbMsg.From,
			To:        dbMsg.To,
			Body:      dbMsg.Body,
			Timestamp: resp.Timestamp.Unix(),
			FromMe:    true,
			ChatID:    jid.String(),
			ChatName:  utils.JIDToPhoneNumber(jid), // Use phone as fallback name
			HasMedia:  false,
			Type:      "text",
		})
	}()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Message sent successfully",
	})
}

// SendMedia handles POST /send-media
func (h *Handler) SendMedia(c *gin.Context) {
	var req SendMediaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	botClient, ok := h.WAManager.GetClient("bot")
	if !ok || !botClient.IsReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "WhatsApp Bot client is not ready",
		})
		return
	}

	ctx := context.Background()
	jid := utils.PhoneToJID(req.Number)

	// Download media from URL
	resp, err := http.Get(req.MediaURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to download media"})
		return
	}
	defer resp.Body.Close()

	mediaData, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to read media"})
		return
	}

	contentType := resp.Header.Get("Content-Type")

	// Anti-bot: Simulate media upload/typing presence
	_ = botClient.WAClient.SendChatPresence(ctx, jid, types.ChatPresenceComposing, types.ChatPresenceMediaText)

	// Anti-bot: Human-like delay
	utils.HumanizeDelay(2000, 4000) // Increase slightly for media

	_ = botClient.WAClient.SendChatPresence(ctx, jid, types.ChatPresencePaused, types.ChatPresenceMediaText)

	// Determine media type and upload
	if isImageMime(contentType) {
		uploaded, err := botClient.WAClient.Upload(ctx, mediaData, whatsmeow.MediaImage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to upload image"})
			return
		}

		resp, err := botClient.WAClient.SendMessage(ctx, jid, &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				URL:           proto.String(uploaded.URL),
				Mimetype:      proto.String(contentType),
				Caption:       proto.String(req.Caption),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(mediaData))),
			},
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to send image"})
			return
		}

		// Save locally (ID.jpg)
		localFileName := fmt.Sprintf("%s.jpg", resp.ID)
		uploadsDir := "./uploads/media"
		_ = os.MkdirAll(uploadsDir, 0755)
		localPath := fmt.Sprintf("%s/%s", uploadsDir, localFileName)
		_ = os.WriteFile(localPath, mediaData, 0644)

		// Manual Save & Broadcast for Image
		go func() {
			dbMsg := &firestore.WAMessage{
				MessageID: resp.ID,
				ChatID:    jid.String(),
				From:      botClient.WAClient.Store.ID.ToNonAD().String(),
				To:        jid.String(),
				Body:      "[Image] " + req.Caption,
				Timestamp: resp.Timestamp,
				FromMe:    true,
				HasMedia:  true,
				MediaType: contentType,
				MediaURL:  fmt.Sprintf("/uploads/media/%s", localFileName),
				Type:      "image",
				Ack:       1,
			}
			if h.Repo != nil {
				_ = h.Repo.SaveMessage(context.Background(), dbMsg)
			}
			h.WAManager.BroadcastMessage(whatsapp.NewMessageEvent{
				Client:    "bot",
				ID:        resp.ID,
				From:      dbMsg.From,
				To:        dbMsg.To,
				Body:      dbMsg.Body,
				Timestamp: resp.Timestamp.Unix(),
				FromMe:    true,
				ChatID:    jid.String(),
				ChatName:  utils.JIDToPhoneNumber(jid),
				HasMedia:  true,
				Type:      "image",
			})
		}()
	} else {
		// Send as document
		uploaded, err := botClient.WAClient.Upload(ctx, mediaData, whatsmeow.MediaDocument)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to upload document"})
			return
		}

	resp, err := botClient.WAClient.SendMessage(ctx, jid, &waProto.Message{
			DocumentMessage: &waProto.DocumentMessage{
				URL:           proto.String(uploaded.URL),
				Mimetype:      proto.String(contentType),
				Caption:       proto.String(req.Caption),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(mediaData))),
			},
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to send document"})
			return
		}

		// Save locally - try to guess ext based on content-type or default to .bin
		// events.go tries to guess from filename or mimetype
		exts, _ := mime.ExtensionsByType(contentType)
		ext := ".bin"
		if len(exts) > 0 {
			ext = exts[0]
		}
		
		localFileName := fmt.Sprintf("%s%s", resp.ID, ext)
		uploadsDir := "./uploads/media"
		_ = os.MkdirAll(uploadsDir, 0755)
		localPath := fmt.Sprintf("%s/%s", uploadsDir, localFileName)
		_ = os.WriteFile(localPath, mediaData, 0644)

		// Manual Save & Broadcast for Document
		go func() {
			dbMsg := &firestore.WAMessage{
				MessageID: resp.ID,
				ChatID:    jid.String(),
				From:      botClient.WAClient.Store.ID.ToNonAD().String(),
				To:        jid.String(),
				Body:      "[Document] " + req.Caption,
				Timestamp: resp.Timestamp,
				FromMe:    true,
				HasMedia:  true,
				MediaType: contentType,
				MediaURL:  fmt.Sprintf("/uploads/media/%s", localFileName),
				Type:      "document",
				Ack:       1,
			}
			if h.Repo != nil {
				_ = h.Repo.SaveMessage(context.Background(), dbMsg)
			}
			h.WAManager.BroadcastMessage(whatsapp.NewMessageEvent{
				Client:    "bot",
				ID:        resp.ID,
				From:      dbMsg.From,
				To:        dbMsg.To,
				Body:      dbMsg.Body,
				Timestamp: resp.Timestamp.Unix(),
				FromMe:    true,
				ChatID:    jid.String(),
				ChatName:  utils.JIDToPhoneNumber(jid),
				HasMedia:  true,
				Type:      "document",
			})
		}()
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Media sent successfully",
	})
}

func isImageMime(mime string) bool {
	return mime == "image/jpeg" || mime == "image/png" || mime == "image/gif" || mime == "image/webp"
}
