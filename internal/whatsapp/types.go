package whatsapp

import (
	"encoding/base64"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
)

// StatusUpdate represents a client status change event
type StatusUpdate struct {
	Client string `json:"client"`
	Ready  bool   `json:"ready"`
	Error  string `json:"error,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// QRImageEvent represents a QR code image event
type QRImageEvent struct {
	Client string `json:"client"`
	URL    string `json:"url"`
}

// NewMessageEvent represents an incoming message event
type NewMessageEvent struct {
	Client    string `json:"client"`
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Body      string `json:"body"`
	Timestamp int64  `json:"timestamp"`
	FromMe    bool   `json:"fromMe"`
	ChatID    string `json:"chatId"`
	ChatName  string `json:"chatName"`
	HasMedia  bool   `json:"hasMedia"`
	Type      string `json:"type"`
}

// Helper function to encode bytes to base64
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// Helper function to decode base64 to bytes
func decodeBase64(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}

// waProto is imported for message creation
var _ = waProto.Message{}
