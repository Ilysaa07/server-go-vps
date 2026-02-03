package whatsapp

import (
	"fmt"
	"sync"
)

// LabelStore manages WhatsApp Business labels and their associations
type LabelStore struct {
	// Labels maps labelID -> labelName
	Labels map[string]string
	// Associations maps labelID -> set of JIDs (phone numbers)
	Associations map[string]map[string]bool
	mu           sync.RWMutex
}

// NewLabelStore creates a new LabelStore instance
func NewLabelStore() *LabelStore {
	return &LabelStore{
		Labels:       make(map[string]string),
		Associations: make(map[string]map[string]bool),
	}
}

// SetLabel stores or updates a label definition
func (ls *LabelStore) SetLabel(id, name string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.Labels[id] = name
	fmt.Printf("🏷️ Label stored: ID=%s, Name=%s\n", id, name)
}

// AddAssociation adds a JID to a label
func (ls *LabelStore) AddAssociation(labelID, jid string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.Associations[labelID] == nil {
		ls.Associations[labelID] = make(map[string]bool)
	}
	ls.Associations[labelID][jid] = true
}

// RemoveAssociation removes a JID from a label
func (ls *LabelStore) RemoveAssociation(labelID, jid string) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.Associations[labelID] != nil {
		delete(ls.Associations[labelID], jid)
	}
}

// GetJIDsForLabelName returns all JIDs that have the given label name
func (ls *LabelStore) GetJIDsForLabelName(labelName string) []string {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	// Find labelID by name
	var labelID string
	for id, name := range ls.Labels {
		if name == labelName {
			labelID = id
			break
		}
	}

	if labelID == "" {
		fmt.Printf("🏷️ Label '%s' not found in store\n", labelName)
		return nil
	}

	// Get all JIDs for this label
	jids := make([]string, 0, len(ls.Associations[labelID]))
	for jid := range ls.Associations[labelID] {
		jids = append(jids, jid)
	}

	fmt.Printf("🏷️ Found %d JIDs for label '%s' (ID: %s)\n", len(jids), labelName, labelID)
	return jids
}

// GetAllLabels returns a copy of all labels for debugging
func (ls *LabelStore) GetAllLabels() map[string]string {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	result := make(map[string]string)
	for k, v := range ls.Labels {
		result[k] = v
	}
	return result
}

// GetAllAssociations returns a copy of all associations for debugging
func (ls *LabelStore) GetAllAssociations() map[string][]string {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	result := make(map[string][]string)
	for labelID, jids := range ls.Associations {
		for jid := range jids {
			result[labelID] = append(result[labelID], jid)
		}
	}
	return result
}
