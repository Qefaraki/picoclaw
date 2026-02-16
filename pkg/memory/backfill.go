package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// BackfillSession mirrors session.Session to avoid circular imports.
type BackfillSession struct {
	Key      string              `json:"key"`
	Messages []providers.Message `json:"messages"`
	Summary  string              `json:"summary,omitempty"`
	Created  time.Time           `json:"created"`
	Updated  time.Time           `json:"updated"`
}

// BackfillStats tracks progress of a backfill operation.
type BackfillStats struct {
	SessionsTotal     int
	SessionsProcessed int
	TurnsIndexed      int
	FactsExtracted    int
	Errors            int
}

// BackfillOptions configures a backfill run.
type BackfillOptions struct {
	ExtractKnowledge bool // Whether to also run knowledge extraction (slow, costs LLM calls)
	DryRun           bool // Print what would be done without actually doing it
}

// Backfill reads all session files and indexes conversations + optionally extracts knowledge.
func Backfill(ctx context.Context, sessionsDir string, store *VectorStore, extractor *KnowledgeExtractor, opts BackfillOptions) (*BackfillStats, error) {
	stats := &BackfillStats{}

	files, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("read sessions directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		stats.SessionsTotal++
	}

	fmt.Printf("Found %d session files in %s\n", stats.SessionsTotal, sessionsDir)

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}

		// Skip heartbeat and cron sessions — they're system-generated, not user conversations
		name := file.Name()
		if strings.HasPrefix(name, "heartbeat") || strings.HasPrefix(name, "cron-") {
			stats.SessionsTotal--
			fmt.Printf("  Skipping %s (system session)\n", name)
			continue
		}

		if ctx.Err() != nil {
			return stats, ctx.Err()
		}

		sessionPath := filepath.Join(sessionsDir, file.Name())
		if err := backfillSession(ctx, sessionPath, store, extractor, stats, opts); err != nil {
			logger.WarnCF("backfill", "Failed to backfill session", map[string]interface{}{
				"file":  file.Name(),
				"error": err.Error(),
			})
			stats.Errors++
		}
		stats.SessionsProcessed++

		fmt.Printf("  [%d/%d] %s — %d turns indexed\n",
			stats.SessionsProcessed, stats.SessionsTotal,
			file.Name(), stats.TurnsIndexed)
	}

	return stats, nil
}

func backfillSession(ctx context.Context, path string, store *VectorStore, extractor *KnowledgeExtractor, stats *BackfillStats, opts BackfillOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read session file: %w", err)
	}

	var sess BackfillSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return fmt.Errorf("parse session JSON: %w", err)
	}

	if len(sess.Messages) == 0 {
		return nil
	}

	// Parse channel and chatID from session key (e.g. "telegram:123456")
	channel, chatID := parseSessionKey(sess.Key)

	// Walk through messages, pairing user messages with the next assistant response
	for i := 0; i < len(sess.Messages); i++ {
		msg := sess.Messages[i]
		if msg.Role != "user" || msg.Content == "" {
			continue
		}

		// Find the next assistant message (skip tool messages)
		assistantMsg := ""
		for j := i + 1; j < len(sess.Messages); j++ {
			if sess.Messages[j].Role == "assistant" && sess.Messages[j].Content != "" {
				assistantMsg = sess.Messages[j].Content
				break
			}
			if sess.Messages[j].Role == "user" {
				// Next user message before an assistant response — no pair
				break
			}
		}

		if assistantMsg == "" {
			continue
		}

		if opts.DryRun {
			preview := msg.Content
			runes := []rune(preview)
			if len(runes) > 80 {
				preview = string(runes[:80]) + "..."
			}
			fmt.Printf("    [dry-run] Would index: %s\n", preview)
			stats.TurnsIndexed++
			continue
		}

		// Index the conversation turn
		store.IndexConversation(ctx, sess.Key, channel, chatID, msg.Content, assistantMsg)
		stats.TurnsIndexed++

		// Optionally extract knowledge
		if opts.ExtractKnowledge && extractor != nil {
			extractor.ExtractAndConsolidate(ctx, msg.Content, assistantMsg, sess.Key, "", KnowledgeIndexOpts{})
		}

		// Small delay to avoid hammering the embedding API
		time.Sleep(100 * time.Millisecond)
	}

	return nil
}

// parseSessionKey extracts channel and chatID from a session key like "telegram:123456".
func parseSessionKey(key string) (channel, chatID string) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "unknown", key
}
