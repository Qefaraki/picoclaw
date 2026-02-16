package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TopicMapping maps a forum topic to a specialist.
type TopicMapping struct {
	ChatID         string `json:"chat_id"`
	ThreadID       string `json:"thread_id"`
	SpecialistName string `json:"specialist_name"`
	CreatedAt      string `json:"created_at"`
}

// TopicMappingStore persists topic-to-specialist mappings.
type TopicMappingStore struct {
	Mappings []TopicMapping `json:"mappings"`
	mu       sync.RWMutex
	filePath string
}

// NewTopicMappingStore creates a new store, loading from disk if available.
func NewTopicMappingStore(workspace string) *TopicMappingStore {
	stateDir := filepath.Join(workspace, "state")
	os.MkdirAll(stateDir, 0755)

	s := &TopicMappingStore{
		filePath: filepath.Join(stateDir, "topic_mappings.json"),
	}
	s.load()
	return s
}

// LookupSpecialist returns the specialist name for a given chat+thread, or "".
func (s *TopicMappingStore) LookupSpecialist(chatID, threadID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.Mappings {
		if m.ChatID == chatID && m.ThreadID == threadID {
			return m.SpecialistName
		}
	}
	return ""
}

// SetMapping creates or updates a topic-to-specialist mapping.
func (s *TopicMappingStore) SetMapping(chatID, threadID, specialistName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update existing or append
	for i, m := range s.Mappings {
		if m.ChatID == chatID && m.ThreadID == threadID {
			s.Mappings[i].SpecialistName = specialistName
			s.Mappings[i].CreatedAt = time.Now().UTC().Format(time.RFC3339)
			return s.saveAtomic()
		}
	}

	s.Mappings = append(s.Mappings, TopicMapping{
		ChatID:         chatID,
		ThreadID:       threadID,
		SpecialistName: specialistName,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	return s.saveAtomic()
}

// RemoveMapping removes a topic mapping.
func (s *TopicMappingStore) RemoveMapping(chatID, threadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, m := range s.Mappings {
		if m.ChatID == chatID && m.ThreadID == threadID {
			s.Mappings = append(s.Mappings[:i], s.Mappings[i+1:]...)
			return s.saveAtomic()
		}
	}
	return nil
}

func (s *TopicMappingStore) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	json.Unmarshal(data, s)
}

func (s *TopicMappingStore) saveAtomic() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal topic mappings: %w", err)
	}

	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := os.Rename(tmp, s.filePath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}
