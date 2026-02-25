package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"wa-server-go/internal/api/handlers"
	"wa-server-go/internal/api/middleware"
	"wa-server-go/internal/api/websocket"
	"wa-server-go/internal/chatbot"
	"wa-server-go/internal/config"
	"wa-server-go/internal/firestore"
	"wa-server-go/internal/whatsapp"

	"github.com/gin-gonic/gin"
)

// Server represents the HTTP server
type Server struct {
	Config      *config.Config
	Router      *gin.Engine
	WSHub       *websocket.Hub
	WAManager   *whatsapp.Manager
	Handler     *handlers.Handler
	Repo        *firestore.ChatsRepository
	ChatHub     *chatbot.ChatHub
	ChatHandler *chatbot.Handler
}

// NewServer creates a new HTTP server
func NewServer(cfg *config.Config, waManager *whatsapp.Manager, repo *firestore.ChatsRepository) *Server {
	// Set Gin mode
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	// Create WebSocket hub
	wsHub := websocket.NewHub()

	// Create business client repositories
	clientRepo := firestore.NewBusinessClientRepository(repo.GetClient())
	docRepo := firestore.NewClientDocumentRepository(repo.GetClient())
	leadsRepo := firestore.NewLeadsRepository(repo.GetClient())
	settingsRepo := firestore.NewSettingsRepository(repo.GetClient())

	// Create handlers
	handler := handlers.NewHandler(cfg, waManager, repo, clientRepo, docRepo, settingsRepo, wsHub)

	// Initialize Chatbot components
	chatEngine := chatbot.NewChatEngine(cfg.GroqAPIKey)
	sessionManager := chatbot.NewSessionManager(repo.GetClient().FS, chatEngine)
	chatHub := chatbot.NewChatHub()
	chatHandler := chatbot.NewHandler(sessionManager, chatHub, leadsRepo, settingsRepo, waManager, cfg)

	server := &Server{
		Config:      cfg,
		Router:      router,
		WSHub:       wsHub,
		WAManager:   waManager,
		Handler:     handler,
		Repo:        repo,
		ChatHub:     chatHub,
		ChatHandler: chatHandler,
	}


	// Serve static files (uploads)
	router.Static("/uploads", "./uploads")
	router.Static("/storage", "./storage") // For new client documents

	server.setupRoutes()

	return server
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Apply global middleware
	s.Router.Use(middleware.CORSMiddleware(s.Config.AllowedDomains))
	s.Router.Use(middleware.SecurityMiddleware(s.Config.APIKey, s.Config.AllowedDomains))

	// Health check endpoints (no auth required)
	s.Router.GET("/", s.Handler.HealthCheck)
	s.Router.GET("/status", s.Handler.GetStatus)

	// WebSocket endpoint
	s.Router.GET("/ws", s.WSHub.HandleWebSocket)

	// Sync endpoints
	s.Router.GET("/sync-status", s.Handler.GetSyncStatus)

	// Protected endpoints (require API key per route)
	protected := s.Router.Group("")
	protected.Use(middleware.APIKeyRequired(s.Config.APIKey))
	{
		// Sending endpoints
		protected.POST("/send-invoice", s.Handler.SendInvoice)
		protected.POST("/send-message", s.Handler.SendMessage)
		protected.POST("/send-media", s.Handler.SendMedia)

		// Chat endpoints
		protected.GET("/get-chats", s.Handler.GetChats)
		protected.GET("/get-messages/:chatId", s.Handler.GetMessages)
		protected.GET("/get-media/:messageId", s.Handler.GetMedia)
		protected.GET("/get-invoice-chats", s.Handler.GetInvoiceChats)

		// Sync endpoints
		protected.POST("/sync-contacts", s.Handler.SyncContacts)
		protected.GET("/sync-contacts-stream", s.Handler.SyncContactsStream) // SSE streaming
		protected.POST("/start-leads-client", s.Handler.StartLeadsClient)
		protected.POST("/stop-leads-client", s.Handler.StopLeadsClient)

		// Feature endpoints
		protected.POST("/trigger-backup", s.Handler.TriggerBackup)
		protected.POST("/api/blog/manual-trigger", s.Handler.TriggerBlog)
		protected.POST("/sync-invoices", s.Handler.SyncInvoices)
		
		// Settings Endpoints
		protected.GET("/settings/whatsapp", s.Handler.GetWhatsAppSettings)
		protected.POST("/settings/whatsapp", s.Handler.UpdateWhatsAppSettings)

		// Business Client Endpoints
		protected.POST("/clients", s.Handler.CreateClient)
		protected.PUT("/clients/:clientId", s.Handler.UpdateClient)
		protected.GET("/clients/:clientId", s.Handler.GetClient)

		// Document Endpoints (Admin - Restored for Frontend Compatibility)
		protected.POST("/documents/upload", s.Handler.UploadDocument)
		protected.GET("/documents/client/:clientId", s.Handler.GetClientDocuments)
		protected.DELETE("/documents/:documentId", s.Handler.DeleteDocument)


		// Portal Endpoints (Client Access)
		// These might not need API Key if they use JWT, or both?
		// Requirement says "Port is strictly for Clients".
		// Let's protect with AuthMiddleware (JWT)
		// We should probably create a separate group without API Key if it's public login,
		// but likely we want some protection.
		// Login endpoint should be public (or API key protected if app-to-app).
		// Assuming public access for login page functionality:
	}

	// Public Portal Routes (or at least no API key if meant for browser directly, but typically API key is for server-to-server)
	// If it's a web portal, it likely calls API.
	// Let's keep them outside the main protected group if API Key is not meant for them.
	// Or create a portal group.
	portal := s.Router.Group("/portal")
	portal.Use(middleware.CORSMiddleware(s.Config.AllowedDomains)) // Ensure CORS
	{
		portal.POST("/login", s.Handler.PortalLogin)
		
		// Protected Portal
		portalProtected := portal.Group("")
		portalProtected.Use(middleware.AuthMiddleware(s.Config.JWTSecret))
		{
			portalProtected.GET("/dashboard", s.Handler.GetPortalDashboard)
			// Add document download for portal users here if different from admin
			portalProtected.GET("/documents/:documentId/download", s.Handler.DownloadDocument) 
		}
	}

	// ============ PUBLIC CHAT WIDGET ROUTES ============
	// These are public endpoints for the website chat widget
	chat := s.Router.Group("/chat")
	chat.Use(middleware.CORSMiddleware(s.Config.AllowedDomains))
	{
		chat.POST("/start", s.ChatHandler.StartChat)
		chat.POST("/message", s.ChatHandler.SendMessage)
		chat.POST("/handover", s.ChatHandler.RequestHandover)
		chat.POST("/return-to-bot", s.ChatHandler.VisitorReturnToBot)
		chat.POST("/end-session", s.ChatHandler.VisitorEndSession)
		chat.GET("/history/:sessionId", s.ChatHandler.GetMessages)
		chat.GET("/ws/:sessionId", s.ChatHub.HandleVisitorWS)
	}

	// ============ ADMIN LIVE CHAT ROUTES ============
	// These require API key authentication
	adminChat := protected.Group("/admin-chat")
	{
		adminChat.GET("/queue", s.ChatHandler.GetQueue)
		adminChat.POST("/claim/:sessionId", s.ChatHandler.ClaimChat)
		adminChat.POST("/close/:sessionId", s.ChatHandler.CloseChat)
		adminChat.POST("/return-to-bot/:sessionId", s.ChatHandler.ReturnToBot)
		adminChat.POST("/message", s.ChatHandler.AdminSendMessage)
		adminChat.GET("/ws", s.ChatHub.HandleAdminWS)
	}

	// Admin Status endpoints
	adminStatus := protected.Group("/admin-status")
	{
		adminStatus.POST("/update", s.ChatHandler.UpdateAdminStatus)
		adminStatus.GET("/online", s.ChatHandler.GetOnlineAdmins)
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	// Start WebSocket hub
	go s.WSHub.Run()

	// Start Chat WebSocket hub
	go s.ChatHub.Run()

	// Start event forwarders
	go s.forwardEvents()

	// Start cleanup job (runs every minute, cleans sessions inactive for 6 mins)
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		for range ticker.C {
			if err := s.ChatHandler.CleanupSessions(context.Background(), 6*time.Minute); err != nil {
				log.Printf("⚠️ Failed to cleanup sessions: %v", err)
			}
		}
	}()

	addr := fmt.Sprintf(":%s", s.Config.Port)
	log.Printf("✅ WhatsApp Server listening on port %s", s.Config.Port)
	log.Printf("💬 Chat widget endpoints available at /chat/*")
	return s.Router.Run(addr)
}

// forwardEvents forwards WhatsApp events to WebSocket clients
func (s *Server) forwardEvents() {
	for {
		select {
		case qr := <-s.WAManager.QRChannel():
			s.WSHub.Broadcast("qr-image", qr)

		case status := <-s.WAManager.StatusChannel():
			s.WSHub.Broadcast("status-update", status)

		case msg := <-s.WAManager.MessageChannel():
			// BRIDGE: Check for Agent Reply to Web Visitor
			go s.ChatHandler.HandleAgentReply(msg)

			// AUTO-REPLY: Handle client replies to invoices on WA Bot
			go s.ChatHandler.HandleBotAutoResponder(msg)
			
			// Broadcast to Dashboard
			s.WSHub.Broadcast("new-message", msg)
		}
	}
}
