package firestore

import (
	"context"
	"log"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// BusinessClient represents a business client in Firestore
type BusinessClient struct {
	ID           string    `firestore:"-"`
	Name         string    `firestore:"name"`
	Email        string    `firestore:"email"`
	PasswordHash string    `firestore:"passwordHash"` // For portal login
	Phone        string    `firestore:"phone"`
	Address      string    `firestore:"address,omitempty"`
	CreatedAt    time.Time `firestore:"createdAt"`
	UpdatedAt    time.Time `firestore:"updatedAt"`
}

// BusinessClientRepository provides access to the clients collection
type BusinessClientRepository struct {
	client     *Client
	collection string
}

// NewBusinessClientRepository creates a new business client repository
func NewBusinessClientRepository(client *Client) *BusinessClientRepository {
	return &BusinessClientRepository{
		client:     client,
		collection: "clients",
	}
}

// Create creates a new business client
func (r *BusinessClientRepository) Create(ctx context.Context, client *BusinessClient) (string, error) {
	now := time.Now()
	client.CreatedAt = now
	client.UpdatedAt = now

	docRef, _, err := r.client.FS.Collection(r.collection).Add(ctx, client)
	if err != nil {
		return "", err
	}
	return docRef.ID, nil
}

// GetByID retrieves a client by ID
func (r *BusinessClientRepository) GetByID(ctx context.Context, id string) (*BusinessClient, error) {
	log.Printf("Repo: GetByID fetching from collection '%s' for ID '%s'", r.collection, id)
	doc, err := r.client.FS.Collection(r.collection).Doc(id).Get(ctx)
	if err != nil {
		return nil, err
	}

	var client BusinessClient
	if err := doc.DataTo(&client); err != nil {
		return nil, err
	}
	client.ID = doc.Ref.ID

	return &client, nil
}

// GetByEmail retrieves a client by Email
func (r *BusinessClientRepository) GetByEmail(ctx context.Context, email string) (*BusinessClient, error) {
	iter := r.client.FS.Collection(r.collection).
		Where("email", "==", email).
		Limit(1).
		Documents(ctx)
	defer iter.Stop()

	doc, err := iter.Next()
	if err == iterator.Done {
		return nil, nil // Not found
	}
	if err != nil {
		return nil, err
	}

	var client BusinessClient
	if err := doc.DataTo(&client); err != nil {
		return nil, err
	}
	client.ID = doc.Ref.ID

	return &client, nil
}

// Update updates a client
func (r *BusinessClientRepository) Update(ctx context.Context, id string, updates map[string]interface{}) error {
	updates["updatedAt"] = time.Now()
	_, err := r.client.FS.Collection(r.collection).Doc(id).Set(ctx, updates, firestore.MergeAll)
	return err
}

// GetAll retrieves all clients (for admin list)
func (r *BusinessClientRepository) GetAll(ctx context.Context, limit int) ([]BusinessClient, error) {
	query := r.client.FS.Collection(r.collection).OrderBy("createdAt", firestore.Desc)
	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)
	defer iter.Stop()

	var clients []BusinessClient
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var client BusinessClient
		if err := doc.DataTo(&client); err != nil {
			continue
		}
		client.ID = doc.Ref.ID
		clients = append(clients, client)
	}

	return clients, nil
}
