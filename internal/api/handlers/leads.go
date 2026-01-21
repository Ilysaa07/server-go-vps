package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"wa-server-go/internal/utils"

	"github.com/gin-gonic/gin"
	"go.mau.fi/whatsmeow"
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
			
			// Resolve LID to Phone Number using helper
			if jid.Server == "lid" {
				resolvedNum, err := utils.ResolveLIDToPhoneNumber(client.WAClient, jid)
				if err == nil && resolvedNum != "" {
					displayID = resolvedNum
					// If name is still user ID, try to use resolved phone as name
					if name == jid.User {
						name = displayID
					}
				}
			} else {
				displayID = strings.Replace(displayID, "@s.whatsapp.net", "", -1)
			}
			
			// Only add if we have a valid phone number or LID
			if jid.Server == "lid" && displayID == jid.User {
				// We allow LIDs now as they are valid for sending, but log it
				// fmt.Printf("ℹ️  Unresolved LID: %s\n", jid.User)
			}
			
			// Get Profile Picture (Sync version) - simplified for non-streaming
			var profilePicUrl string
			pic, _ := client.WAClient.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{Preview: true})
			if pic != nil {
				profilePicUrl = pic.URL
			}

			result = append(result, map[string]interface{}{
				"id":            displayID,
				"name":          name,
				"phone":         displayID,
				"type":          "user",
				"profilePicUrl": profilePicUrl,
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

	// Initialize Cache (Global)
	cachePath := "lid_mapping.json"
	utils.InitGlobalCache(cachePath)
	lidCache := utils.GlobalLIDCache // Use the singleton

	// Process LIDs
	fmt.Printf("🏷️ Processing %d LIDs (Smart Cache Mode)...\n", len(lidJIDs))
	
	// Separate Cached vs Uncached
	var uncachedLIDs []types.JID
	lidToResolvedID := make(map[string]string)
	var pendingPics []types.JID

	for _, lidJID := range lidJIDs {
		// Default to LID
		lidToResolvedID[lidJID.User] = lidJID.User
		
		// Check Cache
		if phone, found := lidCache.Get(lidJID.User); found {
			lidToResolvedID[lidJID.User] = phone
		} else {
			uncachedLIDs = append(uncachedLIDs, lidJID)
		}
		pendingPics = append(pendingPics, lidJID)
	}
	
	fmt.Printf("📦 Cached: %d, Needing Resolution: %d\n", len(lidJIDs)-len(uncachedLIDs), len(uncachedLIDs))

	// Batch process ONLY uncached LIDs
	if len(uncachedLIDs) > 0 {
		batchSize := 1 // Single item for safety
		networkDisabled := false // Circuit breaker
		
		for i := 0; i < len(uncachedLIDs); i += batchSize {
			if networkDisabled {
				break // Stop processing network requests
			}

			end := i + batchSize
			if end > len(uncachedLIDs) {
				end = len(uncachedLIDs)
			}
			batch := uncachedLIDs[i:end]
			
			// Try Network Resolution
			resp, err := client.WAClient.GetUserInfo(ctx, batch)
			if err != nil {
				if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate-overlimit") {
					fmt.Printf("⚠️ Rate Limit Hit (429). Circuit Breaker Activated. Switching to local-only for remaining %d LIDs.\n", len(uncachedLIDs)-i)
					networkDisabled = true
					continue
				}
				// Other network error - just log and continue (maybe wait a bit)
				fmt.Printf("⚠️ Network Error on batch: %v\n", err)
				time.Sleep(1 * time.Second) // Light delay
				continue
			}
				
			// Success
			for lidJID, info := range resp {
				for _, device := range info.Devices {
					if device.Server == "s.whatsapp.net" {
						// Update Map & Cache
						lidToResolvedID[lidJID.User] = device.User
						lidCache.Set(lidJID.User, device.User)
						fmt.Printf("   ✅ Resolved: %s -> %s\n", lidJID.User, device.User)
						break
					}
				}
			}
			
			// Respectful delay between successful requests
			time.Sleep(2 * time.Second)
		}
	}

	// Send Events
	for _, lidJID := range lidJIDs {
		// Get local name info
		contact, _ := client.WAClient.Store.Contacts.GetContact(ctx, lidJID)
		name := lidJID.User
		if contact.Found {
			if contact.PushName != "" {
				name = contact.PushName
			} else if contact.FullName != "" {
				name = contact.FullName
			} else if contact.BusinessName != "" {
				name = contact.BusinessName
			}
		}
		
		displayID := lidToResolvedID[lidJID.User]
		
		sendEvent("contact", gin.H{
			"id":            displayID,
			"name":          name,
			"phone":         displayID,
			"type":          "lid",
			"isLID":         true,
			"profilePicUrl": "", // Will be updated async
		})
		processedCount++
		
		if processedCount % 20 == 0 {
			sendEvent("progress", gin.H{
				"current": processedCount,
				"total":   len(labeledJIDs),
				"message": fmt.Sprintf("Processed %d/%d contacts", processedCount, len(labeledJIDs)),
			})
		}
	}

	sendEvent("complete", gin.H{
		"total":   processedCount,
		"message": fmt.Sprintf("Sync completed. %d contacts synced. Fetching profile pictures...", processedCount),
	})
	
	// Fetch profile pictures concurrently (but block complete signal)
	sendEvent("progress", gin.H{
		"message": fmt.Sprintf("Fetching profile pictures for %d contacts...", len(pendingPics)),
	})
	
	// Worker pool for profile pics
	workerCount := 5
	jobs := make(chan types.JID, len(pendingPics))
	var wg sync.WaitGroup
	
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for lidJID := range jobs {
				// Use a timeout context for each fetch
				// We can just use context.Background() as we manage timeout overall or per-req inside library?
				// Best to use a new context derived from existing if possible, or Background with timeout
				
				pic, err := client.WAClient.GetProfilePictureInfo(context.Background(), lidJID, &whatsmeow.GetProfilePictureParams{
					Preview: true,
				})
				
				if err == nil && pic != nil && pic.URL != "" {
					// Send update event (Thread safe - Gin writer might need lock if concurrent writes are issue?)
					// Gin context writer is NOT thread safe for concurrent writes.
					// We must synchronize calls to sendEvent if called from multiple goroutines?
					// Yes. But sendEvent helper uses c.Writer.SSEvent which writes to socket.
					// We need a mutex for sendEvent to be safe.
					// Since we can't easily modify sendEvent signature/scope here easily without refactor,
					// let's just collect results and send in main thread? 
					// NO, main thread is blocked waiting for WG.
					// We should add a mutex to this handler scope.
					
					// Simple fix: Use a "results" channel and process results in main thread.
					// That avoids concurrent write issues entirely. 
				}
			}
		}()
	}
	
	// Create results channel
	type PicResult struct {
		ID  string
		URL string
	}
	results := make(chan PicResult, len(pendingPics))
	
	// Re-spawn workers to write to results channel
	// Actually let's rewrite the worker part simpler:
	
	go func() {
		localWg := sync.WaitGroup{}
		semaphore := make(chan struct{}, 5) // Limit 5 concurrent requests
		
		for _, jid := range pendingPics {
			localWg.Add(1)
			go func(targetJID types.JID) {
				defer localWg.Done()
				semaphore <- struct{}{} // Acquire
				defer func() { <-semaphore }() // Release
				
				pic, err := client.WAClient.GetProfilePictureInfo(context.Background(), targetJID, &whatsmeow.GetProfilePictureParams{
					Preview: true,
				})
				if err == nil && pic != nil && pic.URL != "" {
					results <- PicResult{ID: targetJID.User, URL: pic.URL}
				}
			}(jid)
		}
		localWg.Wait()
		close(results)
	}()
	
	// Process results in main thread (safe for c.Writer)
	picCount := 0
	for res := range results {
		// Lookup the resolved ID to update the correct row on frontend
		mappedID := res.ID
		if resolved, ok := lidToResolvedID[res.ID]; ok {
			mappedID = resolved
		}

		sendEvent("contact-update", gin.H{
			"id":            mappedID,
			"profilePicUrl": res.URL,
		})
		picCount++
	}

	sendEvent("complete", gin.H{
		"total":   processedCount,
		"message": fmt.Sprintf("Sync completed. %d contacts, %d profile pics fetched.", processedCount, picCount),
	})
}
