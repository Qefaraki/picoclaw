package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/philippgille/chromem-go"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// MemoryResult represents a single search result from the vector store.
type MemoryResult struct {
	ID           string  `json:"id"`
	Content      string  `json:"content"`
	Score        float32 `json:"score"`
	Timestamp    string  `json:"timestamp"` // RFC3339
	Category     string  `json:"category,omitempty"`
	Source       string  `json:"source"` // "conversations" or "knowledge"
	Channel      string  `json:"channel,omitempty"`
	Specialist   string  `json:"specialist,omitempty"`
	SourceType   string  `json:"source_type,omitempty"`
	SourceName   string  `json:"source_name,omitempty"`
	SourceDate   string  `json:"source_date,omitempty"`
	SourcePerson string  `json:"source_person,omitempty"`
}

// KnowledgeIndexOpts holds optional metadata for specialist-scoped knowledge.
type KnowledgeIndexOpts struct {
	Specialist   string // scoping: "summer-camp", "" for global
	SourceType   string // "whatsapp_chat", "pdf", "email", "contract", "conversation", "manual"
	SourceName   string // "Charlie's WhatsApp", "Partnership Agreement.pdf"
	SourceDate   string // "2025-11-06T18:00:00Z" — when the source event happened
	SourcePerson string // "Charlie", "Sarah" — who said/wrote it
}

// VectorStore wraps chromem-go with two collections: conversations and knowledge.
type VectorStore struct {
	db            *chromem.DB
	conversations *chromem.Collection
	knowledge     *chromem.Collection
	dbPath        string
}

// NewVectorStore initializes a persistent vector DB at workspace/memory/vectors/.
func NewVectorStore(workspacePath string, embeddingFn chromem.EmbeddingFunc) (*VectorStore, error) {
	dbPath := filepath.Join(workspacePath, "memory", "vectors")
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		return nil, fmt.Errorf("create memory dir: %w", err)
	}

	db, err := chromem.NewPersistentDB(dbPath, false)
	if err != nil {
		return nil, fmt.Errorf("open vector db: %w", err)
	}

	conversations, err := db.GetOrCreateCollection("conversations", nil, embeddingFn)
	if err != nil {
		return nil, fmt.Errorf("create conversations collection: %w", err)
	}

	knowledge, err := db.GetOrCreateCollection("knowledge", nil, embeddingFn)
	if err != nil {
		return nil, fmt.Errorf("create knowledge collection: %w", err)
	}

	logger.InfoCF("memory", "Vector store initialized", map[string]interface{}{
		"path":               dbPath,
		"conversations_count": conversations.Count(),
		"knowledge_count":     knowledge.Count(),
	})

	return &VectorStore{
		db:            db,
		conversations: conversations,
		knowledge:     knowledge,
		dbPath:        dbPath,
	}, nil
}

// IndexConversation embeds a conversation turn into the conversations collection.
func (vs *VectorStore) IndexConversation(ctx context.Context, sessionKey, channel, chatID, userMsg, assistantMsg string) {
	ts := time.Now()
	docID := fmt.Sprintf("%s:%d", sessionKey, ts.Unix())
	content := fmt.Sprintf("User: %s\nAssistant: %s", userMsg, assistantMsg)

	// Truncate very long messages to keep embeddings meaningful
	// Use rune-safe truncation to avoid splitting multi-byte characters
	if len(content) > 8000 {
		runes := []rune(content)
		if len(runes) > 8000 {
			content = string(runes[:8000])
		}
	}

	doc := chromem.Document{
		ID:      docID,
		Content: content,
		Metadata: map[string]string{
			"session_key": sessionKey,
			"channel":     channel,
			"chat_id":     chatID,
			"timestamp":   ts.Format(time.RFC3339),
			"date":        ts.Format("2006-01-02"),
		},
	}

	if err := vs.conversations.AddDocument(ctx, doc); err != nil {
		logger.ErrorCF("memory", "Failed to index conversation", map[string]interface{}{
			"error":       err.Error(),
			"session_key": sessionKey,
		})
		return
	}

	logger.DebugCF("memory", "Indexed conversation turn", map[string]interface{}{
		"doc_id":      docID,
		"content_len": len(content),
	})
}

// IndexKnowledge adds or updates a fact in the knowledge collection.
func (vs *VectorStore) IndexKnowledge(ctx context.Context, docID, fact, category string) error {
	if docID == "" {
		docID = fmt.Sprintf("k:%d", time.Now().UnixNano())
	}

	doc := chromem.Document{
		ID:      docID,
		Content: fact,
		Metadata: map[string]string{
			"category":   category,
			"updated_at": time.Now().Format(time.RFC3339),
		},
	}

	if err := vs.knowledge.AddDocument(ctx, doc); err != nil {
		return fmt.Errorf("index knowledge: %w", err)
	}

	logger.DebugCF("memory", "Indexed knowledge", map[string]interface{}{
		"doc_id":   docID,
		"category": category,
		"fact_len": len(fact),
	})
	return nil
}

// IndexKnowledgeWithOpts adds a fact with specialist scoping and source attribution.
func (vs *VectorStore) IndexKnowledgeWithOpts(ctx context.Context, docID, fact, category string, opts KnowledgeIndexOpts) error {
	if docID == "" {
		docID = fmt.Sprintf("k:%d", time.Now().UnixNano())
	}

	metadata := map[string]string{
		"category":   category,
		"updated_at": time.Now().Format(time.RFC3339),
	}
	if opts.Specialist != "" {
		metadata["specialist"] = opts.Specialist
	}
	if opts.SourceType != "" {
		metadata["source_type"] = opts.SourceType
	}
	if opts.SourceName != "" {
		metadata["source_name"] = opts.SourceName
	}
	if opts.SourceDate != "" {
		metadata["source_date"] = opts.SourceDate
	}
	if opts.SourcePerson != "" {
		metadata["source_person"] = opts.SourcePerson
	}

	doc := chromem.Document{
		ID:       docID,
		Content:  fact,
		Metadata: metadata,
	}

	if err := vs.knowledge.AddDocument(ctx, doc); err != nil {
		return fmt.Errorf("index knowledge: %w", err)
	}

	logger.DebugCF("memory", "Indexed knowledge", map[string]interface{}{
		"doc_id":     docID,
		"category":   category,
		"specialist": opts.Specialist,
		"fact_len":   len(fact),
	})
	return nil
}

// DeleteKnowledge removes a fact from the knowledge collection.
func (vs *VectorStore) DeleteKnowledge(ctx context.Context, docID string) error {
	if err := vs.knowledge.Delete(ctx, nil, nil, docID); err != nil {
		return fmt.Errorf("delete knowledge %s: %w", docID, err)
	}
	return nil
}

// SearchConversations searches the conversation history.
func (vs *VectorStore) SearchConversations(ctx context.Context, query string, limit int) ([]MemoryResult, error) {
	if vs.conversations.Count() == 0 {
		return nil, nil
	}

	if limit > vs.conversations.Count() {
		limit = vs.conversations.Count()
	}

	results, err := vs.conversations.Query(ctx, query, limit, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("search conversations: %w", err)
	}

	var out []MemoryResult
	for _, r := range results {
		out = append(out, MemoryResult{
			ID:        r.ID,
			Content:   r.Content,
			Score:     r.Similarity,
			Timestamp: r.Metadata["timestamp"],
			Channel:   r.Metadata["channel"],
			Source:    "conversations",
		})
	}
	return out, nil
}

// SearchKnowledge searches the extracted knowledge base (global, unscoped).
func (vs *VectorStore) SearchKnowledge(ctx context.Context, query string, limit int) ([]MemoryResult, error) {
	return vs.SearchKnowledgeScoped(ctx, query, limit, "")
}

// SearchKnowledgeScoped searches knowledge filtered by specialist.
// If specialist is empty, returns all knowledge (global search).
// When specialist is set, searches specialist-scoped first, then backfills
// with global results for a "shared blackboard" effect.
func (vs *VectorStore) SearchKnowledgeScoped(ctx context.Context, query string, limit int, specialist string) ([]MemoryResult, error) {
	if vs.knowledge.Count() == 0 {
		return nil, nil
	}

	if specialist == "" {
		return vs.searchKnowledgeInternal(ctx, query, limit, nil)
	}

	// 1. Search specialist-scoped first
	scoped, err := vs.searchKnowledgeInternal(ctx, query, limit, map[string]string{"specialist": specialist})
	if err != nil {
		return nil, err
	}

	// 2. If fewer than limit, backfill with global (unscoped) results
	if len(scoped) < limit {
		remaining := limit - len(scoped)
		global, _ := vs.searchKnowledgeInternal(ctx, query, remaining, nil)
		// Deduplicate by ID
		seen := make(map[string]bool)
		for _, r := range scoped {
			seen[r.ID] = true
		}
		for _, r := range global {
			if !seen[r.ID] {
				scoped = append(scoped, r)
			}
		}
	}

	return scoped, nil
}

// searchKnowledgeInternal is the core query implementation.
func (vs *VectorStore) searchKnowledgeInternal(ctx context.Context, query string, limit int, where map[string]string) ([]MemoryResult, error) {
	if vs.knowledge.Count() == 0 {
		return nil, nil
	}

	if limit > vs.knowledge.Count() {
		limit = vs.knowledge.Count()
	}

	results, err := vs.knowledge.Query(ctx, query, limit, where, nil)
	if err != nil {
		return nil, fmt.Errorf("search knowledge: %w", err)
	}

	var out []MemoryResult
	for _, r := range results {
		out = append(out, MemoryResult{
			ID:           r.ID,
			Content:      r.Content,
			Score:        r.Similarity,
			Timestamp:    r.Metadata["updated_at"],
			Category:     r.Metadata["category"],
			Source:       "knowledge",
			Specialist:   r.Metadata["specialist"],
			SourceType:   r.Metadata["source_type"],
			SourceName:   r.Metadata["source_name"],
			SourceDate:   r.Metadata["source_date"],
			SourcePerson: r.Metadata["source_person"],
		})
	}
	return out, nil
}

// Search queries both collections and merges results by relevance.
func (vs *VectorStore) Search(ctx context.Context, query string, limit int, filter string) ([]MemoryResult, error) {
	var all []MemoryResult

	if filter == "" || filter == "all" {
		convLimit := limit
		knowLimit := limit
		conv, err := vs.SearchConversations(ctx, query, convLimit)
		if err != nil {
			logger.WarnCF("memory", "Conversation search failed: %v", map[string]interface{}{"error": err.Error()})
		} else {
			all = append(all, conv...)
		}
		know, err := vs.SearchKnowledge(ctx, query, knowLimit)
		if err != nil {
			logger.WarnCF("memory", "Knowledge search failed: %v", map[string]interface{}{"error": err.Error()})
		} else {
			all = append(all, know...)
		}
		// Sort by score descending, take top limit
		sort.Slice(all, func(i, j int) bool {
			return all[i].Score > all[j].Score
		})
		if len(all) > limit {
			all = all[:limit]
		}
	} else if filter == "conversations" {
		var err error
		all, err = vs.SearchConversations(ctx, query, limit)
		if err != nil {
			return nil, err
		}
	} else if filter == "knowledge" {
		var err error
		all, err = vs.SearchKnowledge(ctx, query, limit)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("unknown filter: %s (use: all, conversations, knowledge)", filter)
	}

	return all, nil
}

// FormatResults formats search results into a human-readable string.
func FormatResults(results []MemoryResult) string {
	if len(results) == 0 {
		return "No memories found."
	}

	var knowledgeResults, convResults []MemoryResult
	for _, r := range results {
		if r.Source == "knowledge" {
			knowledgeResults = append(knowledgeResults, r)
		} else {
			convResults = append(convResults, r)
		}
	}

	var sb strings.Builder

	if len(knowledgeResults) > 0 {
		sb.WriteString("## Knowledge\n")
		for _, r := range knowledgeResults {
			prefix := formatProvenance(r)
			cat := ""
			if r.Category != "" {
				cat = fmt.Sprintf(" (%s)", r.Category)
			}
			sb.WriteString(fmt.Sprintf("- %s %s%s\n", prefix, r.Content, cat))
		}
	}

	if len(convResults) > 0 {
		if len(knowledgeResults) > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("## Conversations\n")
		for _, r := range convResults {
			date := formatDate(r.Timestamp)
			ch := ""
			if r.Channel != "" {
				ch = fmt.Sprintf(", %s", r.Channel)
			}
			// Show a preview of the conversation (rune-safe)
			preview := r.Content
			runes := []rune(preview)
			if len(runes) > 200 {
				preview = string(runes[:200]) + "..."
			}
			sb.WriteString(fmt.Sprintf("- [%s%s] %s\n", date, ch, preview))
		}
	}

	return sb.String()
}

// formatProvenance builds a bracketed source attribution prefix for a knowledge result.
// Examples: "[2025-11-06, Charlie via WhatsApp]", "[2025-11-06]", "[unknown]"
func formatProvenance(r MemoryResult) string {
	var parts []string

	// Date: prefer source_date, fall back to updated_at
	date := r.SourceDate
	if date == "" {
		date = r.Timestamp
	}
	parts = append(parts, formatDate(date))

	// Person + source type
	if r.SourcePerson != "" && r.SourceType != "" {
		parts = append(parts, fmt.Sprintf("%s via %s", r.SourcePerson, r.SourceType))
	} else if r.SourcePerson != "" {
		parts = append(parts, r.SourcePerson)
	} else if r.SourceName != "" {
		parts = append(parts, r.SourceName)
	} else if r.SourceType != "" {
		parts = append(parts, r.SourceType)
	}

	return "[" + strings.Join(parts, ", ") + "]"
}

func formatDate(ts string) string {
	if ts == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Format("2006-01-02")
}
