package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"wa-server-go/internal/firestore"

	"github.com/gin-gonic/gin"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// SyncWAStatusRequest is the request body for POST /sync-wa-status
type SyncWAStatusRequest struct {
	Banners []struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"banners"`
	ActiveNumber string `json:"activeNumber,omitempty"` // optional override
}

// WAStatusRepo is the repository for WA status management (injected via Handler setup)
var waStatusRepo *firestore.WAStatusRepository

// InitWAStatusRepo sets up the WA status repository
func InitWAStatusRepo(repo *firestore.WAStatusRepository) {
	waStatusRepo = repo
}

// SyncWAStatus handles POST /sync-wa-status
// Downloads banner images and posts them as WhatsApp status (stories)
func (h *Handler) SyncWAStatus(c *gin.Context) {
	var req SyncWAStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	if len(req.Banners) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "banners array is empty"})
		return
	}

	// Get bot client
	botClient, ok := h.WAManager.GetClient("bot")
	if !ok || !botClient.IsReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "WhatsApp Bot client is not ready",
		})
		return
	}

	ctx := context.Background()

	// Get active WhatsApp number
	activeNumber := req.ActiveNumber
	if activeNumber == "" && waStatusRepo != nil {
		num, err := waStatusRepo.GetWhatsAppActiveNumber(ctx)
		if err == nil {
			activeNumber = num
		}
	}
	if activeNumber == "" {
		activeNumber = "6281399710085"
	}

	// Format caption
	caption := fmt.Sprintf("Informasi lebih lanjut hubungi: +%s", formatPhoneCaption(activeNumber))

	// Limit to max 10 banners
	banners := req.Banners
	if len(banners) > 10 {
		banners = banners[:10]
	}

	// First: revoke/delete old status messages
	h.revokeOldStatuses(ctx, botClient.WAClient)

	// Post new statuses
	statusJID := types.JID{User: "status", Server: "broadcast"}

	var (
		entries []firestore.WAStatusEntry
		mu      sync.Mutex
		posted  int
	)

	for i, banner := range banners {
		if banner.URL == "" {
			continue
		}

		fmt.Printf("📸 [WA Status] Posting banner %d/%d: %s\n", i+1, len(banners), banner.URL)

		// Download the image
		imgData, contentType, err := downloadImage(banner.URL)
		if err != nil {
			fmt.Printf("⚠️ [WA Status] Failed to download banner %d: %v\n", i+1, err)
			continue
		}

		// Upload to WhatsApp media server
		uploaded, err := botClient.WAClient.Upload(ctx, imgData, whatsmeow.MediaImage)
		if err != nil {
			fmt.Printf("⚠️ [WA Status] Failed to upload banner %d: %v\n", i+1, err)
			continue
		}

		// Determine mime type
		mimeType := contentType
		if mimeType == "" {
			mimeType = "image/jpeg"
		}

		// Send as status (story) to status@broadcast
		resp, err := botClient.WAClient.SendMessage(ctx, statusJID, &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				URL:           proto.String(uploaded.URL),
				Mimetype:      proto.String(mimeType),
				Caption:       proto.String(caption),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(imgData))),
				ViewOnce:      proto.Bool(false),
			},
		})
		if err != nil {
			fmt.Printf("⚠️ [WA Status] Failed to send status for banner %d: %v\n", i+1, err)
			continue
		}

		mu.Lock()
		entries = append(entries, firestore.WAStatusEntry{
			MessageID: resp.ID,
			BannerURL: banner.URL,
			Caption:   caption,
			PostedAt:  time.Now(),
		})
		posted++
		mu.Unlock()

		fmt.Printf("✅ [WA Status] Banner %d posted as status (ID: %s)\n", i+1, resp.ID)

		// Human-like delay between status posts
		if i < len(banners)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	// Save status IDs to Firestore
	if waStatusRepo != nil && len(entries) > 0 {
		if err := waStatusRepo.SaveStatusIDs(ctx, entries, activeNumber); err != nil {
			fmt.Printf("⚠️ [WA Status] Failed to save status IDs to Firestore: %v\n", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"posted":       posted,
		"total":        len(banners),
		"activeNumber": activeNumber,
		"message":      fmt.Sprintf("%d status WhatsApp berhasil dikirim", posted),
	})
}

// ClearWAStatus handles DELETE /clear-wa-status
// Revokes/deletes all previously posted WA statuses
func (h *Handler) ClearWAStatus(c *gin.Context) {
	botClient, ok := h.WAManager.GetClient("bot")
	if !ok || !botClient.IsReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"error":   "WhatsApp Bot client is not ready",
		})
		return
	}

	ctx := context.Background()
	revoked := h.revokeOldStatuses(ctx, botClient.WAClient)

	// Clear from Firestore
	if waStatusRepo != nil {
		if err := waStatusRepo.ClearStatusIDs(ctx); err != nil {
			fmt.Printf("⚠️ [WA Status] Failed to clear status IDs from Firestore: %v\n", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"revoked": revoked,
		"message": fmt.Sprintf("%d status WhatsApp dihapus", revoked),
	})
}

// revokeOldStatuses revokes all previously posted WA statuses stored in Firestore
// Returns the number of successfully revoked statuses
func (h *Handler) revokeOldStatuses(ctx context.Context, waClient interface{ RevokeMessage(context.Context, types.JID, types.MessageID) (whatsmeow.SendResponse, error) }) int {
	if waStatusRepo == nil {
		return 0
	}

	record, err := waStatusRepo.GetStatusIDs(ctx)
	if err != nil || len(record.StatusIDs) == 0 {
		return 0
	}

	statusJID := types.JID{User: "status", Server: "broadcast"}
	revoked := 0

	for _, entry := range record.StatusIDs {
		if entry.MessageID == "" {
			continue
		}
		_, err := waClient.RevokeMessage(ctx, statusJID, types.MessageID(entry.MessageID))
		if err != nil {
			fmt.Printf("⚠️ [WA Status] Failed to revoke status %s: %v\n", entry.MessageID, err)
		} else {
			fmt.Printf("🗑️ [WA Status] Revoked status %s\n", entry.MessageID)
			revoked++
		}
		time.Sleep(500 * time.Millisecond)
	}

	return revoked
}

// downloadImage downloads an image from a URL and returns its bytes and content type
func downloadImage(url string) ([]byte, string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("non-200 response: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	// Normalize content type (sometimes has charset etc)
	if len(contentType) > 0 {
		for i, c := range contentType {
			if c == ';' {
				contentType = contentType[:i]
				break
			}
		}
	}

	return data, contentType, nil
}

// formatPhoneCaption formats a phone number for caption display
func formatPhoneCaption(number string) string {
	// Remove leading 0 if exists, keep the number as is
	if len(number) > 2 && number[:2] == "62" {
		// Indonesian number: 62xxx → 0xxx → display as 08xxx
		return "0" + number[2:]
	}
	return number
}

// SyncWAStatusFromScheduler is called by the 24h scheduler to re-post statuses if expired
func (h *Handler) SyncWAStatusFromScheduler(ctx context.Context) {
	if waStatusRepo == nil {
		return
	}

	// Check if status is expired (older than 24 hours)
	record, err := waStatusRepo.GetStatusIDs(ctx)
	if err != nil {
		fmt.Printf("⚠️ [WA Status Scheduler] Failed to read status IDs: %v\n", err)
		return
	}

	// If not expired yet, skip
	if !record.IsExpired() {
		fmt.Println("⏰ [WA Status Scheduler] Status not expired yet, skipping re-post")
		return
	}

	fmt.Println("⏰ [WA Status Scheduler] Status expired or empty, fetching banners and re-posting...")

	// Fetch banners from Firestore
	banners, err := waStatusRepo.GetBannerSettings(ctx)
	if err != nil || len(banners) == 0 {
		fmt.Printf("⚠️ [WA Status Scheduler] No banners found: %v\n", err)
		return
	}

	// Get bot client
	botClient, ok := h.WAManager.GetClient("bot")
	if !ok || !botClient.IsReady() {
		fmt.Println("⚠️ [WA Status Scheduler] Bot client not ready, skipping")
		return
	}

	// Build request data and re-use SyncWAStatus logic
	activeNumber, _ := waStatusRepo.GetWhatsAppActiveNumber(ctx)
	if activeNumber == "" {
		activeNumber = "6281399710085"
	}

	caption := fmt.Sprintf("Informasi lebih lanjut hubungi: +%s", formatPhoneCaption(activeNumber))
	statusJID := types.JID{User: "status", Server: "broadcast"}

	// Limit to max 10
	if len(banners) > 10 {
		banners = banners[:10]
	}

	var entries []firestore.WAStatusEntry

	for i, banner := range banners {
		url := banner["url"]
		if url == "" {
			continue
		}

		imgData, contentType, err := downloadImage(url)
		if err != nil {
			fmt.Printf("⚠️ [WA Status Scheduler] Failed to download banner %d: %v\n", i+1, err)
			continue
		}

		uploaded, err := botClient.WAClient.Upload(ctx, imgData, whatsmeow.MediaImage)
		if err != nil {
			fmt.Printf("⚠️ [WA Status Scheduler] Failed to upload banner %d: %v\n", i+1, err)
			continue
		}

		mimeType := contentType
		if mimeType == "" {
			mimeType = "image/jpeg"
		}

		resp, err := botClient.WAClient.SendMessage(ctx, statusJID, &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				URL:           proto.String(uploaded.URL),
				Mimetype:      proto.String(mimeType),
				Caption:       proto.String(caption),
				DirectPath:    proto.String(uploaded.DirectPath),
				MediaKey:      uploaded.MediaKey,
				FileEncSHA256: uploaded.FileEncSHA256,
				FileSHA256:    uploaded.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(imgData))),
				ViewOnce:      proto.Bool(false),
			},
		})
		if err != nil {
			fmt.Printf("⚠️ [WA Status Scheduler] Failed to send status %d: %v\n", i+1, err)
			continue
		}

		entries = append(entries, firestore.WAStatusEntry{
			MessageID: resp.ID,
			BannerURL: url,
			Caption:   caption,
			PostedAt:  time.Now(),
		})

		fmt.Printf("✅ [WA Status Scheduler] Banner %d re-posted (ID: %s)\n", i+1, resp.ID)

		if i < len(banners)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	if len(entries) > 0 {
		if err := waStatusRepo.SaveStatusIDs(ctx, entries, activeNumber); err != nil {
			fmt.Printf("⚠️ [WA Status Scheduler] Failed to save status IDs: %v\n", err)
		} else {
			fmt.Printf("✅ [WA Status Scheduler] Re-posted %d statuses\n", len(entries))
		}
	}
}
