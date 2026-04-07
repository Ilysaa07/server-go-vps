package firestore

import (
	"context"
	"fmt"
	"time"
)

// WAStatusEntry represents a single WA status that was posted
type WAStatusEntry struct {
	MessageID string    `firestore:"messageId"`
	BannerURL string    `firestore:"bannerUrl"`
	Caption   string    `firestore:"caption"`
	PostedAt  time.Time `firestore:"postedAt"`
}

// WAStatusRecord represents the document stored in Firestore
type WAStatusRecord struct {
	StatusIDs    []WAStatusEntry `firestore:"statusIds"`
	LastPostedAt time.Time       `firestore:"lastPostedAt"`
	ActiveNumber string          `firestore:"activeNumber"`
}

// WAStatusRepository manages WhatsApp Status entries in Firestore
type WAStatusRepository struct {
	client     *Client
	collection string
	docID      string
}

// NewWAStatusRepository creates a new WA status repository
func NewWAStatusRepository(client *Client) *WAStatusRepository {
	return &WAStatusRepository{
		client:     client,
		collection: "settings",
		docID:      "wa_status_ids",
	}
}

// SaveStatusIDs saves the posted WA status IDs to Firestore
func (r *WAStatusRepository) SaveStatusIDs(ctx context.Context, entries []WAStatusEntry, activeNumber string) error {
	if r.client == nil || r.client.FS == nil {
		return fmt.Errorf("firestore client not initialized")
	}

	docRef := r.client.FS.Collection(r.collection).Doc(r.docID)
	record := WAStatusRecord{
		StatusIDs:    entries,
		LastPostedAt: time.Now(),
		ActiveNumber: activeNumber,
	}

	_, err := docRef.Set(ctx, record)
	if err != nil {
		return fmt.Errorf("failed to save WA status IDs: %w", err)
	}

	fmt.Printf("💾 Saved %d WA status IDs to Firestore\n", len(entries))
	return nil
}

// GetStatusIDs retrieves the posted WA status IDs from Firestore
func (r *WAStatusRepository) GetStatusIDs(ctx context.Context) (*WAStatusRecord, error) {
	if r.client == nil || r.client.FS == nil {
		return nil, fmt.Errorf("firestore client not initialized")
	}

	docRef := r.client.FS.Collection(r.collection).Doc(r.docID)
	snap, err := docRef.Get(ctx)
	if err != nil {
		// Document doesn't exist yet
		return &WAStatusRecord{StatusIDs: []WAStatusEntry{}}, nil
	}

	var record WAStatusRecord
	if err := snap.DataTo(&record); err != nil {
		return nil, fmt.Errorf("failed to decode WA status record: %w", err)
	}

	return &record, nil
}

// ClearStatusIDs removes all WA status IDs from Firestore
func (r *WAStatusRepository) ClearStatusIDs(ctx context.Context) error {
	if r.client == nil || r.client.FS == nil {
		return fmt.Errorf("firestore client not initialized")
	}

	docRef := r.client.FS.Collection(r.collection).Doc(r.docID)
	_, err := docRef.Set(ctx, WAStatusRecord{
		StatusIDs:    []WAStatusEntry{},
		LastPostedAt: time.Now(),
	})
	if err != nil {
		return fmt.Errorf("failed to clear WA status IDs: %w", err)
	}

	fmt.Println("🗑️ Cleared all WA status IDs from Firestore")
	return nil
}

// IsExpired checks whether the last post is older than 24 hours
func (r *WAStatusRecord) IsExpired() bool {
	if r.LastPostedAt.IsZero() {
		return true
	}
	return time.Since(r.LastPostedAt) >= 24*time.Hour
}

// WASettingsRecord represents the whatsapp_settings document in Firestore
type WASettingsRecord struct {
	MainNumber      string   `firestore:"mainNumber"`
	ScheduleEnabled bool     `firestore:"scheduleEnabled"`
	ScheduleType    string   `firestore:"scheduleType"`
	ExcludedNumbers []string `firestore:"excludedNumbers"`
}

// GetWhatsAppActiveNumber reads the current active WhatsApp number from Firestore settings
// It applies the same rotation logic as the frontend
func (r *WAStatusRepository) GetWhatsAppActiveNumber(ctx context.Context) (string, error) {
	if r.client == nil || r.client.FS == nil {
		return "6281399710085", nil // default fallback
	}

	docRef := r.client.FS.Collection("settings").Doc("whatsapp_settings")
	snap, err := docRef.Get(ctx)
	if err != nil {
		// Settings not found, use default
		return "6281399710085", nil
	}

	var settings WASettingsRecord
	if err := snap.DataTo(&settings); err != nil {
		return "6281399710085", nil
	}

	// If schedule is not enabled, return static main number
	if !settings.ScheduleEnabled {
		if settings.MainNumber != "" {
			return settings.MainNumber, nil
		}
		return "6281399710085", nil
	}

	// Apply rotation logic (same as frontend)
	allAgents := []string{
		"6281399710085",
		"6283190138549",
		"6289518530306",
		"62895635367495",
		"62881023845975",
		"6282258115474",
	}

	excluded := settings.ExcludedNumbers
	available := []string{}
	for _, num := range allAgents {
		isExcluded := false
		for _, ex := range excluded {
			if ex == num {
				isExcluded = true
				break
			}
		}
		if !isExcluded {
			available = append(available, num)
		}
	}

	if len(available) == 0 {
		return "6281399710085", nil
	}

	now := time.Now()
	var index int

	schedType := settings.ScheduleType
	if schedType == "" {
		schedType = "daily"
	}

	switch schedType {
	case "daily":
		days := int(now.Unix() / 86400)
		index = days % len(available)
	case "weekly":
		weeks := int(now.Unix() / (86400 * 7))
		index = weeks % len(available)
	case "monthly":
		months := (now.Year() * 12) + int(now.Month())
		index = months % len(available)
	default:
		index = 0
	}

	return available[index], nil
}

// GetBannerSettings reads the banner_settings document from Firestore
func (r *WAStatusRepository) GetBannerSettings(ctx context.Context) ([]map[string]string, error) {
	if r.client == nil || r.client.FS == nil {
		return nil, fmt.Errorf("firestore client not initialized")
	}

	docRef := r.client.FS.Collection("settings").Doc("banner_settings")
	snap, err := docRef.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("banner_settings not found: %w", err)
	}

	data := snap.Data()
	imagesRaw, ok := data["images"]
	if !ok {
		return []map[string]string{}, nil
	}

	var images []map[string]string
	imagesSlice, ok := imagesRaw.([]interface{})
	if !ok {
		return []map[string]string{}, nil
	}

	for _, item := range imagesSlice {
		imgMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		entry := map[string]string{}
		if url, ok := imgMap["url"].(string); ok {
			entry["url"] = url
		}
		if title, ok := imgMap["title"].(string); ok {
			entry["title"] = title
		}
		images = append(images, entry)
	}

	return images, nil
}
