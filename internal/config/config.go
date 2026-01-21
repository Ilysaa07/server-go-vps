package config

import (
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	// Server
	Port string

	// WhatsApp
	BotClientID   string
	LeadsClientID string

	// Security
	APIKey         string
	AllowedDomains []string

	// Firestore
	FirebaseProjectID string
	GoogleCredentials string

	// Features
	BackupPhone    string
	WebURL         string
	TargetLabelTag string

	// Blog Automator
	GroqAPIKey                 string
	PexelsAPIKey               string
	ContentfulManagementToken  string
	ContentfulSpaceID          string
	ContentfulEnvironment      string
}

// Load reads configuration from environment variables
func Load() *Config {
	// Load .env file if exists
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	cfg := &Config{
		// Server
		Port: getEnv("PORT", "3001"),

		// WhatsApp
		BotClientID:   getEnv("WA_BOT_CLIENT_ID", "bot"),
		LeadsClientID: getEnv("WA_LEADS_CLIENT_ID", "leads"),

		// Security
		APIKey:         getEnv("API_KEY", ""),
		AllowedDomains: parseAllowedDomains(getEnv("ALLOWED_DOMAINS", "http://localhost:3000,https://valprointertech.com,https://valprointertech.vercel.app")),

		// Firestore
		FirebaseProjectID: getEnv("FIREBASE_PROJECT_ID", ""),
		GoogleCredentials: getEnv("GOOGLE_APPLICATION_CREDENTIALS", ""),

		// Features
		BackupPhone:    getEnv("BACKUP_PHONE", ""),
		WebURL:         getEnv("WEB_URL", "https://valprointertech.com"),
		TargetLabelTag: getEnv("TARGET_LABEL_TAG", "leads_for_web"),

		// Blog Automator
		GroqAPIKey:                 getEnv("GROQ_API_KEY", ""),
		PexelsAPIKey:               getEnv("PEXELS_API_KEY", ""),
		ContentfulManagementToken:  getEnv("CONTENTFUL_MANAGEMENT_TOKEN", ""),
		ContentfulSpaceID:          getEnv("CONTENTFUL_SPACE_ID", ""),
		ContentfulEnvironment:      getEnv("CONTENTFUL_ENVIRONMENT", "master"),
	}

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func parseAllowedDomains(domainsStr string) []string {
	domains := strings.Split(domainsStr, ",")
	result := make([]string, 0, len(domains))
	for _, d := range domains {
		trimmed := strings.TrimSpace(d)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
