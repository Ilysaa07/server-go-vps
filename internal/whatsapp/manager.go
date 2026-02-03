package whatsapp

import (
	"context"
	"fmt"
	"sync"
	"wa-server-go/internal/firestore"
)

// Manager manages multiple WhatsApp clients
type Manager struct {
	clients    map[string]*Client
	Repo       *firestore.ChatsRepository
	LabelStore *LabelStore
	mu         sync.RWMutex
	qrChan     chan QRImageEvent
	statusChan chan StatusUpdate
	msgChan    chan NewMessageEvent
}

// NewManager creates a new client manager
func NewManager(repo *firestore.ChatsRepository) *Manager {
	return &Manager{
		clients:    make(map[string]*Client),
		Repo:       repo,
		LabelStore: NewLabelStore(),
		qrChan:     make(chan QRImageEvent, 10),
		statusChan: make(chan StatusUpdate, 10),
		msgChan:    make(chan NewMessageEvent, 100),
	}
}

// CreateClient creates and registers a new WhatsApp client
func (m *Manager) CreateClient(ctx context.Context, clientID string, dbPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.clients[clientID]; exists {
		return fmt.Errorf("client %s already exists", clientID)
	}

	client, err := NewClient(ctx, clientID, dbPath)
	if err != nil {
		return err
	}

	m.clients[clientID] = client
	return nil
}

// GetClient returns a client by ID
func (m *Manager) GetClient(clientID string) (*Client, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, ok := m.clients[clientID]
	return client, ok
}

// IsReady checks if a client is ready
func (m *Manager) IsReady(clientID string) bool {
	client, ok := m.GetClient(clientID)
	if !ok {
		return false
	}
	return client.IsReady()
}

// Connect connects a client
func (m *Manager) Connect(ctx context.Context, clientID string) error {
	client, ok := m.GetClient(clientID)
	if !ok {
		return fmt.Errorf("client %s not found", clientID)
	}

	// Create per-client channels that forward to manager channels
	qrChan := make(chan string, 5)
	statusChan := make(chan StatusUpdate, 5)

	// Forward QR events
	go func() {
		for qr := range qrChan {
			m.qrChan <- QRImageEvent{Client: clientID, URL: qr}
		}
	}()

	// Connect client
	err := client.Connect(ctx, qrChan, statusChan)
	if err != nil {
		m.statusChan <- StatusUpdate{Client: clientID, Ready: false, Error: err.Error()}
		return err
	}

	return nil
}

// SetupEventHandlers sets up event handlers for a client
func (m *Manager) SetupEventHandlers(clientID string) error {
	client, ok := m.GetClient(clientID)
	if !ok {
		return fmt.Errorf("client %s not found", clientID)
	}

	client.WAClient.AddEventHandler(func(evt interface{}) {
		m.handleEvent(clientID, client, evt)
	})

	return nil
}

// Disconnect disconnects a client
func (m *Manager) Disconnect(clientID string) error {
	client, ok := m.GetClient(clientID)
	if !ok {
		return fmt.Errorf("client %s not found", clientID)
	}
	client.Disconnect()
	m.statusChan <- StatusUpdate{Client: clientID, Ready: false, Reason: "disconnected"}
	return nil
}

// DestroyClient disconnects and removes a client
func (m *Manager) DestroyClient(clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, ok := m.clients[clientID]
	if !ok {
		return fmt.Errorf("client %s not found", clientID)
	}

	client.Close()
	delete(m.clients, clientID)
	return nil
}

// QRChannel returns the channel for QR events
func (m *Manager) QRChannel() <-chan QRImageEvent {
	return m.qrChan
}

// StatusChannel returns the channel for status events
func (m *Manager) StatusChannel() <-chan StatusUpdate {
	return m.statusChan
}

// MessageChannel returns the channel for message events
func (m *Manager) MessageChannel() <-chan NewMessageEvent {
	return m.msgChan
}

// BroadcastMessage allows external packages to broadcast messages via WebSocket
func (m *Manager) BroadcastMessage(evt NewMessageEvent) {
	select {
	case m.msgChan <- evt:
	default:
		fmt.Println("⚠️ Message channel full, dropping broadcast")
	}
}

// GetAllStatus returns status of all clients
func (m *Manager) GetAllStatus() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]interface{})
	for id, client := range m.clients {
		status[id] = map[string]interface{}{
			"ready": client.IsReady(),
			"qr":    client.GetQRCode(),
		}
	}
	return status
}

// Close cleans up all clients
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, client := range m.clients {
		client.Close()
	}
	m.clients = make(map[string]*Client)

	close(m.qrChan)
	close(m.statusChan)
	close(m.msgChan)
}
