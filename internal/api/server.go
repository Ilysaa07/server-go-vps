package api

import (
	"fmt"
	"log"

	"wa-server-go/internal/api/handlers"
	"wa-server-go/internal/api/middleware"
	"wa-server-go/internal/api/websocket"
	"wa-server-go/internal/config"
	"wa-server-go/internal/firestore"
	"wa-server-go/internal/whatsapp"

	"github.com/gin-gonic/gin"
)

// Server represents the HTTP server
type Server struct {
	Config    *config.Config
	Router    *gin.Engine
	WSHub     *websocket.Hub
	WAManager *whatsapp.Manager
	Handler   *handlers.Handler
	Repo      *firestore.ChatsRepository
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

	// Create handlers
	handler := handlers.NewHandler(waManager, repo)

	server := &Server{
		Config:    cfg,
		Router:    router,
		WSHub:     wsHub,
		WAManager: waManager,
		Handler:   handler,
		Repo:      repo,
	}

	// Serve static files (uploads)
	router.Static("/uploads", "./uploads")

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
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	// Start WebSocket hub
	go s.WSHub.Run()

	// Start event forwarders
	go s.forwardEvents()

	addr := fmt.Sprintf(":%s", s.Config.Port)
	log.Printf("✅ WhatsApp Server listening on port %s", s.Config.Port)
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
			s.WSHub.Broadcast("new-message", msg)
		}
	}
}
