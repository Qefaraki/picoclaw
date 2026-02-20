package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Relation represents a subject-predicate-object triple.
type Relation struct {
	Subject    string `json:"s"`
	Predicate  string `json:"p"`
	Object     string `json:"o"`
	Timestamp  string `json:"ts"`
	Specialist string `json:"specialist,omitempty"`
}

// RelationStore is a simple JSON-line based relation store for entity relationships.
type RelationStore struct {
	relations []Relation
	filePath  string
	mu        sync.RWMutex
}

// NewRelationStore loads or creates a relation store at workspace/memory/relations.jsonl.
func NewRelationStore(workspace string) *RelationStore {
	dir := filepath.Join(workspace, "memory")
	os.MkdirAll(dir, 0755)
	fp := filepath.Join(dir, "relations.jsonl")

	rs := &RelationStore{filePath: fp}
	rs.load()
	return rs
}

// Add appends a new relation and persists it.
func (rs *RelationStore) Add(r Relation) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if r.Timestamp == "" {
		r.Timestamp = time.Now().Format(time.RFC3339)
	}

	// Deduplicate: skip if exact (s,p,o) already exists
	for _, existing := range rs.relations {
		if existing.Subject == r.Subject && existing.Predicate == r.Predicate && existing.Object == r.Object {
			return nil
		}
	}

	rs.relations = append(rs.relations, r)
	return rs.appendToFile(r)
}

// Query returns all relations where entity appears as subject or object (1-hop traversal).
func (rs *RelationStore) Query(entity string) []Relation {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	entity = strings.ToLower(entity)
	var results []Relation
	for _, r := range rs.relations {
		if strings.ToLower(r.Subject) == entity || strings.ToLower(r.Object) == entity {
			results = append(results, r)
		}
	}
	return results
}

// QueryScoped returns relations matching entity and optional specialist scope.
func (rs *RelationStore) QueryScoped(entity, specialist string) []Relation {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	entity = strings.ToLower(entity)
	var results []Relation
	for _, r := range rs.relations {
		if specialist != "" && r.Specialist != specialist && r.Specialist != "" {
			continue
		}
		if strings.ToLower(r.Subject) == entity || strings.ToLower(r.Object) == entity {
			results = append(results, r)
		}
	}
	return results
}

// FormatRelations formats relations into a readable string.
func FormatRelations(relations []Relation) string {
	if len(relations) == 0 {
		return ""
	}
	var lines []string
	for _, r := range relations {
		lines = append(lines, r.Subject+" → "+r.Predicate+" → "+r.Object)
	}
	return strings.Join(lines, "\n")
}

func (rs *RelationStore) load() {
	data, err := os.ReadFile(rs.filePath)
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r Relation
		if err := json.Unmarshal([]byte(line), &r); err == nil {
			rs.relations = append(rs.relations, r)
		}
	}
}

func (rs *RelationStore) appendToFile(r Relation) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(rs.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	f.Write(data)
	f.Write([]byte("\n"))
	return nil
}
