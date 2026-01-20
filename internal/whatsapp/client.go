package whatsapp

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite"
)

// Client wraps whatsmeow.Client with additional state
type Client struct {
	WAClient  *whatsmeow.Client
	Container *sqlstore.Container
	Device    *store.Device
	ID        string
	Ready     bool
	QRCode    string
	mu        sync.RWMutex
}

// NewClient creates a new WhatsApp client with SQLite session storage
func NewClient(ctx context.Context, clientID string, dbPath string) (*Client, error) {
	// Create SQLite container for session storage
	// Use WAL mode, busy_timeout, and cache=shared for better concurrent access
	dbLog := waLog.Stdout("Database", "ERROR", true)
	dsn := dbPath + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&cache=shared"
	container, err := sqlstore.New(ctx, "sqlite", dsn, dbLog)
	if err != nil {
		return nil, fmt.Errorf("failed to create SQLite container: %w", err)
	}

	// Get or create device
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}

	// Create whatsmeow client
	clientLog := waLog.Stdout("Client", "INFO", true)
	waClient := whatsmeow.NewClient(deviceStore, clientLog)
	
	// CRITICAL: Enable emitting AppState events during full sync to get existing labels
	waClient.EmitAppStateEventsOnFullSync = true

	client := &Client{
		WAClient:  waClient,
		Container: container,
		Device:    deviceStore,
		ID:        clientID,
		Ready:     false,
	}

	return client, nil
}

// Connect initiates the WhatsApp connection
func (c *Client) Connect(ctx context.Context, qrChan chan<- string, statusChan chan<- StatusUpdate) error {
	// Check if already logged in
	if c.WAClient.Store.ID == nil {
		// Need to pair with QR code
		qrChannel, _ := c.WAClient.GetQRChannel(ctx)
		err := c.WAClient.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		// Handle QR codes
		go func() {
			for evt := range qrChannel {
				if evt.Event == "code" {
					// Generate QR code as data URL (Optional: Keep for terminal log only)
					// qrPNG, err := qrcode.Encode(evt.Code, qrcode.Medium, 256)
					
					// Store the RAW code (Critical for Frontend QRCodeSVG)
					c.mu.Lock()
					c.QRCode = evt.Code // Save Raw String!
					c.mu.Unlock()
					
					// Send raw code to channel
					qrChan <- evt.Code

					// Also print to terminal
					qrcode.WriteFile(evt.Code, qrcode.Medium, 256, "qr-"+c.ID+".png")
					fmt.Printf("\n[%s] 📱 QR Code generated. Scan with WhatsApp!\n", c.ID)
				}
			}
		}()
	} else {
		// Already logged in, just connect
		err := c.WAClient.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
	}

	return nil
}

// IsReady returns whether the client is connected and ready
func (c *Client) IsReady() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Ready
}

// SetReady updates the ready state
func (c *Client) SetReady(ready bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Ready = ready
}

// GetQRCode returns the current QR code
func (c *Client) GetQRCode() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.QRCode
}

// Disconnect closes the WhatsApp connection
func (c *Client) Disconnect() {
	c.WAClient.Disconnect()
	c.SetReady(false)
}

// SendTextMessage sends a text message
func (c *Client) SendTextMessage(ctx context.Context, to string, text string) error {
	jid, err := types.ParseJID(to)
	if err != nil {
		// Try parsing as phone number
		jid = types.NewJID(to, types.DefaultUserServer)
	}

	_, err = c.WAClient.SendMessage(ctx, jid, &waProto.Message{
		Conversation: &text,
	})
	return err
}

// Close cleans up resources
func (c *Client) Close() error {
	c.Disconnect()
	return c.Container.Close()
}

// Helper to check if file exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
