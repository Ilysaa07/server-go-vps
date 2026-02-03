package firestore

import (
	"context"
	"fmt"
	"log"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/option"
)

// Client wraps the Firestore client
type Client struct {
	FS        *firestore.Client
	ProjectID string
}

// NewClient creates a new Firestore client
func NewClient(ctx context.Context, credentialsPath string, projectID string) (*Client, error) {
	var app *firebase.App
	var err error

	if credentialsPath != "" {
		opt := option.WithCredentialsFile(credentialsPath)
		conf := &firebase.Config{ProjectID: projectID}
		app, err = firebase.NewApp(ctx, conf, opt)
	} else {
		// Use Application Default Credentials (for GCP environments)
		conf := &firebase.Config{ProjectID: projectID}
		app, err = firebase.NewApp(ctx, conf)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create Firebase app: %w", err)
	}

	fs, err := app.Firestore(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Firestore client: %w", err)
	}

	log.Printf("✅ Firestore client initialized for project: %s", projectID)

	return &Client{
		FS:        fs,
		ProjectID: projectID,
	}, nil
}

// Close closes the Firestore client
func (c *Client) Close() error {
	if c.FS != nil {
		return c.FS.Close()
	}
	return nil
}

// Collection returns a reference to a collection
func (c *Client) Collection(path string) *firestore.CollectionRef {
	return c.FS.Collection(path)
}

// Doc returns a reference to a document
func (c *Client) Doc(path string) *firestore.DocumentRef {
	return c.FS.Doc(path)
}

// Batch returns a write batch
func (c *Client) Batch() *firestore.WriteBatch {
	return c.FS.Batch()
}
