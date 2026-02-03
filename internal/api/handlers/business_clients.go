package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"wa-server-go/internal/firestore"

	"github.com/gin-gonic/gin"
)

// AllowedClientFileTypes maps MIME types to extensions
var AllowedClientFileTypes = map[string]string{
	"application/pdf": ".pdf",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": ".xlsx",
}

// CreateClientRequest matches the form data fields
type CreateClientRequest struct {
	Name    string `form:"name" binding:"required"`
	Email   string `form:"email" binding:"required,email"`
	Phone   string `form:"phone" binding:"required"`
	Address string `form:"address"`
}

// CreateClient handles POST /clients
func (h *Handler) CreateClient(c *gin.Context) {
	var req CreateClientRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	// Check if email already exists
	existing, err := h.BusinessClientRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check email availability"})
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Email already registered"})
		return
	}

	// Create Client Record
	client := &firestore.BusinessClient{
		Name:    req.Name,
		Email:   req.Email,
		Phone:   req.Phone,
		Address: req.Address,
		// PasswordHash: ... (TODO: Add password handling for portal)
	}

	clientID, err := h.BusinessClientRepo.Create(ctx, client)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create client"})
		return
	}
	client.ID = clientID

	// Handle File Upload (Optional)
	file, header, err := c.Request.FormFile("document")
	if err == nil {
		defer file.Close()
		// Process file upload
		docErr := h.processDocumentUpload(ctx, clientID, file, header)
		if docErr != nil {
			// Log error but don't fail the client creation entirely, or warn user
			fmt.Printf("⚠️ Failed to upload initial document for client %s: %v\n", clientID, docErr)
			c.JSON(http.StatusCreated, gin.H{
				"success": true,
				"message": "Client created but document upload failed",
				"error":   docErr.Error(),
				"client":  client,
			})
			return
		}
	} else if err != http.ErrMissingFile {
		// Real error occurred
		c.JSON(http.StatusBadRequest, gin.H{"error": "File upload error: " + err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "Client created successfully",
		"client":  client,
	})
}

// UpdateClient handles PUT /clients/:clientId
func (h *Handler) UpdateClient(c *gin.Context) {
	clientID := c.Param("clientId")
	if clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Client ID required"})
		return
	}

	// Manual binding because ShouldBind might complain about partial updates or multipart differences
	updates := make(map[string]interface{})
	
	if name := c.PostForm("name"); name != "" {
		updates["name"] = name
	}
	if email := c.PostForm("email"); email != "" {
		updates["email"] = email
	}
	if phone := c.PostForm("phone"); phone != "" {
		updates["phone"] = phone
	}
	if address := c.PostForm("address"); address != "" {
		updates["address"] = address
	}

	ctx := c.Request.Context()

	if len(updates) > 0 {
		if err := h.BusinessClientRepo.Update(ctx, clientID, updates); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update client"})
			return
		}
	}

	// Handle File Upload (Optional) - Adding a NEW document
	file, header, err := c.Request.FormFile("document")
	if err == nil {
		defer file.Close()
		if err := h.processDocumentUpload(ctx, clientID, file, header); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upload document: " + err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Client updated successfully",
	})
}

// GetClient handles GET /clients/:clientId
func (h *Handler) GetClient(c *gin.Context) {
	clientID := c.Param("clientId")
	client, err := h.BusinessClientRepo.GetByID(c.Request.Context(), clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch client"})
		return
	}
	if client == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}
	c.JSON(http.StatusOK, client)
}

// Helper to process document upload
func (h *Handler) processDocumentUpload(ctx context.Context, clientID string, file io.Reader, header *multipart.FileHeader) error {
	// Size limit 5MB
	const MaxSize = 5 * 1024 * 1024
	if header.Size > MaxSize {
		return fmt.Errorf("file size exceeds 5MB limit")
	}

	// Type Check
	contentType := header.Header.Get("Content-Type")
	if _, ok := AllowedClientFileTypes[contentType]; !ok {
		return fmt.Errorf("unsupported file type: %s", contentType)
	}

	// Prepare path
	uploadDir := filepath.Join("storage", "clients", clientID, "documents")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	// Sanitize & Unique Name
	safeName := sanitizeFilename(header.Filename)
	uniqueName := fmt.Sprintf("%d_%s", time.Now().Unix(), safeName)
	savePath := filepath.Join(uploadDir, uniqueName)

	// Save to disk
	dst, err := os.Create(savePath)
	if err != nil {
		return fmt.Errorf("failed to create file on disk: %v", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		os.Remove(savePath)
		return fmt.Errorf("failed to save content: %v", err)
	}

	// Save Metadata
	doc := &firestore.ClientDocument{
		ClientID:    clientID,
		FileName:    safeName,
		EncodedName: uniqueName,
		FileType:    contentType,
		FileSize:    header.Size,
		StoragePath: savePath,
		FileURL:     filepath.ToSlash(savePath), // Windows path handling
		UploadedBy:  "admin", // TODO: Auth context
	}

	return h.ClientDocsRepo.Create(ctx, doc)
}

// Reusing sanitizeFilename from previous Documents logic
func sanitizeFilename(filename string) string {
	filename = filepath.Base(filename)
	filename = strings.ReplaceAll(filename, " ", "_")
	reg := regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	filename = reg.ReplaceAllString(filename, "")
	if filename == "" {
		return "unnamed_file"
	}
	return filename
}

// UploadDocument handles POST /documents/upload (Standalone)
func (h *Handler) UploadDocument(c *gin.Context) {
	clientID := c.PostForm("clientId")
	if clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Client ID required"})
		return
	}

	file, header, err := c.Request.FormFile("file") // Frontend sends 'file'
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File required: " + err.Error()})
		return
	}
	defer file.Close()

	// Optional metadata
	description := c.PostForm("description")
	category := c.PostForm("category")

	ctx := c.Request.Context()

	// Verify Client Exists
	log.Printf("UploadDocument: Verifying client %s", clientID)
	client, err := h.BusinessClientRepo.GetByID(ctx, clientID)
	if err != nil {
		log.Printf("UploadDocument: Error getting client %s: %v", clientID, err)
	}
	if client == nil {
		log.Printf("UploadDocument: Client %s return nil", clientID)
	}
	if err != nil || client == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Client not found"})
		return
	}

	// Process Upload
	// Note: processDocumentUpload saves file and creates metadata but doesn't set Description/Category
	// We need to update processDocumentUpload or handle it here?
	// processDocumentUpload creates the struct. We should modify it to accept metadata or update after?
	// Let's modify processDocumentUpload signature or update metadata logic.
	// For now, let's reuse processDocumentUpload and then update the doc if needed?
	// Actually processDocumentUpload creates file on disk and inserts to DB.
	// It's better to update processDocumentUpload to accept metadata struct?
	
	// Quickest fix: Copy-paste valid parts of processDocumentUpload or modify it. 
	// I'll inline the logic here for flexibility since I need to set Description/Category.
	
	// ... Logic ...
	
	// 1. Size/Type Check (Reuse helper logic or copy)
	const MaxSize = 10 * 1024 * 1024 // Frontend says 10MB
	if header.Size > MaxSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File too large (>10MB)"})
		return
	}
	
	contentType := header.Header.Get("Content-Type")
	log.Printf("UploadDocument: Received file '%s' with Content-Type: '%s'", header.Filename, contentType)

	// Using the map from earlier
	if _, ok := AllowedClientFileTypes[contentType]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Unsupported file type: %s", contentType)})
			return
	}

	uploadDir := filepath.Join("storage", "clients", clientID, "documents")
	_ = os.MkdirAll(uploadDir, 0755)
	
	safeName := sanitizeFilename(header.Filename)
	uniqueName := fmt.Sprintf("%d_%s", time.Now().Unix(), safeName)
	savePath := filepath.Join(uploadDir, uniqueName)

	dst, err := os.Create(savePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Save failed"})
		return
	}
	defer dst.Close()
	_, _ = io.Copy(dst, file)

	doc := &firestore.ClientDocument{
		ClientID:    clientID,
		FileName:    safeName,
		EncodedName: uniqueName,
		FileType:    contentType,
		FileSize:    header.Size,
		StoragePath: savePath,
		FileURL:     filepath.ToSlash(savePath),
		UploadedBy:  "admin",
		Description: description,
		Category:    category,
	}

	if err := h.ClientDocsRepo.Create(ctx, doc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "DB Error: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": doc})
}

// GetClientDocuments handles GET /documents/client/:clientId
func (h *Handler) GetClientDocuments(c *gin.Context) {
	clientID := c.Param("clientId")
	docs, err := h.ClientDocsRepo.GetByClientID(c.Request.Context(), clientID)
	if err != nil {
		log.Printf("Error fetching documents for client %s: %v", clientID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch documents"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "documents": docs})
}

// DeleteDocument handles DELETE /documents/:documentId
func (h *Handler) DeleteDocument(c *gin.Context) {
	docID := c.Param("documentId")
	ctx := c.Request.Context()

	// Get doc to find path
	doc, err := h.ClientDocsRepo.GetByID(ctx, docID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Fetch error"})
		return
	}
	if doc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Document not found"})
		return
	}

	// Delete from disk
	_ = os.Remove(doc.StoragePath)

	// Delete from DB
	if err := h.ClientDocsRepo.Delete(ctx, docID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Delete failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
