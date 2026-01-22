package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// TriggerBackup handles POST /trigger-backup
func (h *Handler) TriggerBackup(c *gin.Context) {
	botClient, ok := h.WAManager.GetClient("bot")
	if !ok || !botClient.IsReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "Bot not ready",
		})
		return
	}

	// TODO: Implement async backup trigger
	// - Fetch data from WEB_URL/api/backup/data-dump
	// - Generate Excel file using excelize
	// - Send via WhatsApp to BACKUP_PHONE

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Backup triggered (implementation pending)",
	})
}

// TriggerBlog handles POST /api/blog/manual-trigger
func (h *Handler) TriggerBlog(c *gin.Context) {
	// TODO: Implement blog automation trigger
	// - Generate content using Groq API
	// - Fetch images from Pexels
	// - Publish to Contentful

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Blog generation triggered (implementation pending)",
	})
}

// SyncInvoices handles POST /sync-invoices
func (h *Handler) SyncInvoices(c *gin.Context) {
	if h.Repo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "Chat storage (Firestore) is not configured",
		})
		return
	}

	// This handler is now "SyncChatMetadata" but we keep the endpoint /sync-invoices for compatibility
	// or we can rename the handler. Let's redirect to ScanChatMetadata.
	count, err := h.Repo.ScanChatMetadata(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to scan chats",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Invoice chats synced successfully",
		"updated": count,
	})
}
