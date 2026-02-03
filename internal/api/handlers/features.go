package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

// DataDumpResponse represents the response from WEB_URL/api/backup/data-dump
type DataDumpResponse struct {
	Timestamp string     `json:"timestamp"`
	Version   string     `json:"version"`
	Summary   map[string]int `json:"summary"`
	Data      BackupData `json:"data"`
}

// BackupData contains the actual data collections
type BackupData struct {
	Clients  []map[string]interface{} `json:"clients"`
	Invoices []map[string]interface{} `json:"invoices"`
	Services []map[string]interface{} `json:"services"`
	Users    []map[string]interface{} `json:"users"`
}

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

	// Get environment variables
	webURL := os.Getenv("WEB_URL")
	backupPhone := os.Getenv("BACKUP_PHONE")
	apiKey := os.Getenv("API_KEY")

	if webURL == "" || backupPhone == "" {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Missing WEB_URL or BACKUP_PHONE configuration",
		})
		return
	}

	// Respond immediately and process async
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Backup triggered, processing in background",
	})

	// Process backup asynchronously
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		// Fetch data from WEB_URL
		dataDumpURL := webURL + "/api/backup/data-dump"
		req, err := http.NewRequestWithContext(ctx, "GET", dataDumpURL, nil)
		if err != nil {
			fmt.Printf("[Backup] Failed to create request: %v\n", err)
			return
		}
		req.Header.Set("x-api-key", apiKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Printf("[Backup] Failed to fetch data dump: %v\n", err)
			// Send error notification
			botClient.SendTextMessage(ctx, backupPhone+"@s.whatsapp.net", 
				"❌ *BACKUP FAILED*\n\nGagal mengambil data dari server:\n"+err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fmt.Printf("[Backup] Data dump returned status %d: %s\n", resp.StatusCode, string(body))
			botClient.SendTextMessage(ctx, backupPhone+"@s.whatsapp.net", 
				fmt.Sprintf("❌ *BACKUP FAILED*\n\nServer returned status %d", resp.StatusCode))
			return
		}

		var dumpResponse DataDumpResponse
		if err := json.NewDecoder(resp.Body).Decode(&dumpResponse); err != nil {
			fmt.Printf("[Backup] Failed to decode response: %v\n", err)
			botClient.SendTextMessage(ctx, backupPhone+"@s.whatsapp.net", 
				"❌ *BACKUP FAILED*\n\nGagal decode data: "+err.Error())
			return
		}
		backupData := dumpResponse.Data

		// Generate Excel file
		excelData, err := generateBackupExcel(backupData)
		if err != nil {
			fmt.Printf("[Backup] Failed to generate Excel: %v\n", err)
			botClient.SendTextMessage(ctx, backupPhone+"@s.whatsapp.net", 
				"❌ *BACKUP FAILED*\n\nGagal generate Excel: "+err.Error())
			return
		}

		// Send the Excel file via WhatsApp
		timestamp := time.Now().Format("2006-01-02_15-04")
		filename := fmt.Sprintf("backup_valpro_%s.xlsx", timestamp)
		caption := fmt.Sprintf("✅ *BACKUP DATA VAULT*\n\n📅 %s\n📊 %d Clients\n📑 %d Invoices\n🔧 %d Services",
			time.Now().Format("02 Jan 2006 15:04 WIB"),
			len(backupData.Clients),
			len(backupData.Invoices),
			len(backupData.Services))

		err = botClient.SendDocument(ctx, backupPhone+"@s.whatsapp.net", excelData, filename, 
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", caption)
		if err != nil {
			fmt.Printf("[Backup] Failed to send document: %v\n", err)
			botClient.SendTextMessage(ctx, backupPhone+"@s.whatsapp.net", 
				"❌ *BACKUP FAILED*\n\nGagal mengirim file: "+err.Error())
			return
		}

		fmt.Printf("[Backup] Successfully sent backup to %s\n", backupPhone)
	}()
}

// generateBackupExcel creates an Excel file from backup data
func generateBackupExcel(data BackupData) ([]byte, error) {
	f := excelize.NewFile()

	// Create Clients sheet
	clientSheet := "Clients"
	f.SetSheetName("Sheet1", clientSheet)
	
	// Headers for clients
	clientHeaders := []string{"ID", "Name", "Email", "Phone", "Address", "Created At"}
	for i, h := range clientHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(clientSheet, cell, h)
	}
	
	// Data for clients
	for i, client := range data.Clients {
		row := i + 2
		f.SetCellValue(clientSheet, fmt.Sprintf("A%d", row), getStringValue(client, "id"))
		f.SetCellValue(clientSheet, fmt.Sprintf("B%d", row), getStringValue(client, "name"))
		f.SetCellValue(clientSheet, fmt.Sprintf("C%d", row), getStringValue(client, "email"))
		f.SetCellValue(clientSheet, fmt.Sprintf("D%d", row), getStringValue(client, "phone"))
		f.SetCellValue(clientSheet, fmt.Sprintf("E%d", row), getStringValue(client, "address"))
		f.SetCellValue(clientSheet, fmt.Sprintf("F%d", row), getStringValue(client, "createdAt"))
	}

	// Create Invoices sheet
	invoiceSheet := "Invoices"
	f.NewSheet(invoiceSheet)
	
	invoiceHeaders := []string{"ID", "Invoice Number", "Client Name", "Total", "Status", "Issue Date", "Due Date"}
	for i, h := range invoiceHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(invoiceSheet, cell, h)
	}
	
	for i, invoice := range data.Invoices {
		row := i + 2
		f.SetCellValue(invoiceSheet, fmt.Sprintf("A%d", row), getStringValue(invoice, "id"))
		f.SetCellValue(invoiceSheet, fmt.Sprintf("B%d", row), getStringValue(invoice, "invoiceNumber"))
		f.SetCellValue(invoiceSheet, fmt.Sprintf("C%d", row), getStringValue(invoice, "clientName"))
		f.SetCellValue(invoiceSheet, fmt.Sprintf("D%d", row), getNumberValue(invoice, "total"))
		f.SetCellValue(invoiceSheet, fmt.Sprintf("E%d", row), getStringValue(invoice, "status"))
		f.SetCellValue(invoiceSheet, fmt.Sprintf("F%d", row), getStringValue(invoice, "issueDate"))
		f.SetCellValue(invoiceSheet, fmt.Sprintf("G%d", row), getStringValue(invoice, "dueDate"))
	}

	// Create Services sheet
	serviceSheet := "Services"
	f.NewSheet(serviceSheet)
	
	serviceHeaders := []string{"ID", "Name", "Price", "Category"}
	for i, h := range serviceHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(serviceSheet, cell, h)
	}
	
	for i, service := range data.Services {
		row := i + 2
		f.SetCellValue(serviceSheet, fmt.Sprintf("A%d", row), getStringValue(service, "id"))
		f.SetCellValue(serviceSheet, fmt.Sprintf("B%d", row), getStringValue(service, "name"))
		f.SetCellValue(serviceSheet, fmt.Sprintf("C%d", row), getNumberValue(service, "price"))
		f.SetCellValue(serviceSheet, fmt.Sprintf("D%d", row), getStringValue(service, "category"))
	}

	// Write to buffer
	buffer, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}

func getStringValue(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func getNumberValue(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case int64:
			return float64(n)
		}
	}
	return 0
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
