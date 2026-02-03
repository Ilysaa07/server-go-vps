package firestore

import (
	"context"
	"sort"
	"time"

	"google.golang.org/api/iterator"
)

// ClientDocument represents a document belonging to a Business Client
type ClientDocument struct {
	ID          string    `firestore:"-"`
	ClientID    string    `firestore:"clientId"` // Links to BusinessClient.ID
	FileName    string    `firestore:"fileName"`
	FileType    string    `firestore:"fileType"`
	FileSize    int64     `firestore:"fileSize"`
	FileURL     string    `firestore:"fileUrl"`     // Relative URL path
	StoragePath string    `firestore:"storagePath"` // Physical disk path
	EncodedName string    `firestore:"encodedName"` // Name on disk
	UploadedBy  string    `firestore:"uploadedBy"`
	UploadedAt  time.Time `firestore:"uploadedAt"`
	Description string    `firestore:"description,omitempty"`
	Category    string    `firestore:"category,omitempty"`
}

// ClientDocumentRepository provides access to the client_documents collection
type ClientDocumentRepository struct {
	client     *Client
	collection string
}

// NewClientDocumentRepository creates a new client document repository
func NewClientDocumentRepository(client *Client) *ClientDocumentRepository {
	return &ClientDocumentRepository{
		client:     client,
		collection: "client_documents",
	}
}

// Create saves a new document metadata
func (r *ClientDocumentRepository) Create(ctx context.Context, doc *ClientDocument) error {
	if doc.UploadedAt.IsZero() {
		doc.UploadedAt = time.Now()
	}

	docRef, _, err := r.client.FS.Collection(r.collection).Add(ctx, doc)
	if err != nil {
		return err
	}
	doc.ID = docRef.ID
	return nil
}

// GetByClientID retrieves all documents for a specific client
func (r *ClientDocumentRepository) GetByClientID(ctx context.Context, clientID string) ([]ClientDocument, error) {
	query := r.client.FS.Collection(r.collection).
		Where("clientId", "==", clientID)
		// OrderBy("uploadedAt", firestore.Desc) // Removed to avoid composite index requirement

	iter := query.Documents(ctx)
	defer iter.Stop()

	var docs []ClientDocument
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var d ClientDocument
		if err := doc.DataTo(&d); err != nil {
			continue
		}
		d.ID = doc.Ref.ID
		docs = append(docs, d)
	}

	// Sort in memory
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].UploadedAt.After(docs[j].UploadedAt)
	})

	return docs, nil
}

// GetByID retrieves a document by ID
func (r *ClientDocumentRepository) GetByID(ctx context.Context, id string) (*ClientDocument, error) {
	doc, err := r.client.FS.Collection(r.collection).Doc(id).Get(ctx)
	if err != nil {
		return nil, err
	}

	var d ClientDocument
	if err := doc.DataTo(&d); err != nil {
		return nil, err
	}
	d.ID = doc.Ref.ID

	return &d, nil
}

// Delete deletes a document metadata
func (r *ClientDocumentRepository) Delete(ctx context.Context, id string) error {
	_, err := r.client.FS.Collection(r.collection).Doc(id).Delete(ctx)
	return err
}
