package utils

import (
	"encoding/json"
	"os"
	"sync"
)

// LIDCache manages the persistent mapping of LIDs to Phone Numbers
type LIDCache struct {
	mu       sync.RWMutex
	FilePath string
	Mapping  map[string]string // LID (User) -> Phone (User)
}

// Global instance (optional, or can be instantiated per handler)
var GlobalLIDCache *LIDCache

func InitGlobalCache(path string) {
	if GlobalLIDCache != nil {
		return
	}
	GlobalLIDCache = NewLIDCache(path)
	GlobalLIDCache.Load()
}

func NewLIDCache(path string) *LIDCache {
	return &LIDCache{
		FilePath: path,
		Mapping:  make(map[string]string),
	}
}

// Load reads the cache from disk
func (c *LIDCache) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // New cache
		}
		return err
	}

	return json.Unmarshal(data, &c.Mapping)
}

// Save writes the cache to disk
func (c *LIDCache) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := json.MarshalIndent(c.Mapping, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(c.FilePath, data, 0644)
}

// Get retrieves a phone number for an LID
func (c *LIDCache) Get(lidUser string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.Mapping[lidUser]
	return val, ok
}

// Set stores a phone number for an LID and saves to disk
func (c *LIDCache) Set(lidUser, phoneUser string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Only update if changed to avoid unnecessary I/O
	if c.Mapping[lidUser] == phoneUser {
		return
	}
	
	c.Mapping[lidUser] = phoneUser
	
	// Save immediately (or could debounce in production)
	go func() {
		// Use a separate lock for saving or just save? 
		// Since Set calls are frequent, maybe save periodically?
		// For robustness, let's save immediately but handle error silently
		data, _ := json.MarshalIndent(c.Mapping, "", "  ")
		_ = os.WriteFile(c.FilePath, data, 0644)
	}()
}
