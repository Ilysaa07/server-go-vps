package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"wa-server-go/internal/api"
	"wa-server-go/internal/config"
	"wa-server-go/internal/firestore"
	"wa-server-go/internal/whatsapp"
)

func main() {
	fmt.Println("\n🚀 Initializing WhatsApp Server (Go)...")
	fmt.Println("=========================================")
	fmt.Printf("📌 Running as Go/whatsmeow (socket-based)\n")
	fmt.Printf("📌 Session: SQLite (local)\n")
	fmt.Printf("📌 Business Data: Firestore\n")
	fmt.Println("=========================================\n")

	// Load configuration
	cfg := config.Load()

	// Create context for app lifecycle
	ctx := context.Background()

	// Initialize Firestore
	fsClient, err := firestore.NewClient(ctx, cfg.GoogleCredentials, cfg.FirebaseProjectID)
	if err != nil {
		log.Fatalf("⚠️ Failed to initialize Firestore: %v", err)
	}
	defer fsClient.Close()

	var chatsRepo *firestore.ChatsRepository
	if fsClient != nil {
		chatsRepo = firestore.NewChatsRepository(fsClient)
	}

	// Create WhatsApp manager
	waManager := whatsapp.NewManager(chatsRepo)

	// Create bot client
	err = waManager.CreateClient(ctx, cfg.BotClientID, "session-bot.db")
	if err != nil {
		log.Fatalf("Failed to create bot client: %v", err)
	}

	// Set up event handlers
	err = waManager.SetupEventHandlers(cfg.BotClientID)
	if err != nil {
		log.Fatalf("Failed to setup event handlers: %v", err)
	}

	// Connect bot client
	go func() {
		err := waManager.Connect(ctx, cfg.BotClientID)
		if err != nil {
			log.Printf("❌ Failed to connect bot client: %v", err)
		}
	}()


	// Create and start HTTP server
	server := api.NewServer(cfg, waManager, chatsRepo)

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		fmt.Println("\n⚠️ Shutdown signal received...")
		waManager.Close()
		fmt.Println("✅ Cleanup complete. Goodbye!")
		os.Exit(0)
	}()

	// Start server
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
