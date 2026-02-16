package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// KnowledgeExtractor extracts and consolidates knowledge from conversations.
type KnowledgeExtractor struct {
	provider providers.LLMProvider
	model    string
	store    *VectorStore
}

// ExtractedFact represents a single fact extracted from a conversation.
type ExtractedFact struct {
	Fact     string `json:"fact"`
	Category string `json:"category"`
}

// ConsolidationAction represents the LLM's decision for a fact.
type ConsolidationAction struct {
	Action  string `json:"action"`   // ADD, UPDATE, DELETE, NOOP
	FactID  string `json:"fact_id"`  // existing fact ID (for UPDATE/DELETE)
	NewFact string `json:"new_fact"` // merged/updated fact text (for UPDATE)
}

// NewKnowledgeExtractor creates a new extractor.
func NewKnowledgeExtractor(provider providers.LLMProvider, model string, store *VectorStore) *KnowledgeExtractor {
	return &KnowledgeExtractor{
		provider: provider,
		model:    model,
		store:    store,
	}
}

// ExtractAndConsolidate runs the full Mem0-style pipeline:
// 1. Extract facts from conversation
// 2. For each fact, search for similar existing knowledge
// 3. If similar: LLM decides ADD/UPDATE/DELETE
// 4. Execute operations
//
// specialist and opts scope the knowledge to a specific specialist domain.
// Pass empty specialist and zero opts for global (unscoped) extraction.
func (ke *KnowledgeExtractor) ExtractAndConsolidate(ctx context.Context, userMsg, assistantMsg, sessionKey, specialist string, opts KnowledgeIndexOpts) {
	// Step 1: Extract facts
	facts, err := ke.extractFacts(ctx, userMsg, assistantMsg)
	if err != nil {
		logger.WarnCF("memory", "Knowledge extraction failed", map[string]interface{}{
			"error":       err.Error(),
			"session_key": sessionKey,
			"specialist":  specialist,
		})
		return
	}

	if len(facts) == 0 {
		logger.DebugCF("memory", "No facts extracted from conversation", map[string]interface{}{
			"session_key": sessionKey,
		})
		return
	}

	logger.InfoCF("memory", "Extracted facts from conversation", map[string]interface{}{
		"count":       len(facts),
		"session_key": sessionKey,
		"specialist":  specialist,
	})

	// Step 2-4: Consolidate each fact
	for _, fact := range facts {
		if err := ke.consolidateFact(ctx, fact, specialist, opts); err != nil {
			logger.WarnCF("memory", "Failed to consolidate fact", map[string]interface{}{
				"error": err.Error(),
				"fact":  fact.Fact,
			})
		}
	}
}

// ExtractFacts is a public version of extractFacts for use by the feed tool.
func (ke *KnowledgeExtractor) ExtractFacts(ctx context.Context, content string) ([]ExtractedFact, error) {
	return ke.extractFacts(ctx, content, "")
}

const extractionPrompt = `Extract key facts about the user from this conversation. Focus on:
- Biographical information (name, location, occupation, plans)
- Preferences and opinions
- Tasks, deadlines, goals
- Relationships (people mentioned)
- Important context (events, decisions, states)

Return a JSON array of facts. Each fact should be a self-contained statement.
If no meaningful facts can be extracted, return an empty array [].

Categories: biographical, preference, task, relationship, contextual

Example output:
[
  {"fact": "User is a student at QMUL", "category": "biographical"},
  {"fact": "User prefers dark mode in all apps", "category": "preference"}
]

CONVERSATION:
User: %s
Assistant: %s

Return ONLY valid JSON, no markdown fences or explanation.`

func (ke *KnowledgeExtractor) extractFacts(ctx context.Context, userMsg, assistantMsg string) ([]ExtractedFact, error) {
	// Skip very short or trivial messages
	if len(userMsg) < 10 {
		return nil, nil
	}

	prompt := fmt.Sprintf(extractionPrompt, userMsg, truncate(assistantMsg, 2000))

	resp, err := ke.provider.Chat(ctx, []providers.Message{
		{Role: "user", Content: prompt},
	}, nil, ke.model, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.1,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM extraction call: %w", err)
	}

	content := strings.TrimSpace(resp.Content)
	// Strip markdown fences if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var facts []ExtractedFact
	if err := json.Unmarshal([]byte(content), &facts); err != nil {
		return nil, fmt.Errorf("parse extracted facts: %w (response: %s)", err, truncate(content, 200))
	}

	return facts, nil
}

func (ke *KnowledgeExtractor) consolidateFact(ctx context.Context, fact ExtractedFact, specialist string, opts KnowledgeIndexOpts) error {
	// Ensure specialist is set in opts
	opts.Specialist = specialist

	// Search for similar existing knowledge (scoped to same specialist)
	existing, err := ke.store.SearchKnowledgeScoped(ctx, fact.Fact, 3, specialist)
	if err != nil {
		// If search fails, just add as new
		return ke.store.IndexKnowledgeWithOpts(ctx, "", fact.Fact, fact.Category, opts)
	}

	// Filter to only highly similar results (> 0.8)
	var similar []MemoryResult
	for _, r := range existing {
		if r.Score > 0.8 {
			similar = append(similar, r)
		}
	}

	if len(similar) == 0 {
		// No similar facts — add as new
		return ke.store.IndexKnowledgeWithOpts(ctx, "", fact.Fact, fact.Category, opts)
	}

	// LLM decides: UPDATE, DELETE, or NOOP
	action, err := ke.decideAction(ctx, fact, similar)
	if err != nil {
		// On error, just add as new to avoid losing information
		logger.WarnCF("memory", "Consolidation decision failed, adding as new", map[string]interface{}{
			"error": err.Error(),
		})
		return ke.store.IndexKnowledgeWithOpts(ctx, "", fact.Fact, fact.Category, opts)
	}

	switch action.Action {
	case "UPDATE":
		// Delete old, add updated version
		if action.FactID != "" {
			_ = ke.store.DeleteKnowledge(ctx, action.FactID)
		}
		newFact := action.NewFact
		if newFact == "" {
			newFact = fact.Fact
		}
		return ke.store.IndexKnowledgeWithOpts(ctx, "", newFact, fact.Category, opts)

	case "DELETE":
		if action.FactID != "" {
			return ke.store.DeleteKnowledge(ctx, action.FactID)
		}
		return nil

	case "NOOP":
		// Fact already exists, no action needed
		return nil

	default:
		// ADD — treat unknown actions as add
		return ke.store.IndexKnowledgeWithOpts(ctx, "", fact.Fact, fact.Category, opts)
	}
}

const consolidationPrompt = `You are managing a knowledge base about a user. A new fact has been extracted from a conversation, and similar existing facts were found.

NEW FACT: %s

EXISTING SIMILAR FACTS:
%s

Decide what to do:
- UPDATE: The new fact updates/replaces an existing one (e.g., new address replaces old). Return the merged fact.
- DELETE: An existing fact is now obsolete due to the new fact. Specify which to delete.
- NOOP: The new fact is essentially the same as an existing one. No action needed.
- ADD: The new fact is related but distinct from existing facts. Add it.

Return ONLY valid JSON:
{"action": "UPDATE|DELETE|NOOP|ADD", "fact_id": "id_of_existing_fact_if_applicable", "new_fact": "merged fact text for UPDATE"}
`

func (ke *KnowledgeExtractor) decideAction(ctx context.Context, fact ExtractedFact, similar []MemoryResult) (*ConsolidationAction, error) {
	var existingLines []string
	for _, s := range similar {
		existingLines = append(existingLines, fmt.Sprintf("- [ID: %s] %s (score: %.2f)", s.ID, s.Content, s.Score))
	}

	prompt := fmt.Sprintf(consolidationPrompt, fact.Fact, strings.Join(existingLines, "\n"))

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := ke.provider.Chat(ctx, []providers.Message{
		{Role: "user", Content: prompt},
	}, nil, ke.model, map[string]interface{}{
		"max_tokens":  256,
		"temperature": 0.1,
	})
	if err != nil {
		return nil, fmt.Errorf("consolidation LLM call: %w", err)
	}

	content := strings.TrimSpace(resp.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var action ConsolidationAction
	if err := json.Unmarshal([]byte(content), &action); err != nil {
		return nil, fmt.Errorf("parse consolidation action: %w", err)
	}

	return &action, nil
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
