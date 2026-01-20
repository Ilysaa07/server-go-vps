package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/types"
)

// SyncContacts handles POST /sync-contacts
// Fetches contacts from Number B (Leads) filtered by "Leads for Web" label
func (h *Handler) SyncContacts(c *gin.Context) {
	// Auto-Start 'leads' client logic
	clientID := "leads"
	client, exists := h.WAManager.GetClient(clientID)

	if !exists {
		// Create and Connect
		err := h.WAManager.CreateClient(context.Background(), clientID, "session-leads.db")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
			return
		}
		_ = h.WAManager.SetupEventHandlers(clientID)
		
		go func() {
			_ = h.WAManager.Connect(context.Background(), clientID)
		}()

		c.JSON(http.StatusOK, gin.H{
			"success":    false,
			"requiresQR": true,
			"message":    "Session started. Please scan QR code.",
		})
		return
	}

	if !client.IsReady() {
		c.JSON(http.StatusOK, gin.H{
			"success":    false,
			"requiresQR": true,
			"message":    "Session not ready. Please scan QR code.",
		})
		return
	}

	ctx := context.Background()

	// Sync app state to get latest labels (no QR reconnect needed!)
	// Labels can be in different app state patches - try multiple with fullSync=true
	fmt.Printf("🏷️ [leads] Fetching app state for labels (fullSync=true)...\n")
	
	// Check if we already have labels - only do fullSync if empty
	needsFullSync := len(h.WAManager.LabelStore.GetAllLabels()) == 0
	
	// Try Regular patch (most label data is here)
	if err := client.WAClient.FetchAppState(ctx, appstate.WAPatchRegular, needsFullSync, false); err != nil {
		fmt.Printf("⚠️ Failed to fetch WAPatchRegular: %v\n", err)
	}
	
	// Try RegularLow patch
	if err := client.WAClient.FetchAppState(ctx, appstate.WAPatchRegularLow, needsFullSync, false); err != nil {
		fmt.Printf("⚠️ Failed to fetch WAPatchRegularLow: %v\n", err)
	}
	
	// Try RegularHigh patch (label associations might be here)
	if err := client.WAClient.FetchAppState(ctx, appstate.WAPatchRegularHigh, needsFullSync, false); err != nil {
		fmt.Printf("⚠️ Failed to fetch WAPatchRegularHigh: %v\n", err)
	}
	
	fmt.Printf("🏷️ [leads] App state fetch completed. Labels in store: %d\n", len(h.WAManager.LabelStore.GetAllLabels()))

	// Get all contacts first
	contacts, err := client.WAClient.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to fetch contacts"})
		return
	}

	// Get JIDs that have "Leads for Web" label
	targetLabel := "Leads for Web"
	labeledJIDs := h.WAManager.LabelStore.GetJIDsForLabelName(targetLabel)
	labeledSet := make(map[string]bool)
	for _, jid := range labeledJIDs {
		labeledSet[jid] = true
	}

	// Log label store status for debugging
	allLabels := h.WAManager.LabelStore.GetAllLabels()
	fmt.Printf("🏷️ Available labels in store: %v\n", allLabels)
	fmt.Printf("🏷️ JIDs with '%s' label: %d\n", targetLabel, len(labeledJIDs))

	result := make([]map[string]interface{}, 0)
	filtered := len(labeledSet) > 0 // Define filtered for use in response
	
	// Better filtering strategy:
	if filtered {
		// STRATEGY A: If we have labeled leads, iterate through THEM
		fmt.Printf("🏷️ Filtering mode: Iterating through %d labeled JIDs\n", len(labeledJIDs))
		
		for _, targetJID := range labeledJIDs {
			// Construct JID object
			jid, _ := types.ParseJID(targetJID + "@s.whatsapp.net") // adjust suffix if needed
			
			// Try to find contact info
			contact, err := client.WAClient.Store.Contacts.GetContact(ctx, jid)
			
			name := targetJID // Default to number
			if err == nil && contact.Found {
				if contact.FullName != "" {
					name = contact.FullName
				} else if contact.PushName != "" {
					name = contact.PushName
				}
			}
			
			// Formatted number
			formattedID := strings.Replace(targetJID, "@s.whatsapp.net", "", -1)

			result = append(result, map[string]interface{}{
				"id":   formattedID,
				"name": name,
				"type": "user",
			})
		}
	} else {
		// STRATEGY B: Fallback to iterating all contacts if no label found (or filtering disabled)
		fmt.Printf("🏷️ Filtering mode: Iterating through all %d contacts\n", len(contacts))
		
		for jid, contact := range contacts {
			if contact.FullName == "" && contact.PushName == "" {
				continue
			}
			name := contact.FullName
			if name == "" {
				name = contact.PushName
			}

			// Clean up ID
			formattedID := strings.Replace(jid.User, "@s.whatsapp.net", "", -1)

			result = append(result, map[string]interface{}{
				"id":   formattedID,
				"name": name,
				"type": "user",
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"count":         len(result),
		"contacts":      result,
		"filtered":      filtered,
		"targetLabel":   targetLabel,
		"labelsInStore": len(allLabels),
	})
}

// StartLeadsClient handles POST /start-leads-client
func (h *Handler) StartLeadsClient(c *gin.Context) {
	clientID := "leads"
	
	// Check if already exists
	if client, exists := h.WAManager.GetClient(clientID); exists {
		if client.IsReady() {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"message": "Leads client is already running and ready",
			})
			return
		}
		// If exists but not ready, try to connect again logic could go here
		// For now assume if exists we just let it be or user should stop first
	}

	// Create client
	// Use a separate DB for leads session
	err := h.WAManager.CreateClient(context.Background(), clientID, "session-leads.db")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to create leads client: " + err.Error(),
		})
		return
	}

	// Setup events
	err = h.WAManager.SetupEventHandlers(clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to setup event handlers: " + err.Error(),
		})
		return
	}

	// Connect in background
	go func() {
		// Use background context for long running connection
		err := h.WAManager.Connect(context.Background(), clientID)
		if err != nil {
			// Log error (can't write to response anymore)
			fmt.Printf("❌ Failed to connect leads client: %v\n", err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Leads client starting... please scan QR code",
	})
}

// StopLeadsClient handles POST /stop-leads-client
func (h *Handler) StopLeadsClient(c *gin.Context) {
	clientID := "leads"
	
	err := h.WAManager.DestroyClient(clientID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to stop leads client: " + err.Error(),
		})
		return
	}

	// Remove DB file to ensure fresh start next time (optional, maybe keep for persistence)
	// For "on-demand" usually we want persistence so don't delete DB
	// os.Remove("session-leads.db")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Leads client stopped successfully",
	})
}
