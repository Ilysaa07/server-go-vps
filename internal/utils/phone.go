package utils

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

var nonDigitRegex = regexp.MustCompile(`\D`)

// FormatPhoneNumber formats a phone number to WhatsApp format (628xxx)
func FormatPhoneNumber(number string) string {
	// Remove all non-digits
	phone := nonDigitRegex.ReplaceAllString(number, "")

	// Convert 08xx to 628xx
	if strings.HasPrefix(phone, "0") {
		phone = "62" + phone[1:]
	}

	// Add 62 if starts with 8
	if strings.HasPrefix(phone, "8") {
		phone = "62" + phone
	}

	return phone
}

// PhoneToJID converts a phone number to WhatsApp JID
func PhoneToJID(number string) types.JID {
	phone := FormatPhoneNumber(number)
	return types.NewJID(phone, types.DefaultUserServer)
}

// JIDToPhoneNumber extracts the user (number) part from a JID
func JIDToPhoneNumber(jid types.JID) string {
	return jid.User
}

// FormatPhoneForDisplay formats phone for display (08xxx)
func FormatPhoneForDisplay(number string) string {
	phone := FormatPhoneNumber(number)
	if strings.HasPrefix(phone, "62") {
		return "0" + phone[2:]
	}
	return phone
}

// NormalizeNewlines converts all newline types to LF
func NormalizeNewlines(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// Truncate shortens a string to maxLen characters
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// GetExtensionFromMimetype returns file extension for a MIME type
func GetExtensionFromMimetype(mimetype string) string {
	extensions := map[string]string{
		"application/pdf":  ".pdf",
		"image/jpeg":       ".jpg",
		"image/png":        ".png",
		"image/gif":        ".gif",
		"image/webp":       ".webp",
		"video/mp4":        ".mp4",
		"audio/mpeg":       ".mp3",
		"audio/ogg":        ".ogg",
		"application/zip":  ".zip",
		"application/msword": ".doc",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
		"application/vnd.ms-excel": ".xls",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": ".xlsx",
	}

	if ext, ok := extensions[mimetype]; ok {
		return ext
	}

	// Try to extract from mimetype (e.g., "image/png" -> ".png")
	parts := strings.Split(mimetype, "/")
	if len(parts) == 2 {
		return "." + parts[1]
	}

	return ""
}


// ResolveLIDToPhoneNumber resolves a LID (Logical ID) to a corresponding Phone Number
// by looking it up in the local client store.
//
// Input: types.JID (LID) or string.
// Output: Phone number string (e.g. "62812xxx") or error.
//
// NOTE: This relies on the local store having the contact.
// Standard whatsmeow ContactInfo does NOT explicitly link LID -> Phone JID.
// If the store returns a contact for the LID, we confirm it exists.
// However, extracting the *Phone Number* from an LID-keyed ContactInfo is not standard
// without a 'Devices' list or using GetUserInfo (Network).
// This function assumes that if the contact is found, the caller determines how to proceed,
// or that the specific store implementation potentially maps it (unlikely in standard sqlite).
// Ideally, use Client.GetUserInfo(LID) for accurate resolution.
func ResolveLIDToPhoneNumber(client *whatsmeow.Client, input interface{}) (string, error) {
	if client == nil || client.Store == nil {
		return "", errors.New("client or client store is nil")
	}

	var targetJID types.JID
	var err error

	// 1. Parse Input
	switch v := input.(type) {
	case types.JID:
		targetJID = v
	case string:
		// Clean string first
		if !strings.Contains(v, "@") {
			// Check if it looks like a phone number (digits)
			if nonDigitRegex.MatchString(v) {
				// It has non-digits? No, replace removes non-digits.
				// If strictly digits, treat as phone JID.
				// If UUID-like, treat as LID? 
				// Let's assume input string without @ is a phone number or partial JID user
				targetJID = types.NewJID(v, types.DefaultUserServer)
			} else {
				// Maybe a UUID string? logic for LID
				targetJID = types.NewJID(v, types.DefaultUserServer) // Default to s.whatsapp.net
			}
		} else {
			targetJID, err = types.ParseJID(v)
			if err != nil {
				return "", fmt.Errorf("invalid JID string: %w", err)
			}
		}
	default:
		return "", errors.New("input must be types.JID or string")
	}

	// 2. Optimization: If already a Phone JID, return the User
	if targetJID.Server == types.DefaultUserServer {
		return targetJID.User, nil
	}

	// 3. Check Global Cache first (In-Memory/File persistent)
	if GlobalLIDCache != nil {
		if phone, found := GlobalLIDCache.Get(targetJID.User); found {
			return phone, nil
		}
	}

	// 4. Fallback: Check local store (Validation only)
	// We can't reverse lookup phone numbers easily from LIDs in standard store without iterating everything
	// and even then, ContactInfo struct might not have the link. 
	// So we just verify the LID exists as a contact.
	
	contact, err := client.Store.Contacts.GetContact(context.Background(), targetJID)
	if err != nil {
		return "", fmt.Errorf("store lookup error for %s: %w", targetJID, err)
	}

	if !contact.Found {
		return "", fmt.Errorf("contact not found in local store for JID: %s", targetJID)
	}

	// Return error indicating we know it exists but can't resolve it locally
	// failing gracefully to return the LID itself is handled by caller usually,
	// but here we return error to be strict, or just the LID user?
	// The caller leads.go handles error by using displayID = lidJID.User
	
	return "", fmt.Errorf("LID %s exists but phone number not resolved in cache", targetJID.User)
}
