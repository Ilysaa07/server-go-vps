package handlers

import (
	"net/http"
	"time"

	"wa-server-go/internal/api/websocket"
	"wa-server-go/internal/firestore"
	"wa-server-go/internal/whatsapp"

	"github.com/gin-gonic/gin"
)

// Handler holds dependencies for HTTP handlers
type Handler struct {
	WAManager *whatsapp.Manager
	Repo      *firestore.ChatsRepository
	WSHub     *websocket.Hub
}

// NewHandler creates a new handler with dependencies
func NewHandler(waManager *whatsapp.Manager, repo *firestore.ChatsRepository, wsHub *websocket.Hub) *Handler {
	return &Handler{
		WAManager: waManager,
		Repo:      repo,
		WSHub:     wsHub,
	}
}

// HealthCheck handles GET /
func (h *Handler) HealthCheck(c *gin.Context) {
	c.String(http.StatusOK, "WhatsApp Server is Running! 🚀<br/>Bot: Always On | Leads: On-Demand (RAM Optimized)")
}

// GetStatus handles GET /status
func (h *Handler) GetStatus(c *gin.Context) {
	botClient, botExists := h.WAManager.GetClient("bot")
	leadsClient, leadsExists := h.WAManager.GetClient("leads")

	botStatus := map[string]interface{}{
		"ready":   false,
		"purpose": "Invoice & Broadcast",
		"mode":    "always-on",
	}
	if botExists {
		botStatus["ready"] = botClient.IsReady()
		botStatus["qr"] = botClient.GetQRCode()
	}

	leadsStatus := map[string]interface{}{
		"ready":        false,
		"initializing": false,
		"purpose":      "Contact Sync (Read-Only)",
		"mode":         "on-demand",
		"note":         "Starts when sync is triggered, auto-shuts down after 30s",
	}
	if leadsExists {
		leadsStatus["ready"] = leadsClient.IsReady()
		leadsStatus["qr"] = leadsClient.GetQRCode()
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "running",
		"mode":   "low-ram-optimized",
		"sessions": gin.H{
			"bot":   botStatus,
			"leads": leadsStatus,
		},
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// GetSyncStatus handles GET /sync-status
func (h *Handler) GetSyncStatus(c *gin.Context) {
	leadsClient, leadsExists := h.WAManager.GetClient("leads")

	response := gin.H{
		"clientLeadsReady":        false,
		"clientLeadsInitializing": false,
		"mode":                    "on-demand",
		"cooldownActive":          false,
		"remainingCooldownSeconds": 0,
		"lastSyncTime":            nil,
		"nextSyncAvailable":       "Now",
		"qr":                      nil,
	}

	if leadsExists {
		response["clientLeadsReady"] = leadsClient.IsReady()
		response["qr"] = leadsClient.GetQRCode()
	}

	c.JSON(http.StatusOK, response)
}
