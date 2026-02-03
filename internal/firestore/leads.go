package firestore

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// Lead represents a contact/lead in Firestore (replacement for WA Labels)
type Lead struct {
	ID            string    `firestore:"-"`
	Phone         string    `firestore:"phone"`
	Name          string    `firestore:"name"`
	PushName      string    `firestore:"pushname,omitempty"`
	Tags          []string  `firestore:"tags"`
	Source        string    `firestore:"source"` // whatsapp_sync, manual, import, incoming_message
	LastMessageAt time.Time `firestore:"lastMessageAt,omitempty"`
	MessageCount  int       `firestore:"messageCount,omitempty"`
	SyncedAt      time.Time `firestore:"syncedAt"`
	CreatedAt     time.Time `firestore:"createdAt"`
	UpdatedAt     time.Time `firestore:"updatedAt"`
}

// LeadsRepository provides access to the leads collection
type LeadsRepository struct {
	client     *Client
	collection string
}

// NewLeadsRepository creates a new leads repository
func NewLeadsRepository(client *Client) *LeadsRepository {
	return &LeadsRepository{
		client:     client,
		collection: "leads",
	}
}

// GetByTag retrieves leads with a specific tag
func (r *LeadsRepository) GetByTag(ctx context.Context, tag string) ([]Lead, error) {
	iter := r.client.Collection(r.collection).
		Where("tags", "array-contains", tag).
		Documents(ctx)

	var leads []Lead
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var lead Lead
		if err := doc.DataTo(&lead); err != nil {
			continue
		}
		lead.ID = doc.Ref.ID
		leads = append(leads, lead)
	}

	return leads, nil
}

// GetAll retrieves all leads
func (r *LeadsRepository) GetAll(ctx context.Context, limit int) ([]Lead, error) {
	query := r.client.Collection(r.collection).OrderBy("createdAt", firestore.Desc)
	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)

	var leads []Lead
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var lead Lead
		if err := doc.DataTo(&lead); err != nil {
			continue
		}
		lead.ID = doc.Ref.ID
		leads = append(leads, lead)
	}

	return leads, nil
}

// GetByPhone retrieves a lead by phone number
func (r *LeadsRepository) GetByPhone(ctx context.Context, phone string) (*Lead, error) {
	iter := r.client.Collection(r.collection).
		Where("phone", "==", phone).
		Limit(1).
		Documents(ctx)

	doc, err := iter.Next()
	if err == iterator.Done {
		return nil, nil // Not found
	}
	if err != nil {
		return nil, err
	}

	var lead Lead
	if err := doc.DataTo(&lead); err != nil {
		return nil, err
	}
	lead.ID = doc.Ref.ID
	return &lead, nil
}

// Create creates a new lead
func (r *LeadsRepository) Create(ctx context.Context, lead *Lead) (string, error) {
	now := time.Now()
	lead.CreatedAt = now
	lead.UpdatedAt = now
	lead.SyncedAt = now

	docRef, _, err := r.client.Collection(r.collection).Add(ctx, lead)
	if err != nil {
		return "", err
	}
	return docRef.ID, nil
}

// Update updates an existing lead
func (r *LeadsRepository) Update(ctx context.Context, id string, updates map[string]interface{}) error {
	updates["updatedAt"] = time.Now()

	updateFields := make([]firestore.Update, 0, len(updates))
	for key, value := range updates {
		updateFields = append(updateFields, firestore.Update{Path: key, Value: value})
	}

	_, err := r.client.Collection(r.collection).Doc(id).Update(ctx, updateFields)
	return err
}

// UpsertFromMessage creates or updates a lead from an incoming message
func (r *LeadsRepository) UpsertFromMessage(ctx context.Context, phone, pushName string) error {
	existing, err := r.GetByPhone(ctx, phone)
	if err != nil {
		return err
	}

	now := time.Now()

	if existing != nil {
		// Update existing lead
		updates := map[string]interface{}{
			"lastMessageAt": now,
			"messageCount":  existing.MessageCount + 1,
		}
		if pushName != "" && existing.PushName != pushName {
			updates["pushname"] = pushName
		}
		return r.Update(ctx, existing.ID, updates)
	}

	// Create new lead
	newLead := &Lead{
		Phone:         phone,
		Name:          pushName,
		PushName:      pushName,
		Tags:          []string{},
		Source:        "incoming_message",
		LastMessageAt: now,
		MessageCount:  1,
	}
	_, err = r.Create(ctx, newLead)
	return err
}
