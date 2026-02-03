package utils

import (
	"fmt"
	"strings"
	"time"
)

// RetryConfig configures retry behavior
type RetryConfig struct {
	MaxRetries int
	Delay      time.Duration
	Retryable  func(error) bool
}

// DefaultRetryConfig returns default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		Delay:      2 * time.Second,
		Retryable: func(err error) bool {
			// Default: retry on any error
			return true
		},
	}
}

// WithRetry executes a function with retry logic
func WithRetry[T any](fn func() (T, error), config RetryConfig) (T, error) {
	var lastErr error
	var zero T

	for i := 0; i < config.MaxRetries; i++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Check if error is retryable
		if config.Retryable != nil && !config.Retryable(err) {
			return zero, err
		}

		// Don't wait after last attempt
		if i < config.MaxRetries-1 {
			fmt.Printf("[RETRY] Attempt %d failed, retrying in %v...\n", i+1, config.Delay)
			time.Sleep(config.Delay)
		}
	}

	return zero, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// IsRetryableError checks if an error should be retried
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()
	retryablePatterns := []string{
		"context deadline exceeded",
		"connection reset",
		"connection refused",
		"timeout",
		"temporary failure",
		"unavailable",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(strings.ToLower(errMsg), pattern) {
			return true
		}
	}

	return false
}

// HumanizeDelay adds a random human-like delay
func HumanizeDelay(minMs, maxMs int) {
	delay := time.Duration(minMs) * time.Millisecond
	if maxMs > minMs {
		// Add random component
		delay += time.Duration(time.Now().UnixNano()%int64(maxMs-minMs)) * time.Millisecond
	}
	time.Sleep(delay)
}
