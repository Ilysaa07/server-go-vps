package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/xuri/excelize/v2"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// BackupService handles scheduled backup tasks
type BackupService struct {
	waClient    *whatsmeow.Client
	webURL      string
	backupPhone string
	cron        *cron.Cron
}

// NewBackupService creates a new backup service
func NewBackupService(waClient *whatsmeow.Client, webURL, backupPhone string) *BackupService {
	return &BackupService{
		waClient:    waClient,
		webURL:      webURL,
		backupPhone: backupPhone,
		cron:        cron.New(),
	}
}

// Start starts the backup cron job (runs daily at 23:00 WIB)
func (s *BackupService) Start() error {
	_, err := s.cron.AddFunc("0 23 * * *", func() {
		log.Println("🔄 [BACKUP] Starting scheduled backup...")
		if err := s.RunBackup(); err != nil {
			log.Printf("❌ [BACKUP] Failed: %v", err)
		}
	})
	if err != nil {
		return fmt.Errorf("failed to schedule backup: %w", err)
	}

	s.cron.Start()
	log.Println("✅ [BACKUP] Scheduler started (runs daily at 23:00 WIB)")
	return nil
}

// Stop stops the backup cron job
func (s *BackupService) Stop() {
	s.cron.Stop()
}

// RunBackup executes the backup process
func (s *BackupService) RunBackup() error {
	ctx := context.Background()
	timestamp := time.Now()
	dateStr := timestamp.Format("20060102")

	// 1. Fetch data from web API
	dataURL := fmt.Sprintf("%s/api/backup/data-dump", s.webURL)
	log.Printf("📥 [BACKUP] Fetching data from %s", dataURL)

	resp, err := http.Get(dataURL)
	if err != nil {
		return fmt.Errorf("failed to fetch backup data: %w", err)
	}
	defer resp.Body.Close()

	var backupData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&backupData); err != nil {
		return fmt.Errorf("failed to parse backup data: %w", err)
	}

	// 2. Generate Excel file
	excelData, err := s.generateExcel(backupData, dateStr)
	if err != nil {
		return fmt.Errorf("failed to generate Excel: %w", err)
	}

	// 3. Generate JSON backup
	jsonData, err := json.MarshalIndent(backupData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to generate JSON: %w", err)
	}

	// 4. Send Excel via WhatsApp
	excelFileName := fmt.Sprintf("BACKUP_DATA_%s.xlsx", dateStr)
	if err := s.sendFile(ctx, excelData, excelFileName, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"); err != nil {
		log.Printf("⚠️ [BACKUP] Failed to send Excel: %v", err)
	} else {
		log.Println("✅ [BACKUP] Excel file sent")
	}

	// 5. Send JSON via WhatsApp
	jsonFileName := fmt.Sprintf("BACKUP_RESTORE_%s.json", dateStr)
	if err := s.sendFile(ctx, jsonData, jsonFileName, "application/json"); err != nil {
		log.Printf("⚠️ [BACKUP] Failed to send JSON: %v", err)
	} else {
		log.Println("✅ [BACKUP] JSON file sent")
	}

	// 6. Send completion notification
	notification := fmt.Sprintf("✅ *BACKUP BERHASIL*\n\n📁 Files: %s, %s\n🕐 Waktu: %s\n\nBackup data harian telah berhasil dikirim.",
		excelFileName, jsonFileName, timestamp.Format("02 Jan 2006 15:04 WIB"))
	jid := types.NewJID(s.backupPhone, types.DefaultUserServer)
	_, _ = s.waClient.SendMessage(ctx, jid, &waProto.Message{
		Conversation: proto.String(notification),
	})

	log.Printf("✅ [BACKUP] Completed successfully at %s", timestamp.Format("15:04:05"))
	return nil
}

// generateExcel creates an Excel file from backup data
func (s *BackupService) generateExcel(data map[string]interface{}, dateStr string) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	// Create sheets for each data type
	for sheetName, sheetData := range data {
		// Create sheet
		_, err := f.NewSheet(sheetName)
		if err != nil {
			continue
		}

		// Write data based on type
		if arr, ok := sheetData.([]interface{}); ok {
			for i, row := range arr {
				if rowMap, ok := row.(map[string]interface{}); ok {
					col := 'A'
					for key, value := range rowMap {
						if i == 0 {
							// Write header
							headerCell := fmt.Sprintf("%c%d", col, 1)
							f.SetCellValue(sheetName, headerCell, key)
						}
						dataCell := fmt.Sprintf("%c%d", col, i+2)
						f.SetCellValue(sheetName, dataCell, fmt.Sprintf("%v", value))
						col++
					}
				}
			}
		}
	}

	// Delete default sheet
	f.DeleteSheet("Sheet1")

	// Write to buffer
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// sendFile uploads and sends a file via WhatsApp
func (s *BackupService) sendFile(ctx context.Context, data []byte, fileName, mimeType string) error {
	if s.waClient == nil {
		return fmt.Errorf("WhatsApp client not available")
	}

	// Upload file
	uploaded, err := s.waClient.Upload(ctx, data, whatsmeow.MediaDocument)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	// Send document
	jid := types.NewJID(s.backupPhone, types.DefaultUserServer)
	_, err = s.waClient.SendMessage(ctx, jid, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(uploaded.URL),
			Mimetype:      proto.String(mimeType),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
		},
	})

	return err
}

// TriggerManual runs backup immediately (for API trigger)
func (s *BackupService) TriggerManual() error {
	return s.RunBackup()
}
