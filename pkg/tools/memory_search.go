package tools

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/memory"
)

// MemorySearchTool provides semantic search over past conversations and knowledge.
type MemorySearchTool struct {
	store *memory.VectorStore
}

// NewMemorySearchTool creates a new memory search tool.
func NewMemorySearchTool(store *memory.VectorStore) *MemorySearchTool {
	return &MemorySearchTool{store: store}
}

func (t *MemorySearchTool) Name() string {
	return "search_memory"
}

func (t *MemorySearchTool) Description() string {
	return "Search your memory of past conversations and knowledge about the user. You SHOULD call this proactively at the start of conversations and whenever the user mentions anything that might relate to prior context, preferences, or past discussions. Do not wait to be asked — if prior knowledge could help, search first."
}

func (t *MemorySearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Natural language search query describing what you want to recall",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 5)",
			},
			"filter": map[string]interface{}{
				"type":        "string",
				"description": "Filter results by source",
				"enum":        []string{"all", "conversations", "knowledge"},
			},
		},
		"required": []string{"query"},
	}
}

func (t *MemorySearchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return ErrorResult("query is required")
	}

	limit := 5
	if l, ok := args["limit"].(float64); ok && int(l) > 0 {
		limit = int(l)
	}

	filter := "all"
	if f, ok := args["filter"].(string); ok && f != "" {
		filter = f
	}

	results, err := t.store.Search(ctx, query, limit, filter)
	if err != nil {
		return ErrorResult(fmt.Sprintf("memory search failed: %v", err))
	}

	formatted := memory.FormatResults(results)
	return SilentResult(formatted)
}
