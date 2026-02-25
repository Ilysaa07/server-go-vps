package chatbot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"wa-server-go/internal/config"
	"wa-server-go/internal/firestore"
	"wa-server-go/internal/utils"
	"wa-server-go/internal/whatsapp"
)

// Handler contains HTTP handlers for the chatbot
type Handler struct {
	sessionManager *SessionManager
	hub            *ChatHub
	leadsRepo      *firestore.LeadsRepository
	waManager      *whatsapp.Manager
	settingsRepo   *firestore.SettingsRepository
	config         *config.Config
}

// NewHandler creates a new chatbot handler
func NewHandler(sm *SessionManager, hub *ChatHub, leadsRepo *firestore.LeadsRepository, settingsRepo *firestore.SettingsRepository, waManager *whatsapp.Manager, cfg *config.Config) *Handler {
	return &Handler{
		sessionManager: sm,
		hub:            hub,
		leadsRepo:      leadsRepo,
		settingsRepo:   settingsRepo,
		waManager:      waManager,
		config:         cfg,
	}
}

// StartChatRequest represents a request to start a chat session
type StartChatRequest struct {
	VisitorID    string `json:"visitorId" binding:"required"`
	VisitorName  string `json:"visitorName" binding:"required"`
	VisitorEmail string `json:"visitorEmail"`
	VisitorPhone    string `json:"visitorPhone"`
	VisitorLocation string `json:"visitorLocation"` // City, Country
	CurrentPage     string `json:"currentPage"`     // Metadata
}

// SendMessageRequest represents a request to send a chat message
type SendMessageRequest struct {
	SessionID string `json:"sessionId" binding:"required"`
	Content   string `json:"content" binding:"required"`
}

// HandoverRequest represents a request for admin handover
type HandoverRequest struct {
	SessionID string `json:"sessionId" binding:"required"`
}

// StartChat handles POST /chat/start
func (h *Handler) StartChat(c *gin.Context) {
	var req StartChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request"})
		return
	}

	// Save/Update Lead if phone provided (Sync with Broadcast)
	if req.VisitorPhone != "" {
		phone := sanitizePhone(req.VisitorPhone)
		
		// Check if exists
		existing, err := h.leadsRepo.GetByPhone(c.Request.Context(), phone)
		if err == nil {
			if existing != nil {
				// Update existing: Ensure "leads" tag exists
				updates := map[string]interface{}{}
				hasTag := false
				for _, t := range existing.Tags {
					if t == "leads" {
						hasTag = true
						break
					}
				}
				if !hasTag {
					updates["tags"] = append(existing.Tags, "leads")
				}
				if req.VisitorName != "" && existing.Name != req.VisitorName {
					updates["name"] = req.VisitorName
				}
				
				if len(updates) > 0 {
					h.leadsRepo.Update(c.Request.Context(), existing.ID, updates)
				}
			} else {
				// Create new lead
				newLead := &firestore.Lead{
					Phone:  phone,
					Name:   req.VisitorName,
					Tags:   []string{"leads"}, // Tag for broadcast
					Source: "web_chat",
				}
				h.leadsRepo.Create(c.Request.Context(), newLead)
			}
		} else {
            log.Printf("⚠️ Failed to check lead existence: %v", err)
        }
	}

	// Check for existing active session
	existing, err := h.sessionManager.GetSessionByVisitorID(c.Request.Context(), req.VisitorID)
	if err != nil {
		log.Printf("Error checking existing session: %v", err)
	}

	if existing != nil {
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"sessionId": existing.ID,
			"status":    existing.Status,
			"resumed":   true,
		})
		return
	}

	// Create new session
	location := req.VisitorLocation
	if location == "" {
		location = "Unknown Location" 
		// TODO: Implement server-side IP geolocation if needed
	}

	session, err := h.sessionManager.CreateSession(c.Request.Context(), req.VisitorID, req.VisitorName, req.VisitorEmail, req.VisitorPhone, req.CurrentPage, location)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to create session"})
		return
	}

	log.Printf("💬 New chat session started: %s (%s)", session.VisitorName, session.ID)

	// Save welcome message to DB
	welcomeMsg := &ChatMessage{
		SessionID: session.ID,
		Sender:    "bot",
		Content:   fmt.Sprintf("Halo %s! 👋 Saya asisten virtual Valpro. Ada yang bisa saya bantu hari ini?", req.VisitorName),
		Timestamp: time.Now(),
	}
	if err := h.sessionManager.SaveMessage(c.Request.Context(), welcomeMsg); err != nil {
		log.Printf("Failed to save welcome message: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"sessionId": session.ID,
		"status":    session.Status,
	})
}

// SendMessage handles POST /chat/message
func (h *Handler) SendMessage(c *gin.Context) {
	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request"})
		return
	}

	// Get session
	session, err := h.sessionManager.GetSession(c.Request.Context(), req.SessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Session not found"})
		return
	}

	// Create and Save Message (Ensure persistence and generate ID)
	msg := &ChatMessage{
		SessionID: req.SessionID,
		Sender:    "visitor",
		Content:   req.Content,
		Timestamp: time.Now(),
	}

	// Always save visitor messages (persistence)
	if err := h.sessionManager.SaveMessage(c.Request.Context(), msg); err != nil {
		log.Printf("❌ Failed to save visitor message: %v", err)
		// Continue to broadcast even if save fails, but log it
	}

	// Broadcast visitor message to admin (if live or queued)
	log.Printf("📨 Visitor message: %s (Status: %s)", req.Content, session.Status)
	if session.Status == StatusLive || session.Status == StatusQueued {
		log.Printf("📢 Broadcasting to admins...")
		h.hub.BroadcastToAdmins("chat-message", gin.H{
			"id":        msg.ID, // Include ID used for deduplication
			"sessionId": req.SessionID,
			"content":   req.Content,
			"sender":    "visitor",
			"timestamp": msg.Timestamp,
		})
	} else {
		log.Printf("⚠️ Message NOT broadcast: Status is %s (not live/queued)", session.Status)
	}

	// If not in bot mode, we are done (message saved & broadcasted)
	if session.Status != StatusBot {
		// BRIDGE: Forward to WhatsApp Agent if configured
		// BRIDGE: Forward to WhatsApp Agent if configured
		agentPhone := h.getAgentPhone()
		if agentPhone != "" && h.waManager != nil { 
			// Use simple "628xxx" format from config
			botClient, ok := h.waManager.GetClient("bot")
			if ok && botClient.IsReady() {
				// Format:
				// 👤 *Name* (WA: 08xxx)
				// 📍 Location
				// 📄 Page: /url
				//
				// Message...
				//
				// 🆔 #session_id
				
				visitorInfo := fmt.Sprintf("Nama: %s", session.VisitorName)
				if session.VisitorPhone != "" {
					visitorInfo += fmt.Sprintf("\nWA: %s", session.VisitorPhone)
				}
				if session.Location != "" {
					visitorInfo += fmt.Sprintf("\nLokasi: %s", session.Location)
				}
				if session.CurrentPage != "" {
					visitorInfo += fmt.Sprintf("\nHalaman: %s", session.CurrentPage)
				}

				// Clean, minimalist forwarding format
				msgText := fmt.Sprintf("%s\n\n%s\n\nID: #%s", visitorInfo, req.Content, req.SessionID)
				
				// Send to Agent
				// Append @s.whatsapp.net if not present
				targetJID := agentPhone
				if !strings.HasSuffix(targetJID, "@s.whatsapp.net") {
					targetJID += "@s.whatsapp.net"
				}
				
				go botClient.SendTextMessage(context.Background(), targetJID, msgText)
			}
		}

		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Process with AI
	response, err := h.sessionManager.ProcessMessage(c.Request.Context(), req.SessionID, req.Content)
	if err != nil {
		log.Printf("Error processing message: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to process message"})
		return
	}

	// Broadcast bot response
	h.hub.BroadcastToSession(req.SessionID, "chat-message", gin.H{
		"sessionId": req.SessionID,
		"content":   response.Reply,
		"sender":    "bot",
		"timestamp": time.Now(),
	})

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"reply":           response.Reply,
		"suggestHandover": response.SuggestHandover,
		"sentiment":       response.Sentiment,
	})
}

// VisitorReturnToBot handles POST /chat/return-to-bot (visitor-side)
func (h *Handler) VisitorReturnToBot(c *gin.Context) {
	var req struct {
		SessionID string `json:"sessionId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request"})
		return
	}

	if err := h.sessionManager.ReturnToBot(c.Request.Context(), req.SessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to return to bot"})
		return
	}

	// Notify via WebSocket
	h.hub.BroadcastToSession(req.SessionID, "status-change", gin.H{
		"status": "bot",
	})

	log.Printf("🤖 Visitor returned session %s to AI", req.SessionID)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// VisitorEndSession handles POST /chat/end-session (visitor-side)
func (h *Handler) VisitorEndSession(c *gin.Context) {
	var req struct {
		SessionID string `json:"sessionId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request"})
		return
	}

	if err := h.sessionManager.CloseSession(c.Request.Context(), req.SessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to end session"})
		return
	}

	// Notify visitor via WebSocket
	h.hub.BroadcastToSession(req.SessionID, "status-change", gin.H{
		"status": "closed",
	})

	// Notify admins that session was closed by visitor
	h.hub.BroadcastToAdmins("session-closed", gin.H{
		"sessionId": req.SessionID,
		"closedBy":  "visitor",
	})
	
	// Also broadcast queue update so admin UI refreshes
	h.hub.BroadcastToAdmins("queue-update", gin.H{
		"type":      "removed",
		"sessionId": req.SessionID,
	})

	log.Printf("✅ Visitor ended session %s", req.SessionID)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RequestHandover handles POST /chat/handover
func (h *Handler) RequestHandover(c *gin.Context) {
	var req HandoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request"})
		return
	}

	adminOnline, err := h.sessionManager.RequestHandover(c.Request.Context(), req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to request handover"})
		return
	}

	// Notify all admins about new queue item
	session, _ := h.sessionManager.GetSession(c.Request.Context(), req.SessionID)
	h.hub.BroadcastToAdmins("queue-update", gin.H{
		"type":    "new",
		"session": session,
	})

	log.Printf("🔔 Handover requested for session %s (admin online: %v)", req.SessionID, adminOnline)

	// NOTIFICATION: Send WhatsApp to Agent
	agentPhone := h.getAgentPhone()
	if agentPhone != "" && h.waManager != nil {
		botClient, ok := h.waManager.GetClient("bot")
		if ok && botClient.IsReady() {
			visitorInfo := fmt.Sprintf("Nama: %s", session.VisitorName)
			if session.VisitorPhone != "" {
				visitorInfo += fmt.Sprintf("\nWA: %s", session.VisitorPhone)
			}
			if session.Location != "" {
				visitorInfo += fmt.Sprintf("\nLokasi: %s", session.Location)
			}
			if session.CurrentPage != "" {
				visitorInfo += fmt.Sprintf("\nHalaman: %s", session.CurrentPage)
			}
			
			// Get AI Summary
			aiSummary, _ := h.sessionManager.GenerateAISummary(context.Background(), req.SessionID)
			
			// Minimalist, user-friendly template with ID in header for safety
			msgText := fmt.Sprintf("*Permintaan Chat Baru (#%s)*\n\n%s\n\n*Ringkasan AI:*\n%s\n\nBalas pesan ini untuk terhubung.", req.SessionID, visitorInfo, aiSummary)
			
			targetJID := agentPhone
			if !strings.HasSuffix(targetJID, "@s.whatsapp.net") {
				targetJID += "@s.whatsapp.net"
			}
			
			log.Printf("📢 Sending WhatsApp notification to %s for session %s", targetJID, req.SessionID)
			
			go func() {
				if err := botClient.SendTextMessage(context.Background(), targetJID, msgText); err != nil {
					log.Printf("❌ Failed to send WhatsApp notification: %v", err)
				} else {
					log.Printf("✅ WhatsApp notification sent successfully to %s", targetJID)
				}
			}()
		} else {
			log.Printf("⚠️ WhatsApp notification skipped: Bot client not ready or not found")
		}
	} else {
		log.Printf("⚠️ WhatsApp notification skipped: No agent phone configured or WA manager nil")
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"adminOnline": adminOnline,
	})
}

// GetQueue handles GET /admin-chat/queue
func (h *Handler) GetQueue(c *gin.Context) {
	sessions, err := h.sessionManager.GetQueuedSessions(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to get queue"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"sessions": sessions,
		"count":    len(sessions),
	})
}

// ClaimChat handles POST /admin-chat/claim/:sessionId
func (h *Handler) ClaimChat(c *gin.Context) {
	sessionID := c.Param("sessionId")
	adminID := c.GetString("adminId")     // Set by auth middleware
	adminName := c.GetString("adminName") // Set by auth middleware

	if adminID == "" {
		adminID = "admin_default"
		adminName = "Admin"
	}

	if err := h.sessionManager.ClaimSession(c.Request.Context(), sessionID, adminID, adminName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to claim session"})
		return
	}

	// Notify visitor
	h.hub.BroadcastToSession(sessionID, "admin-joined", gin.H{
		"adminName": adminName,
	})

	// Notify other admins
	h.hub.BroadcastToAdmins("queue-update", gin.H{
		"type":      "claimed",
		"sessionId": sessionID,
		"adminId":   adminID,
	})

	// Generate summary for admin
	summary, _ := h.sessionManager.GenerateAISummary(c.Request.Context(), sessionID)

	log.Printf("👨‍💼 Session %s claimed by %s", sessionID, adminName)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"summary": summary,
	})
}

// CloseChat handles POST /admin-chat/close/:sessionId
func (h *Handler) CloseChat(c *gin.Context) {
	sessionID := c.Param("sessionId")

	if err := h.sessionManager.CloseSession(c.Request.Context(), sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to close session"})
		return
	}

	// Notify visitor
	h.hub.BroadcastToSession(sessionID, "status-change", gin.H{
		"status": "closed",
	})

	log.Printf("✅ Session %s closed", sessionID)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ReturnToBot handles POST /admin-chat/return-to-bot/:sessionId
func (h *Handler) ReturnToBot(c *gin.Context) {
	sessionID := c.Param("sessionId")

	if err := h.sessionManager.ReturnToBot(c.Request.Context(), sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to return to bot"})
		return
	}

	// Notify visitor that they are now chatting with AI
	h.hub.BroadcastToSession(sessionID, "status-change", gin.H{
		"status": "bot",
	})

	log.Printf("🤖 Session %s returned to AI bot", sessionID)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CleanupSessions cleans up inactive sessions
func (h *Handler) CleanupSessions(ctx context.Context, duration time.Duration) error {
	return h.sessionManager.CleanupInactiveSessions(ctx, duration)
}

// AdminSendMessage handles POST /admin-chat/message
func (h *Handler) AdminSendMessage(c *gin.Context) {
	var req struct {
		SessionID string `json:"sessionId" binding:"required"`
		Content   string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request"})
		return
	}

	adminName := c.GetString("adminName")
	if adminName == "" {
		adminName = "Admin"
	}

	// Save message
	msg := &ChatMessage{
		SessionID: req.SessionID,
		Sender:    "admin",
		Content:   req.Content,
		Timestamp: time.Now(),
	}
	if err := h.sessionManager.SaveMessage(c.Request.Context(), msg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to save message"})
		return
	}

	// Broadcast to visitor
	h.hub.BroadcastToSession(req.SessionID, "chat-message", gin.H{
		"sessionId": req.SessionID,
		"content":   req.Content,
		"sender":    "admin",
		"timestamp": time.Now(),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// UpdateAdminStatus handles POST /admin-status/update
func (h *Handler) UpdateAdminStatus(c *gin.Context) {
	var req struct {
		AdminID   string `json:"adminId" binding:"required"`
		AdminName string `json:"adminName"`
		Status    string `json:"status" binding:"required"` // online, away, offline
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request"})
		return
	}

	h.sessionManager.UpdateAdminStatus(req.AdminID, req.AdminName, req.Status)

	log.Printf("👤 Admin %s status: %s", req.AdminID, req.Status)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetOnlineAdmins handles GET /admin-status/online
func (h *Handler) GetOnlineAdmins(c *gin.Context) {
	admins := h.sessionManager.GetOnlineAdmins()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"admins":  admins,
	})
}

// GetMessages handles GET /chat/history/:sessionId
func (h *Handler) GetMessages(c *gin.Context) {
	sessionID := c.Param("sessionId")

	messages, err := h.sessionManager.GetMessages(c.Request.Context(), sessionID, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to get messages"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"messages": messages,
	})
}

// ========== WebSocket Hub for Chat ==========

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // TODO: Check origin in production
	},
}

// ChatClient represents a WebSocket client
type ChatClient struct {
	hub       *ChatHub
	conn      *websocket.Conn
	send      chan []byte
	sessionID string
	isAdmin   bool
}

// ChatHub manages chat WebSocket connections
type ChatHub struct {
	visitors   map[string]*ChatClient // sessionID -> client
	admins     map[*ChatClient]bool
	broadcast  chan []byte
	register   chan *ChatClient
	unregister chan *ChatClient
	mu         sync.RWMutex
}

// NewChatHub creates a new chat hub
func NewChatHub() *ChatHub {
	return &ChatHub{
		visitors:   make(map[string]*ChatClient),
		admins:     make(map[*ChatClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *ChatClient),
		unregister: make(chan *ChatClient),
	}
}

// Run starts the hub's main loop
func (h *ChatHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			if client.isAdmin {
				h.admins[client] = true
				log.Printf("👨‍💼 Admin connected to chat hub. Total: %d", len(h.admins))
			} else {
				h.visitors[client.sessionID] = client
				log.Printf("👤 Visitor connected: %s", client.sessionID)
			}
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if client.isAdmin {
				if _, ok := h.admins[client]; ok {
					delete(h.admins, client)
					close(client.send)
				}
			} else {
				if existing, ok := h.visitors[client.sessionID]; ok && existing == client {
					delete(h.visitors, client.sessionID)
					close(client.send)
				}
			}
			h.mu.Unlock()
		}
	}
}

// BroadcastToSession sends a message to a specific session's visitor
func (h *ChatHub) BroadcastToSession(sessionID string, event string, data interface{}) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	msg := map[string]interface{}{
		"event": event,
		"data":  data,
	}
	jsonData, _ := json.Marshal(msg)

	if client, ok := h.visitors[sessionID]; ok {
		select {
		case client.send <- jsonData:
		default:
			// Channel full, skip
		}
	}
}

// BroadcastToAdmins sends a message to all connected admins
func (h *ChatHub) BroadcastToAdmins(event string, data interface{}) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	msg := map[string]interface{}{
		"event": event,
		"data":  data,
	}
	jsonData, _ := json.Marshal(msg)

	for client := range h.admins {
		select {
		case client.send <- jsonData:
		default:
			// Channel full, skip
		}
	}
}

// HandleVisitorWS handles WebSocket connections from visitors
func (h *ChatHub) HandleVisitorWS(c *gin.Context) {
	sessionID := c.Param("sessionId")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Session ID required"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := &ChatClient{
		hub:       h,
		conn:      conn,
		send:      make(chan []byte, 256),
		sessionID: sessionID,
		isAdmin:   false,
	}

	h.register <- client

	go client.writePump()
	go client.readPump()
}

// HandleAdminWS handles WebSocket connections from admins
func (h *ChatHub) HandleAdminWS(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := &ChatClient{
		hub:     h,
		conn:    conn,
		send:    make(chan []byte, 256),
		isAdmin: true,
	}

	h.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *ChatClient) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512 * 1024)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		// Process Message
		var evt map[string]interface{}
		if err := json.Unmarshal(message, &evt); err != nil {
			log.Printf("Invalid JSON from WS: %v", err)
			continue
		}

		eventType, _ := evt["type"].(string)

		if eventType == "typing" {
			// Broadcast typing status
			status, _ := evt["status"].(string)
			sessionID, _ := evt["sessionId"].(string)

			if c.isAdmin {
				// Admin typing -> Visitor
				c.hub.BroadcastToSession(sessionID, "typing", gin.H{
					"sessionId": sessionID,
					"sender":    "admin",
					"status":    status,
				})
			} else {
				// Visitor typing -> Admins
				c.hub.BroadcastToAdmins("typing", gin.H{
					"sessionId": c.sessionID, // Use client's session ID
					"sender":    "visitor",
					"status":    status,
				})
			}
		} else if eventType == "page-change" {
			// Visitor changed page -> Update Session & Notify Admins
			url, _ := evt["url"].(string)
			title, _ := evt["title"].(string)
			
			if !c.isAdmin {
				// Update session in DB (Async best effort)
				// We need access to sessionManager here. 
				// Since ChatClient doesn't have it directly, we can skip DB update for now or add it to Hub?
				// For now, just broadcast to live admins for real-time UI.
				
				c.hub.BroadcastToAdmins("page-change", gin.H{
					"sessionId": c.sessionID,
					"url":       url,
					"title":     title,
				})
				log.Printf("📄 Visitor %s changed page to: %s", c.sessionID, url)
			}
		}
	}
}

func (c *ChatClient) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)
			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// sanitizePhone normalizes phone number
func sanitizePhone(phone string) string {
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "+", "")
	
	if strings.HasPrefix(phone, "0") {
		phone = "62" + phone[1:]
	}
	
	return phone
}

// KnownAdmins contains hardcoded critical numbers for fallback authorization
var KnownAdmins = map[string]string{
	"6281399710085": "Angga Puziana",
	"6289518530306": "Ilyasa Meydiansyah",
	"6282258115474": "Valpro Intertech (Kantor)",
	"6282110100085": "Valpro Intertech (Backup)",
}

// isAuthorizedAdmin checks if a phone number or LID is authorized to control the bot
// If the input is a LID (ends with @lid), it will try to resolve it to a phone number first.
func (h *Handler) isAuthorizedAdmin(jidStr string) bool {
	// Extract the user part (before @)
	user := strings.Split(jidStr, "@")[0]
	server := ""
	if strings.Contains(jidStr, "@") {
		server = strings.Split(jidStr, "@")[1]
	}
	
	phone := sanitizePhone(user)
	
	// If this is a LID, try to resolve it to a phone number
	if server == "lid" || !looksLikePhone(user) {
		log.Printf("isAuthorizedAdmin: Input %s looks like LID, attempting resolution...", user)
		
		// Try to resolve via WhatsApp client
		if h.waManager != nil {
			if botClient, ok := h.waManager.GetClient("bot"); ok && botClient != nil && botClient.WAClient != nil {
				resolved, err := utils.ResolveLIDToPhoneNumber(botClient.WAClient, jidStr)
				if err == nil && resolved != "" {
					log.Printf("isAuthorizedAdmin: Resolved LID %s -> %s", user, resolved)
					phone = sanitizePhone(resolved)
				} else {
					log.Printf("isAuthorizedAdmin: Could not resolve LID %s: %v", user, err)
				}
			}
		}
	}
	
	if phone == "" {
		log.Printf("isAuthorizedAdmin: Empty phone after sanitization for %s", jidStr)
		return false
	}

	// 1. Check Hardcoded List
	if _, ok := KnownAdmins[phone]; ok {
		log.Printf("isAuthorizedAdmin: %s found in KnownAdmins", phone)
		return true
	}

	// 2. Check Dynamic Settings
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if h.settingsRepo != nil {
		settings, err := h.settingsRepo.GetWhatsAppSettings(ctx)
		if err == nil && settings != nil {
			if sanitizePhone(settings.AgentPhone) == phone {
				log.Printf("isAuthorizedAdmin: %s matches AgentPhone", phone)
				return true
			}
			if sanitizePhone(settings.MainNumber) == phone {
				log.Printf("isAuthorizedAdmin: %s matches MainNumber", phone)
				return true
			}
			if sanitizePhone(settings.SecondaryNumber) == phone {
				log.Printf("isAuthorizedAdmin: %s matches SecondaryNumber", phone)
				return true
			}
		}
	} else {
		// Fallback to config
		if sanitizePhone(h.config.AgentPhone) == phone {
			log.Printf("isAuthorizedAdmin: %s matches Config.AgentPhone", phone)
			return true
		}
	}

	log.Printf("isAuthorizedAdmin: %s (from %s) is NOT authorized", phone, jidStr)
	return false
}

// looksLikePhone checks if a string looks like a phone number (all digits, 10-15 chars)
func looksLikePhone(s string) bool {
	if len(s) < 10 || len(s) > 15 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// HandleAgentReply processes incoming WhatsApp messages from the Agent
// Authorization is implicit: if someone can quote a valid session message, they received the notification.
func (h *Handler) HandleAgentReply(evt whatsapp.NewMessageEvent) {
	// CRITICAL: Ignore messages sent BY the bot itself
	if evt.FromMe {
		return
	}

	// CRITICAL: Only process messages with quotes (replies to session notifications)
	if evt.QuotedMsgBody == "" {
		return // Not a reply, ignore silently
	}

	log.Printf("🌉 Bridge: Incoming quoted reply from %s. Body: '%s'", evt.From, truncate(evt.Body, 50))
	log.Printf("🌉 Bridge: Quote content: '%s'", truncate(evt.QuotedMsgBody, 200))

	// 1. Extract Session ID from Quoted Message
	// Format: ... #<session_id> somewhere in the message
	sessionID := extractSessionID(evt.QuotedMsgBody)
	
	if sessionID == "" {
		log.Printf("🌉 Bridge: No valid session ID found in quote. Ignoring.")
		return
	}

	log.Printf("🌉 Bridge: Extracted session ID: %s", sessionID)

	// 2. Verify Session Exists (this IS the authorization - if session exists and quote matches, sender is valid)
	session, err := h.sessionManager.GetSession(context.Background(), sessionID)
	if err != nil || session == nil {
		log.Printf("🌉 Bridge: Session %s not found or error: %v. Ignoring reply.", sessionID, err)
		return
	}

	log.Printf("🌉 Bridge: Valid session found! Processing reply from %s for session %s", evt.From, sessionID)

	// 3. Handle Commands
	cmd := strings.ToLower(strings.TrimSpace(evt.Body))
	switch cmd {
	case "/hi", "/halo", "/sambutan":
		name := session.VisitorName
		if name == "" {
			name = "Kak"
		}
		evt.Body = fmt.Sprintf("Halo %s! 👋 Perkenalkan saya Admin dari Valpro. Ada yang bisa saya bantu terkait layanan kami?", name)
	case "/thanks", "/trims", "/tq":
		evt.Body = "Terima kasih telah menghubungi Valpro. Jangan ragu untuk menghubungi kami kembali jika ada pertanyaan lain! 🙏"
	case "/end", "/close", "/selesai":
		if err := h.sessionManager.CloseSession(context.Background(), sessionID); err != nil {
			log.Printf("Failed to close session via WA: %v", err)
			return
		}
		h.hub.BroadcastToSession(sessionID, "session-closed", gin.H{})
		h.hub.BroadcastToAdmins("queue-update", gin.H{
			"type":      "closed",
			"sessionId": sessionID,
		})
		return

	case "/ai", "/bot", "/kembali":
		if err := h.sessionManager.ReturnToBot(context.Background(), sessionID); err != nil {
			log.Printf("Failed to return to bot via WA: %v", err)
			return
		}
		h.hub.BroadcastToSession(sessionID, "status-change", gin.H{"status": "bot"})
		h.hub.BroadcastToAdmins("queue-update", gin.H{
			"type":      "bot_handoff",
			"sessionId": sessionID,
		})
		return
	}

	// 4. Create and Save Message
	msg := &ChatMessage{
		SessionID: sessionID,
		Sender:    "admin",
		Content:   evt.Body,
		Timestamp: time.Unix(evt.Timestamp, 0),
	}

	if err := h.sessionManager.SaveMessage(context.Background(), msg); err != nil {
		log.Printf("🌉 Bridge Error: Failed to save message: %v", err)
	}

	// 5. Broadcast to Visitor
	h.hub.BroadcastToSession(sessionID, "chat-message", gin.H{
		"sessionId": sessionID,
		"content":   msg.Content,
		"sender":    "admin",
		"timestamp": msg.Timestamp,
	})

	// 6. Broadcast to Dashboard
	h.hub.BroadcastToAdmins("chat-message", gin.H{
		"id":        msg.ID,
		"sessionId": sessionID,
		"content":   msg.Content,
		"sender":    "admin",
		"timestamp": msg.Timestamp,
		"via":       "whatsapp",
	})

	// 7. Auto-claim if session is queued
	if session.Status == StatusQueued {
		if err := h.sessionManager.ClaimSession(context.Background(), sessionID, "admin_wa", "WhatsApp Admin"); err != nil {
			log.Printf("Failed to auto-claim session: %v", err)
		} else {
			h.hub.BroadcastToSession(sessionID, "admin-joined", gin.H{
				"adminName": "WhatsApp Admin",
			})
			h.hub.BroadcastToAdmins("queue-update", gin.H{
				"type":      "claimed",
				"sessionId": sessionID,
				"adminId":   "admin_wa",
			})
		}
	}

	log.Printf("🌉 Bridge: Successfully forwarded message to session %s", sessionID)
}

// extractSessionID extracts a session ID from a quoted message body
// Looks for pattern: #<session_id> or 🆔 #<session_id>
func extractSessionID(quotedBody string) string {
	lines := strings.Split(quotedBody, "\n")
	
	// Search backwards from the end (ID is usually at the bottom)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "#") {
			parts := strings.Split(line, "#")
			if len(parts) >= 2 {
				potentialID := strings.TrimSpace(parts[len(parts)-1])
				// Clean up trailing chars
				potentialID = strings.TrimRight(potentialID, ")*_ ")
				
				// Validate: Session IDs are typically 20 alphanumeric chars
				if len(potentialID) >= 15 && len(potentialID) <= 30 {
					return potentialID
				}
			}
		}
	}
	
	return ""
}

// getAgentPhone fetches the agent phone from SettingsRepo, falling back to Config
func (h *Handler) getAgentPhone() string {
	if h.settingsRepo == nil {
		log.Printf("⚠️ getAgentPhone: settingsRepo is nil, using config: %s", h.config.AgentPhone)
		return h.config.AgentPhone
	}
	
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	settings, err := h.settingsRepo.GetWhatsAppSettings(ctx)
	if err != nil || settings == nil || settings.AgentPhone == "" {
		// Fallback to static config
		log.Printf("⚠️ getAgentPhone: Firestore settings missing/empty (err: %v), using config: %s", err, h.config.AgentPhone)
		return h.config.AgentPhone
	}
	
	log.Printf("📱 getAgentPhone: Using dynamic settings: %s", settings.AgentPhone)
	return settings.AgentPhone
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// autoReplyCache tracks the last time an auto-reply was sent to a specific phone number to prevent spam (value: time.Time)
var autoReplyCache sync.Map

// HandleBotAutoResponder sends an automatic reply when a client messages the WhatsApp bot
func (h *Handler) HandleBotAutoResponder(evt whatsapp.NewMessageEvent) {
	// 1. Ignore if message is sent by the bot itself
	if evt.FromMe {
		return
	}

	// 2. Ignore if message is from an authorized admin (we don't want to auto-reply to our own admins)
	if h.isAuthorizedAdmin(evt.From) {
		return
	}

	// 3. Ignore if this is a quote reply for the Web Chat bridge
	// (HandleAgentReply will take care of Web Chat bridge messages if any)
	if evt.QuotedMsgBody != "" {
		sessionID := extractSessionID(evt.QuotedMsgBody)
		if sessionID != "" {
			return // This is part of a web chat bridge conversation, do not send invoice auto-reply
		}
	}

	// 4. Rate Limiting: Check if we've sent an auto-reply to this number recently (e.g., within 30 minutes)
	lastReplyVal, exists := autoReplyCache.Load(evt.From)
	if exists {
		lastReplyTime, ok := lastReplyVal.(time.Time)
		if ok && time.Since(lastReplyTime) < 30*time.Minute {
			log.Printf("🤖 AutoResponder: Skipping auto-reply for %s (cooldown active)", evt.From)
			return // Still in cooldown
		}
	}

	// Update cache
	autoReplyCache.Store(evt.From, time.Now())

	// 5. Build the professional auto-reply template
	replyMessage := `Halo! 👋 Terima kasih telah menghubungi Valpro Intertech.
Pesan Anda telah diterima secara otomatis oleh sistem kami.

✅ *Konfirmasi Pembayaran*
Apabila Anda melampirkan bukti transfer pembayaran Invoice, mohon pastikan *Nomor Invoice* terlihat atau disebutkan. Tim Finance kami akan segera memverifikasi dan memperbarui status tagihan Anda pada jam kerja.

💬 *Informasi & Bantuan CS*
Jika Anda membutuhkan bantuan Admin, pertanyaan teknis, atau layanan lainnya, silakan hubungi Customer Service kami di saluran berikut:
📞 WhatsApp/Telp: +62 813-9971-0085
📧 Email: mail@valprointertech.com
🌐 Website: valprointertech.com

_Mohon diperhatikan bahwa nomor ini digunakan oleh sistem robot untuk pengiriman notifikasi otomatis tagihan, sehingga balasan manual mungkin memerlukan waktu lebih lama._

Terima kasih atas kepercayaan Anda kepada Valpro Intertech! ✨`

	// 6. Send the message back via the exact client that received it
	if h.waManager != nil {
		client, ok := h.waManager.GetClient(evt.Client)
		if ok && client != nil && client.WAClient != nil {
			log.Printf("🤖 AutoResponder: Sending acknowledgment to %s", evt.From)
			go func() {
				// evt.ChatID contains the target JID address
				err := client.SendTextMessage(context.Background(), evt.ChatID, replyMessage)
				if err != nil {
					log.Printf("❌ AutoResponder Error: %v", err)
				}
			}()
		}
	}
}
