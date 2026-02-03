package handlers

import (
	"fmt"
	"net/http"
	"os"
	"wa-server-go/internal/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// LoginRequest represents login form
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// PortalLogin handles POST /portal/login
func (h *Handler) PortalLogin(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	// Find client by email
	client, err := h.BusinessClientRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	if client == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
		return
	}

	// Verify Password
	// Assuming PasswordHash is stored in DB. If empty (legacy/new), might need handling.
	if client.PasswordHash == "" {
		// For now, if no password set, maybe allow or deny? 
		// Security wise: deny. But for dev/testing of new feature:
		// Let's assume we set a default password or we need a SetPassword endpoint.
		// For MVP, if hash is empty, we can't login.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Account not fully set up (no password)"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(client.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid email or password"})
		return
	}

	// Generate JWT
	secret := h.Config.JWTSecret
	token, err := utils.GenerateToken(client.ID, "client", secret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"token":   token,
		"client": gin.H{
			"id":    client.ID,
			"name":  client.Name,
			"email": client.Email,
		},
	})
}

// GetPortalDashboard handles GET /portal/dashboard
func (h *Handler) GetPortalDashboard(c *gin.Context) {
	clientID := c.GetString("clientID")
	if clientID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	ctx := c.Request.Context()

	// Fetch documents
	docs, err := h.ClientDocsRepo.GetByClientID(ctx, clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch documents"})
		return
	}

	// Fetch client details (optional)
	client, _ := h.BusinessClientRepo.GetByID(ctx, clientID)

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"client":    client,
		"documents": docs,
	})
}

// DownloadDocument handles GET /portal/documents/:documentId/download
func (h *Handler) DownloadDocument(c *gin.Context) {
	// 1. Get User Context (from JWT)
	clientID := c.GetString("clientID")
	if clientID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	documentID := c.Param("documentId")
	if documentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Document ID required"})
		return
	}

	ctx := c.Request.Context()

	// 2. Fetch Document Metadata
	doc, err := h.ClientDocsRepo.GetByID(ctx, documentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch document"})
		return
	}
	if doc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Document not found"})
		return
	}

	// 3. Verify Ownership (IDOR Protection)
	if doc.ClientID != clientID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Access denied"})
		return
	}

	// 4. Check File Existence
	if _, err := os.Stat(doc.StoragePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found on disk"})
		return
	}

	// 5. Serve File
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", doc.FileName))
	c.Header("Content-Type", doc.FileType)
	c.File(doc.StoragePath)
}
