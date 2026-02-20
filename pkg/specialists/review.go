package specialists

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
)

const reviewPrompt = `You are reviewing recent interactions for the specialist "%s".

Below are recent knowledge entries extracted from conversations involving this specialist. Analyze them and produce self-improvement notes:

1. What patterns are you seeing in the questions/requests?
2. What knowledge gaps did you notice?
3. What could you do better next time?
4. Any recurring topics or entities to track more closely?

Keep your notes concise and actionable (max 10 bullet points).

RECENT KNOWLEDGE:
%s

Write your self-improvement notes below:`

// ReviewSpecialist analyzes recent specialist interactions and writes learnings.
func ReviewSpecialist(ctx context.Context, name string, provider providers.LLMProvider, model string, store *memory.VectorStore, workspace string) error {
	if store == nil {
		return fmt.Errorf("vector store not available")
	}

	// Pull last 20 specialist-scoped knowledge entries
	facts, err := store.SearchKnowledgeScoped(ctx, "recent interactions and consultations", 20, name)
	if err != nil {
		return fmt.Errorf("search specialist knowledge: %w", err)
	}

	if len(facts) == 0 {
		logger.InfoCF("specialist", "No recent knowledge for review", map[string]interface{}{
			"specialist": name,
		})
		return nil
	}

	// Format facts for the prompt
	var factLines []string
	for _, f := range facts {
		factLines = append(factLines, fmt.Sprintf("- [%s] %s", f.Category, f.Content))
	}

	prompt := fmt.Sprintf(reviewPrompt, name, strings.Join(factLines, "\n"))

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := provider.Chat(ctx, []providers.Message{
		{Role: "user", Content: prompt},
	}, nil, model, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.3,
	})
	if err != nil {
		return fmt.Errorf("review LLM call: %w", err)
	}

	// Write to LEARNINGS.md
	learningsPath := filepath.Join(workspace, "specialists", name, "LEARNINGS.md")
	header := fmt.Sprintf("\n\n## Review — %s\n\n", time.Now().Format("2006-01-02"))

	f, err := os.OpenFile(learningsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open LEARNINGS.md: %w", err)
	}
	defer f.Close()

	f.WriteString(header)
	f.WriteString(strings.TrimSpace(resp.Content))
	f.WriteString("\n")

	logger.InfoCF("specialist", "Specialist review completed", map[string]interface{}{
		"specialist":    name,
		"facts_reviewed": len(facts),
	})

	return nil
}

// ReviewAllSpecialists runs a review for each specialist that has knowledge entries.
func ReviewAllSpecialists(ctx context.Context, loader *SpecialistLoader, provider providers.LLMProvider, model string, store *memory.VectorStore, workspace string) {
	specialists := loader.ListSpecialists()
	for _, s := range specialists {
		if err := ReviewSpecialist(ctx, s.Name, provider, model, store, workspace); err != nil {
			logger.WarnCF("specialist", "Review failed", map[string]interface{}{
				"specialist": s.Name,
				"error":      err.Error(),
			})
		}
	}
}
