package main

import (
	"context"
	"fmt"
	"log"

	"wa-server-go/internal/config"
	"wa-server-go/internal/firestore"
)

func main() {
	fmt.Println("🔥 Testing Firebase Connection...")
	fmt.Println("=====================================\n")

	// Load configuration
	cfg := config.Load()

	fmt.Printf("📋 Project ID: %s\n", cfg.FirebaseProjectID)
	fmt.Printf("📄 Credentials: %s\n\n", cfg.GoogleCredentials)

	// Create context
	ctx := context.Background()

	// Initialize Firestore
	fmt.Println("🔌 Connecting to Firestore...")
	fsClient, err := firestore.NewClient(ctx, cfg.GoogleCredentials, cfg.FirebaseProjectID)
	if err != nil {
		log.Fatalf("❌ Failed to connect: %v", err)
	}
	defer fsClient.Close()

	fmt.Println("✅ Successfully connected to Firestore!\n")

	// Try to list some clients
	fmt.Println("👥 Fetching clients from Firestore...")
	
	iter := fsClient.FS.Collection("clients").Limit(5).Documents(ctx)
	defer iter.Stop()

	count := 0
	for {
		doc, err := iter.Next()
		if err != nil {
			break
		}
		count++
		data := doc.Data()
		fmt.Printf("  %d. %s (ID: %s)\n", count, data["name"], doc.Ref.ID)
	}

	if count == 0 {
		fmt.Println("  ⚠️ No clients found in database")
	} else {
		fmt.Printf("\n✅ Found %d clients in database\n", count)
	}

	fmt.Println("\n=====================================")
	fmt.Println("🎉 Firebase connection test completed!")
}
