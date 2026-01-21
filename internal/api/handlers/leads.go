package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
		
		// Step 1: Separate LIDs and regular JIDs
		var lidJIDs []types.JID
		var regularJIDs []types.JID
		
		for _, targetJID := range labeledJIDs {
			jid, err := types.ParseJID(targetJID)
			if err != nil {
				jid, _ = types.ParseJID(targetJID + "@s.whatsapp.net")
			}
			
			if jid.Server == "lid" {
				lidJIDs = append(lidJIDs, jid)
			} else {
				regularJIDs = append(regularJIDs, jid)
			}
		}
		
		fmt.Printf("🏷️ Found %d LIDs and %d regular JIDs\n", len(lidJIDs), len(regularJIDs))
		
		// Step 2: Batch resolve LIDs (max 3 per batch to avoid rate limit)
		lidToPhoneMap := make(map[string]string) // LID -> Phone number
		lidToNameMap := make(map[string]string)  // LID -> Name
		batchSize := 3
		delayBetweenBatches := 5 * time.Second
		
		for i := 0; i < len(lidJIDs); i += batchSize {
			end := i + batchSize
			if end > len(lidJIDs) {
				end = len(lidJIDs)
			}
			batch := lidJIDs[i:end]
			
			fmt.Printf("🔄 Processing LID batch %d-%d of %d...\n", i+1, end, len(lidJIDs))
			
			// Retry logic with exponential backoff
			maxRetries := 3
			retryDelay := 3 * time.Second
			
			for retry := 0; retry < maxRetries; retry++ {
				resp, err := client.WAClient.GetUserInfo(ctx, batch)
				if err != nil {
					if strings.Contains(err.Error(), "rate-overlimit") || strings.Contains(err.Error(), "429") {
						fmt.Printf("⚠️ Rate limited, waiting %v before retry %d/%d...\n", retryDelay, retry+1, maxRetries)
						time.Sleep(retryDelay)
						retryDelay *= 2 // Exponential backoff
						continue
					}
					fmt.Printf("❌ GetUserInfo batch failed: %v\n", err)
					break
				}
				
				// Process successful responses
				for lidJID, info := range resp {
					// Look for phone JID in Devices list
					for _, device := range info.Devices {
						if device.Server == "s.whatsapp.net" {
							lidToPhoneMap[lidJID.String()] = device.User
							fmt.Printf("   ✅ LID %s -> Phone %s\n", lidJID.User, device.User)
							
							// Try to get contact name from phone JID
							if phContact, err := client.WAClient.Store.Contacts.GetContact(ctx, device); err == nil && phContact.Found {
								if phContact.FullName != "" {
									lidToNameMap[lidJID.String()] = phContact.FullName
								} else if phContact.PushName != "" {
									lidToNameMap[lidJID.String()] = phContact.PushName
								} else if phContact.BusinessName != "" {
									lidToNameMap[lidJID.String()] = phContact.BusinessName
								}
							}
							break
						}
					}
					
					// If no phone found in devices, check if we got a VerifiedName
					if _, exists := lidToPhoneMap[lidJID.String()]; !exists {
						if info.VerifiedName != nil && info.VerifiedName.Details != nil {
							lidToNameMap[lidJID.String()] = info.VerifiedName.Details.GetVerifiedName()
							fmt.Printf("   📛 LID %s has verified name: %s\n", lidJID.User, info.VerifiedName.Details.GetVerifiedName())
						}
					}
				}
				break // Success, exit retry loop
			}
			
			// Wait before next batch to avoid rate limiting
			if end < len(lidJIDs) {
				fmt.Printf("⏳ Waiting %v before next batch...\n", delayBetweenBatches)
				time.Sleep(delayBetweenBatches)
			}
		}
		
		// Step 3: Build result with resolved data
		for _, targetJID := range labeledJIDs {
			jid, err := types.ParseJID(targetJID)
			if err != nil {
				jid, _ = types.ParseJID(targetJID + "@s.whatsapp.net")
			}
			
			name := jid.User
			displayID := jid.User
			
			// Try to get contact info first
			contact, err := client.WAClient.Store.Contacts.GetContact(ctx, jid)
			if err == nil && contact.Found {
				if contact.FullName != "" {
					name = contact.FullName
				} else if contact.PushName != "" {
					name = contact.PushName
				} else if contact.BusinessName != "" {
					name = contact.BusinessName
				}
			}
			
			// For LIDs, use resolved phone number
			if jid.Server == "lid" {
				if phone, ok := lidToPhoneMap[jid.String()]; ok {
					displayID = phone
				}
				if resolvedName, ok := lidToNameMap[jid.String()]; ok && resolvedName != "" {
					name = resolvedName
				}
				// If still no name, use phone or LID as fallback
				if name == jid.User && displayID != jid.User {
					name = displayID // Use phone number as name if no name found
				}
			} else {
				displayID = strings.Replace(displayID, "@s.whatsapp.net", "", -1)
			}
			
			// Only add if we have a valid phone number (skip unresolved LIDs)
			if jid.Server == "lid" && displayID == jid.User {
				fmt.Printf("⏭️ Skipping unresolved LID: %s\n", jid.User)
				continue
			}

			result = append(result, map[string]interface{}{
				"id":    displayID,
				"name":  name,
				"phone": displayID,
				"type":  "user",
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
				"id":    formattedID,
				"name":  name,
				"phone": formattedID, // Added for frontend compatibility
				"type":  "user",
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

// SyncContactsStream handles GET /sync-contacts-stream
// Uses Server-Sent Events to stream contacts progressively
func (h *Handler) SyncContactsStream(c *gin.Context) {
	fmt.Println("🚀 [SSE] SyncContactsStream started")
	
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", c.GetHeader("Origin"))
	c.Header("X-Accel-Buffering", "no") // For nginx

	// Helper function to send SSE event
	sendEvent := func(eventType string, data interface{}) {
		jsonData, _ := json.Marshal(data)
		fmt.Printf("📤 [SSE] Sending event: %s\n", eventType)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, jsonData)
		c.Writer.Flush()
	}

	// Auto-Start 'leads' client logic
	clientID := "leads"
	client, exists := h.WAManager.GetClient(clientID)

	if !exists {
		// Create and Connect
		err := h.WAManager.CreateClient(context.Background(), clientID, "session-leads.db")
		if err != nil {
			sendEvent("error", gin.H{"error": err.Error()})
			return
		}
		_ = h.WAManager.SetupEventHandlers(clientID)

		go func() {
			_ = h.WAManager.Connect(context.Background(), clientID)
		}()

		sendEvent("status", gin.H{"status": "requiresQR", "message": "Session started. Please scan QR code."})
		return
	}

	if !client.IsReady() {
		sendEvent("status", gin.H{"status": "requiresQR", "message": "Session not ready. Please scan QR code."})
		return
	}

	ctx := context.Background()

	sendEvent("status", gin.H{"status": "syncing", "message": "Fetching labels..."})

	// Sync app state to get latest labels
	needsFullSync := len(h.WAManager.LabelStore.GetAllLabels()) == 0

	if err := client.WAClient.FetchAppState(ctx, appstate.WAPatchRegular, needsFullSync, false); err != nil {
		fmt.Printf("⚠️ Failed to fetch WAPatchRegular: %v\n", err)
	}
	if err := client.WAClient.FetchAppState(ctx, appstate.WAPatchRegularLow, needsFullSync, false); err != nil {
		fmt.Printf("⚠️ Failed to fetch WAPatchRegularLow: %v\n", err)
	}
	if err := client.WAClient.FetchAppState(ctx, appstate.WAPatchRegularHigh, needsFullSync, false); err != nil {
		fmt.Printf("⚠️ Failed to fetch WAPatchRegularHigh: %v\n", err)
	}

	// Get JIDs that have "Leads for Web" label
	targetLabel := "Leads for Web"
	labeledJIDs := h.WAManager.LabelStore.GetJIDsForLabelName(targetLabel)

	sendEvent("status", gin.H{
		"status":  "processing",
		"message": fmt.Sprintf("Found %d contacts with '%s' label", len(labeledJIDs), targetLabel),
		"total":   len(labeledJIDs),
	})

	if len(labeledJIDs) == 0 {
		sendEvent("complete", gin.H{"total": 0, "message": "No contacts found with label"})
		return
	}

	// Separate LIDs and regular JIDs
	var lidJIDs []types.JID
	var regularJIDs []types.JID

	for _, targetJID := range labeledJIDs {
		jid, err := types.ParseJID(targetJID)
		if err != nil {
			jid, _ = types.ParseJID(targetJID + "@s.whatsapp.net")
		}

		if jid.Server == "lid" {
			lidJIDs = append(lidJIDs, jid)
		} else {
			regularJIDs = append(regularJIDs, jid)
		}
	}

	// Process regular JIDs first (no rate limiting needed)
	processedCount := 0
	for _, jid := range regularJIDs {
		contact, _ := client.WAClient.Store.Contacts.GetContact(ctx, jid)
		name := jid.User
		if contact.Found {
			if contact.FullName != "" {
				name = contact.FullName
			} else if contact.PushName != "" {
				name = contact.PushName
			}
		}

		displayID := strings.Replace(jid.User, "@s.whatsapp.net", "", -1)

		sendEvent("contact", gin.H{
			"id":    displayID,
			"name":  name,
			"phone": displayID,
			"type":  "user",
		})
		processedCount++
	}

	// Process LIDs in batches with rate limiting
	batchSize := 10  // Increased for faster processing
	delayBetweenBatches := 2 * time.Second  // Reduced delay, rely on retry for rate limits

	for i := 0; i < len(lidJIDs); i += batchSize {
		end := i + batchSize
		if end > len(lidJIDs) {
			end = len(lidJIDs)
		}
		batch := lidJIDs[i:end]

		sendEvent("progress", gin.H{
			"current": processedCount,
			"total":   len(labeledJIDs),
			"message": fmt.Sprintf("Processing batch %d-%d", i+1, end),
		})

		// Retry logic with exponential backoff
		maxRetries := 3
		retryDelay := 3 * time.Second

		for retry := 0; retry < maxRetries; retry++ {
			resp, err := client.WAClient.GetUserInfo(ctx, batch)
			if err != nil {
				if strings.Contains(err.Error(), "rate-overlimit") || strings.Contains(err.Error(), "429") {
					sendEvent("progress", gin.H{"message": fmt.Sprintf("Rate limited, waiting %v...", retryDelay)})
					time.Sleep(retryDelay)
					retryDelay *= 2
					continue
				}
				fmt.Printf("❌ GetUserInfo batch failed: %v\n", err)
				break
			}

			// Process successful responses
			for lidJID, info := range resp {
				phone := ""
				for _, device := range info.Devices {
					if device.Server == "s.whatsapp.net" {
						phone = device.User
						break
					}
				}

				if phone == "" {
					continue // Skip unresolved LIDs
				}

				// Try to get name
				name := phone
				if phContact, err := client.WAClient.Store.Contacts.GetContact(ctx, types.JID{User: phone, Server: "s.whatsapp.net"}); err == nil && phContact.Found {
					if phContact.FullName != "" {
						name = phContact.FullName
					} else if phContact.PushName != "" {
						name = phContact.PushName
					} else if phContact.BusinessName != "" {
						name = phContact.BusinessName
					}
				}

				sendEvent("contact", gin.H{
					"id":    phone,
					"name":  name,
					"phone": phone,
					"type":  "user",
					"lidResolved": lidJID.User,
				})
				processedCount++
			}
			break // Success, exit retry loop
		}

		// Wait before next batch to avoid rate limiting
		if end < len(lidJIDs) {
			time.Sleep(delayBetweenBatches)
		}
	}

	sendEvent("complete", gin.H{
		"total":   processedCount,
		"message": fmt.Sprintf("Sync completed. %d contacts synced.", processedCount),
	})
}
