package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware configures CORS for specified domains
func CORSMiddleware(allowedDomains []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// Check if origin is allowed
		allowed := false
		for _, domain := range allowedDomains {
			if domain == "*" || origin == domain {
				allowed = true
				break
			}
		}


		// Allow requests with no origin (server-to-server, curl, etc.)
		if origin == "" {
			allowed = true
		}

		if !allowed {
			fmt.Printf("⚠️ CORS Blocked: Origin='%s' not in %v\n", origin, allowedDomains)
		} else {
			c.Header("Access-Control-Allow-Origin", origin)
		}

		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Headers", "Content-Type, x-api-key, Origin, Referer, Authorization")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")

		// Handle preflight
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// SecurityMiddleware checks API key and origin/referer
func SecurityMiddleware(apiKey string, allowedDomains []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Skip for health check endpoints
		if path == "/" || path == "/status" {
			c.Next()
			return
		}

		// Skip OPTIONS (preflight)
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		origin := c.GetHeader("Origin")
		referer := c.GetHeader("Referer")
		reqAPIKey := c.GetHeader("x-api-key")

		// Check API key
		isValidAPIKey := apiKey != "" && reqAPIKey == apiKey

		// Check origin
		isAllowedOrigin := false
		for _, domain := range allowedDomains {
			if domain == "*" || origin == domain {
				isAllowedOrigin = true
				break
			}
		}

		// Check referer
		isAllowedReferer := false
		for _, domain := range allowedDomains {
			if strings.HasPrefix(referer, domain) {
				isAllowedReferer = true
				break
			}
		}

		// Allow if any check passes
		if isValidAPIKey || isAllowedOrigin || isAllowedReferer {
			c.Next()
			return
		}

		// Block unauthorized access
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
	}
}

// APIKeyRequired requires a valid API key for the endpoint
func APIKeyRequired(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if apiKey == "" {
			// No API key configured, allow through
			c.Next()
			return
		}

		// Check header first, then query parameter (for EventSource/SSE)
		reqAPIKey := c.GetHeader("x-api-key")
		if reqAPIKey == "" {
			reqAPIKey = c.Query("api_key")
		}
		
		if reqAPIKey != apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Invalid API Key"})
			return
		}

		c.Next()
	}
}
