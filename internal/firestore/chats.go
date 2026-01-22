package firestore

import (
	"context"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// WAChat represents a WhatsApp chat in Firestore
type WAChat struct {
	ID              string    `firestore:"-"`
	JID             string    `firestore:"jid"`
	Name            string    `firestore:"name"`
	Number          string    `firestore:"number"`
	IsGroup         bool      `firestore:"isGroup"`
	UnreadCount     int       `firestore:"unreadCount"`
	LastMessageBody string    `firestore:"lastMessageBody,omitempty"`
	LastMessageAt   time.Time `firestore:"lastMessageAt,omitempty"`
	ProfilePicURL   string    `firestore:"profilePicUrl,omitempty"`
	HasInvoice      bool      `firestore:"hasInvoice,omitempty"`
	UpdatedAt       time.Time `firestore:"updatedAt"`
}

// WAMessage represents a WhatsApp message in Firestore
type WAMessage struct {
	ID        string    `firestore:"-"`
	MessageID string    `firestore:"messageId"`
	ChatID    string    `firestore:"chatId"`
	From      string    `firestore:"from"`
	To        string    `firestore:"to"`
	Body      string    `firestore:"body"`
	Timestamp time.Time `firestore:"timestamp"`
	FromMe    bool      `firestore:"fromMe"`
	HasMedia  bool      `firestore:"hasMedia"`
	MediaType string    `firestore:"mediaType,omitempty"`
	MediaURL  string    `firestore:"mediaUrl,omitempty"`
	Type      string    `firestore:"type"` // text, image, document, audio, video
	Ack       int       `firestore:"ack"`
	CreatedAt time.Time `firestore:"createdAt"`
}

// ChatsRepository provides access to the wa_chats and wa_messages collections
type ChatsRepository struct {
	client             *Client
	chatsCollection    string
	messagesCollection string
}

// NewChatsRepository creates a new chats repository
func NewChatsRepository(client *Client) *ChatsRepository {
	return &ChatsRepository{
		client:             client,
		chatsCollection:    "wa_chats_v3",
		messagesCollection: "wa_messages_v3",
	}
}

// GetRecentChats retrieves recent chats ordered by last message time
func (r *ChatsRepository) GetRecentChats(ctx context.Context, limit int) ([]WAChat, error) {
	query := r.client.Collection(r.chatsCollection).
		OrderBy("lastMessageAt", firestore.Desc)
	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)

	var chats []WAChat
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var chat WAChat
		if err := doc.DataTo(&chat); err != nil {
			continue
		}
		chat.ID = doc.Ref.ID
		chats = append(chats, chat)
	}

	return chats, nil
}

// GetChatMessages retrieves messages for a specific chat
func (r *ChatsRepository) GetChatMessages(ctx context.Context, chatID string, limit int) ([]WAMessage, error) {
	query := r.client.Collection(r.messagesCollection).
		Where("chatId", "==", chatID).
		OrderBy("timestamp", firestore.Desc)
	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)

	var messages []WAMessage
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var msg WAMessage
		if err := doc.DataTo(&msg); err != nil {
			continue
		}
		msg.ID = doc.Ref.ID
		messages = append(messages, msg)
	}

	return messages, nil
}

// SaveMessage saves a new message and updates the chat
func (r *ChatsRepository) SaveMessage(ctx context.Context, msg *WAMessage) error {
	now := time.Now()
	msg.CreatedAt = now

	// Save message (Idempotent: Use MessageID as Document ID)
	_, err := r.client.Collection(r.messagesCollection).Doc(msg.MessageID).Set(ctx, msg)
	if err != nil {
		return err
	}

	// Update or create chat
	return r.updateChatFromMessage(ctx, msg)
}

// updateChatFromMessage updates chat info from a message
func (r *ChatsRepository) updateChatFromMessage(ctx context.Context, msg *WAMessage) error {
	// Find existing chat
	iter := r.client.Collection(r.chatsCollection).
		Where("jid", "==", msg.ChatID).
		Limit(1).
		Documents(ctx)

	doc, err := iter.Next()
	now := time.Now()

	if err == iterator.Done {
		// Create new chat
		newChat := WAChat{
			JID:             msg.ChatID,
			Number:          msg.From,
			IsGroup:         false, // Will be updated if needed
			UnreadCount:     0,
			LastMessageBody: truncateBody(msg.Body),
			LastMessageAt:   msg.Timestamp,
			UpdatedAt:       now,
			HasInvoice:      strings.Contains(strings.ToUpper(msg.Body), "INV-") || strings.Contains(strings.ToLower(msg.Body), "invoice") || strings.Contains(strings.ToLower(msg.Body), "tagihan"),
		}
		if msg.FromMe {
			newChat.Number = msg.To
		} else {
			newChat.UnreadCount = 1
		}
		_, _, err = r.client.Collection(r.chatsCollection).Add(ctx, newChat)
		return err
	}
	if err != nil {
		return err
	}

	// Update existing chat
	updates := []firestore.Update{
		{Path: "lastMessageBody", Value: truncateBody(msg.Body)},
		{Path: "lastMessageAt", Value: msg.Timestamp},
		{Path: "updatedAt", Value: now},
	}
	if !msg.FromMe {
		updates = append(updates, firestore.Update{Path: "unreadCount", Value: firestore.Increment(1)})
	}

	// Check for invoice keywords to auto-mark as relevant
	if strings.Contains(strings.ToUpper(msg.Body), "INV-") || strings.Contains(strings.ToLower(msg.Body), "invoice") || strings.Contains(strings.ToLower(msg.Body), "tagihan") {
		updates = append(updates, firestore.Update{Path: "hasInvoice", Value: true})
	}

	_, err = doc.Ref.Update(ctx, updates)
	return err
}

// MarkChatAsRead marks a chat as read
func (r *ChatsRepository) MarkChatAsRead(ctx context.Context, chatJID string) error {
	iter := r.client.Collection(r.chatsCollection).
		Where("jid", "==", chatJID).
		Limit(1).
		Documents(ctx)

	doc, err := iter.Next()
	if err != nil {
		return err
	}

	_, err = doc.Ref.Update(ctx, []firestore.Update{
		{Path: "unreadCount", Value: 0},
		{Path: "updatedAt", Value: time.Now()},
	})
	return err
}

// GetInvoiceChats retrieves chats that contain invoice messages
func (r *ChatsRepository) GetInvoiceChats(ctx context.Context, limit int) ([]WAChat, error) {
	query := r.client.Collection(r.chatsCollection).
		Where("hasInvoice", "==", true).
		OrderBy("lastMessageAt", firestore.Desc)
	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)

	var chats []WAChat
	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		var chat WAChat
		if err := doc.DataTo(&chat); err != nil {
			continue
		}
		chat.ID = doc.Ref.ID
		chats = append(chats, chat)
	}

	return chats, nil
}

// SetChatHasInvoice marks a chat as relevant to invoices
func (r *ChatsRepository) SetChatHasInvoice(ctx context.Context, jid string, hasInvoice bool) error {
	iter := r.client.Collection(r.chatsCollection).
		Where("jid", "==", jid).
		Limit(1).
		Documents(ctx)

	doc, err := iter.Next()
	// If chat doesn't exist, we should create it or ignore. 
	// Sending an invoice usually implies the chat exists or will be created by SaveMessage.
	// Since SendInvoice calls SaveMessage (async), we might race. 
	// But assuming SendInvoice calls this, it should be fine to just update if exists.
	if err == iterator.Done {
		return nil 
	}
	if err != nil {
		return err
	}

	_, err = doc.Ref.Update(ctx, []firestore.Update{
		{Path: "hasInvoice", Value: hasInvoice},
	})
	return err
}

// UpdateChatName updates the name of a chat
func (r *ChatsRepository) UpdateChatName(ctx context.Context, jid string, name string) error {
	iter := r.client.Collection(r.chatsCollection).
		Where("jid", "==", jid).
		Limit(1).
		Documents(ctx)

	doc, err := iter.Next()
	if err == iterator.Done {
		// Chat not found, maybe create it? Or ignore?
		// For now, ignore. It will be created when message is saved.
		// Or we can assume SaveMessage called before this.
		return nil
	}
	if err != nil {
		return err
	}

	_, err = doc.Ref.Update(ctx, []firestore.Update{
		{Path: "name", Value: name},
	})
	return err
}

// UpdateChatProfilePic updates the profile picture of a chat
func (r *ChatsRepository) UpdateChatProfilePic(ctx context.Context, jid string, url string) error {
	iter := r.client.Collection(r.chatsCollection).
		Where("jid", "==", jid).
		Limit(1).
		Documents(ctx)

	doc, err := iter.Next()
	if err != nil { // Handle Done and Error
		return err // if Done, err is iterator.Done, acceptable to return or check
	}

	_, err = doc.Ref.Update(ctx, []firestore.Update{
		{Path: "profilePicUrl", Value: url},
	})
	return err
}

func truncateBody(body string) string {
	const maxLen = 100
	if len(body) <= maxLen {
		return body
	}
	return body[:maxLen] + "..."
}
