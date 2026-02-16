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
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	Score     float32 `json:"score"`
	Timestamp string  `json:"timestamp"` // RFC3339
	Category  string  `json:"category,omitempty"`
	Source    string  `json:"source"` // "conversations" or "knowledge"
	Channel   string  `json:"channel,omitempty"`
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

// SearchKnowledge searches the extracted knowledge base.
func (vs *VectorStore) SearchKnowledge(ctx context.Context, query string, limit int) ([]MemoryResult, error) {
	if vs.knowledge.Count() == 0 {
		return nil, nil
	}

	if limit > vs.knowledge.Count() {
		limit = vs.knowledge.Count()
	}

	results, err := vs.knowledge.Query(ctx, query, limit, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("search knowledge: %w", err)
	}

	var out []MemoryResult
	for _, r := range results {
		out = append(out, MemoryResult{
			ID:        r.ID,
			Content:   r.Content,
			Score:     r.Similarity,
			Timestamp: r.Metadata["updated_at"],
			Category:  r.Metadata["category"],
			Source:    "knowledge",
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
			date := formatDate(r.Timestamp)
			cat := ""
			if r.Category != "" {
				cat = fmt.Sprintf(" (%s)", r.Category)
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s%s\n", date, r.Content, cat))
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
