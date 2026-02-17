package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/specialists"
	"github.com/sipeed/picoclaw/pkg/state"
)

// ---------------------------------------------------------------------------
// ConsultSpecialistTool — consult a domain specialist
// ---------------------------------------------------------------------------

// ConsultSpecialistTool executes a specialist agent with its own persona and scoped memory.
type ConsultSpecialistTool struct {
	loader      *specialists.SpecialistLoader
	provider    providers.LLMProvider
	model       string
	tools       *ToolRegistry
	vectorStore *memory.VectorStore
	extractor   *memory.KnowledgeExtractor
	maxIter     int
	workspace   string
	channel     string
	chatID      string
}

// ConsultSpecialistConfig holds configuration for creating a ConsultSpecialistTool.
type ConsultSpecialistConfig struct {
	Loader      *specialists.SpecialistLoader
	Provider    providers.LLMProvider
	Model       string
	Tools       *ToolRegistry
	VectorStore *memory.VectorStore
	Extractor   *memory.KnowledgeExtractor
	MaxIter     int
	Workspace   string
}

func NewConsultSpecialistTool(cfg ConsultSpecialistConfig) *ConsultSpecialistTool {
	return &ConsultSpecialistTool{
		loader:      cfg.Loader,
		provider:    cfg.Provider,
		model:       cfg.Model,
		tools:       cfg.Tools,
		vectorStore: cfg.VectorStore,
		extractor:   cfg.Extractor,
		maxIter:     cfg.MaxIter,
		workspace:   cfg.Workspace,
		channel:     "specialist",
		chatID:      "direct",
	}
}

func (t *ConsultSpecialistTool) Name() string { return "consult_specialist" }

func (t *ConsultSpecialistTool) Description() string {
	desc := "Consult a domain specialist for focused expertise. The specialist has its own persona, scoped memory, and learns from each consultation."

	all := t.loader.ListSpecialists()
	if len(all) > 0 {
		var parts []string
		for _, s := range all {
			if s.Description != "" {
				parts = append(parts, fmt.Sprintf("%s (%s)", s.Name, s.Description))
			} else {
				parts = append(parts, s.Name)
			}
		}
		desc += " Available specialists: " + strings.Join(parts, ", ") + "."
	}

	return desc
}

func (t *ConsultSpecialistTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"specialist": map[string]interface{}{
				"type":        "string",
				"description": "Name of the specialist to consult",
			},
			"question": map[string]interface{}{
				"type":        "string",
				"description": "The question to ask the specialist",
			},
			"context": map[string]interface{}{
				"type":        "string",
				"description": "Optional extra context to provide to the specialist",
			},
		},
		"required": []string{"specialist", "question"},
	}
}

func (t *ConsultSpecialistTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

func (t *ConsultSpecialistTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	specialistName, _ := args["specialist"].(string)
	question, _ := args["question"].(string)
	extraContext, _ := args["context"].(string)

	if specialistName == "" || question == "" {
		return ErrorResult("specialist and question are required")
	}

	// Load specialist persona
	persona, ok := t.loader.LoadSpecialist(specialistName)
	if !ok {
		return ErrorResult(fmt.Sprintf("specialist %q not found", specialistName))
	}

	// Search specialist-scoped memory for relevant knowledge
	var knowledgeSection string
	if t.vectorStore != nil {
		results, err := t.vectorStore.SearchKnowledgeScoped(ctx, question, 10, specialistName)
		if err == nil && len(results) > 0 {
			knowledgeSection = "\n\n## Relevant Knowledge\n\n" + memory.FormatResults(results)
		}
	}

	// Load USER.md for user context
	var userContext string
	userMD := filepath.Join(t.workspace, "USER.md")
	if data, err := os.ReadFile(userMD); err == nil {
		userContext = "\n\n## User Profile\n\n" + string(data)
	}

	// Build system prompt
	systemPrompt := persona + knowledgeSection + userContext +
		"\n\n## Instructions\n\nWhen answering, cite your sources (who said it, when, where) so the user can verify. " +
		"Be thorough and draw on all relevant knowledge available to you."

	// Build messages
	userContent := question
	if extraContext != "" {
		userContent = fmt.Sprintf("Context: %s\n\nQuestion: %s", extraContext, question)
	}

	messages := []providers.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}

	// Run tool loop with specialist's tools
	loopResult, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:      t.provider,
		Model:         t.model,
		Tools:         t.tools,
		MaxIterations: t.maxIter,
		LLMOptions: map[string]any{
			"max_tokens":  4096,
			"temperature": 0.7,
		},
	}, messages, t.channel, t.chatID)

	if err != nil {
		return ErrorResult(fmt.Sprintf("Specialist consultation failed: %v", err))
	}

	result := loopResult.Content

	// Async: extract knowledge from the consultation into specialist-scoped memory
	if t.extractor != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			t.extractor.ExtractAndConsolidateSpecialist(
				bgCtx,
				result, question,
				fmt.Sprintf("specialist:%s", specialistName),
				specialistName,
				memory.KnowledgeIndexOpts{
					Specialist: specialistName,
					SourceType: "conversation",
				},
			)
		}()
	}

	return SilentResult(fmt.Sprintf("Specialist '%s' response (iterations: %d):\n\n%s", specialistName, loopResult.Iterations, result))
}

// ---------------------------------------------------------------------------
// CreateSpecialistTool — create a new specialist from natural language
// ---------------------------------------------------------------------------

type CreateSpecialistTool struct {
	loader    *specialists.SpecialistLoader
	provider  providers.LLMProvider
	model     string
	workspace string
	extractor *memory.KnowledgeExtractor
	store     *memory.VectorStore
}

func NewCreateSpecialistTool(loader *specialists.SpecialistLoader, provider providers.LLMProvider, model, workspace string, extractor *memory.KnowledgeExtractor, store *memory.VectorStore) *CreateSpecialistTool {
	return &CreateSpecialistTool{
		loader:    loader,
		provider:  provider,
		model:     model,
		workspace: workspace,
		extractor: extractor,
		store:     store,
	}
}

func (t *CreateSpecialistTool) Name() string { return "create_specialist" }

func (t *CreateSpecialistTool) Description() string {
	return "Create a new domain specialist with a custom persona. The specialist will have its own scoped memory and can be consulted via consult_specialist."
}

func (t *CreateSpecialistTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Specialist name (lowercase letters, digits, hyphens only)",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "What this specialist should know about and be expert in",
			},
			"initial_knowledge": map[string]interface{}{
				"type":        "string",
				"description": "Optional initial information to seed the specialist with",
			},
		},
		"required": []string{"name", "description"},
	}
}

var validSpecialistName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)

func (t *CreateSpecialistTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)
	initialKnowledge, _ := args["initial_knowledge"].(string)

	if name == "" || description == "" {
		return ErrorResult("name and description are required")
	}

	// Validate name
	if !validSpecialistName.MatchString(name) {
		return ErrorResult("name must contain only lowercase letters, digits, and hyphens (cannot start/end with hyphen)")
	}

	// Check if already exists
	if t.loader.Exists(name) {
		return ErrorResult(fmt.Sprintf("specialist %q already exists", name))
	}

	// Create directory
	specDir := filepath.Join(t.loader.Dir(), name)
	if err := os.MkdirAll(specDir, 0755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create directory: %v", err))
	}
	if err := os.MkdirAll(filepath.Join(specDir, "references"), 0755); err != nil {
		return ErrorResult(fmt.Sprintf("failed to create references directory: %v", err))
	}

	// Generate persona using LLM
	personaPrompt := fmt.Sprintf(`Generate a specialist persona definition for a domain expert.

Name: %s
Domain: %s

Write a SPECIALIST.md file with:
1. YAML frontmatter with "name" and "description" fields
2. A markdown body that defines the specialist's persona, expertise areas, and approach

Example format:
---
name: finance
description: Corporate finance, valuation, market analysis, and financial modeling
---

# Finance Specialist

You are a finance specialist with deep expertise in corporate finance, valuation, and market analysis.

## Expertise
- DCF modeling and valuation
- Financial statement analysis
- Market research and competitive analysis

## Approach
- Always ground analysis in data and evidence
- Cite sources when making claims
- Be precise with numbers and calculations

Now generate the SPECIALIST.md content for the "%s" specialist focused on: %s

Return ONLY the file content, no explanation.`, name, description, name, description)

	resp, err := t.provider.Chat(ctx, []providers.Message{
		{Role: "user", Content: personaPrompt},
	}, nil, t.model, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.7,
	})
	if err != nil {
		// Fallback: generate a basic persona without LLM
		titleName := strings.ToUpper(name[:1]) + name[1:]
		resp = &providers.LLMResponse{
			Content: fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s Specialist\n\nYou are a specialist in %s.\n\n## Expertise\n- %s\n\n## Approach\n- Be thorough and cite sources when possible\n- Draw on all available knowledge\n",
				name, description, titleName, description, description),
		}
	}

	// Write SPECIALIST.md
	specFile := filepath.Join(specDir, "SPECIALIST.md")
	if err := os.WriteFile(specFile, []byte(resp.Content), 0644); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write SPECIALIST.md: %v", err))
	}

	// Seed initial knowledge if provided
	if initialKnowledge != "" && t.extractor != nil && t.store != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			facts, err := t.extractor.ExtractSpecialistFacts(bgCtx, initialKnowledge)
			if err != nil {
				logger.WarnCF("specialist", "Failed to extract initial knowledge", map[string]interface{}{
					"error":      err.Error(),
					"specialist": name,
				})
				return
			}
			opts := memory.KnowledgeIndexOpts{
				Specialist: name,
				SourceType: "manual",
				SourceName: "initial_knowledge",
			}
			for _, fact := range facts {
				if err := t.store.IndexKnowledgeWithOpts(bgCtx, "", fact.Fact, fact.Category, opts); err != nil {
					logger.WarnCF("specialist", "Failed to index initial fact", map[string]interface{}{
						"error": err.Error(),
						"fact":  fact.Fact,
					})
				}
			}
			logger.InfoCF("specialist", "Seeded initial knowledge", map[string]interface{}{
				"specialist": name,
				"facts":      len(facts),
			})
		}()
	}

	return SilentResult(fmt.Sprintf("Created specialist '%s' at %s.\nDescription: %s\nYou can now consult this specialist with consult_specialist.", name, specFile, description))
}

// ---------------------------------------------------------------------------
// FeedSpecialistTool — feed knowledge to a specialist
// ---------------------------------------------------------------------------

type FeedSpecialistTool struct {
	loader    *specialists.SpecialistLoader
	store     *memory.VectorStore
	extractor *memory.KnowledgeExtractor
}

func NewFeedSpecialistTool(loader *specialists.SpecialistLoader, store *memory.VectorStore, extractor *memory.KnowledgeExtractor) *FeedSpecialistTool {
	return &FeedSpecialistTool{
		loader:    loader,
		store:     store,
		extractor: extractor,
	}
}

func (t *FeedSpecialistTool) Name() string { return "feed_specialist" }

func (t *FeedSpecialistTool) Description() string {
	return "Feed knowledge to a specialist. Ingests text content (chat logs, documents, notes) and extracts facts into the specialist's scoped memory with source attribution."
}

func (t *FeedSpecialistTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"specialist": map[string]interface{}{
				"type":        "string",
				"description": "Name of the specialist to feed",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Text content to ingest (chat logs, documents, notes, etc.)",
			},
			"source_type": map[string]interface{}{
				"type":        "string",
				"description": "Type of source: whatsapp_chat, pdf, email, contract, notes, manual",
			},
			"source_name": map[string]interface{}{
				"type":        "string",
				"description": "Name of the source (e.g., \"Charlie's WhatsApp\", \"Partnership Agreement.pdf\")",
			},
			"source_date": map[string]interface{}{
				"type":        "string",
				"description": "When the source event happened (ISO 8601)",
			},
			"source_person": map[string]interface{}{
				"type":        "string",
				"description": "Who said/wrote the content",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "Knowledge category for the extracted facts",
			},
		},
		"required": []string{"specialist", "content"},
	}
}

func (t *FeedSpecialistTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	specialistName, _ := args["specialist"].(string)
	content, _ := args["content"].(string)
	sourceType, _ := args["source_type"].(string)
	sourceName, _ := args["source_name"].(string)
	sourceDate, _ := args["source_date"].(string)
	sourcePerson, _ := args["source_person"].(string)
	category, _ := args["category"].(string)

	if specialistName == "" || content == "" {
		return ErrorResult("specialist and content are required")
	}

	// Verify specialist exists
	if !t.loader.Exists(specialistName) {
		return ErrorResult(fmt.Sprintf("specialist %q not found", specialistName))
	}

	if t.extractor == nil || t.store == nil {
		return ErrorResult("semantic memory is not enabled — cannot feed specialist")
	}

	opts := memory.KnowledgeIndexOpts{
		Specialist:   specialistName,
		SourceType:   sourceType,
		SourceName:   sourceName,
		SourceDate:   sourceDate,
		SourcePerson: sourcePerson,
	}

	if category == "" {
		category = "contextual"
	}

	// Chunk large content with overlapping windows
	chunks := chunkContent(content, 1500, 200)
	numChunks := len(chunks)

	// Run extraction asynchronously with timeout
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		totalFacts := 0
		for _, chunk := range chunks {
			facts, err := t.extractor.ExtractSpecialistFacts(bgCtx, chunk)
			if err != nil {
				logger.WarnCF("specialist", "Failed to extract facts from chunk", map[string]interface{}{
					"error":      err.Error(),
					"specialist": specialistName,
				})
				continue
			}

			for _, fact := range facts {
				cat := fact.Category
				if cat == "" {
					cat = category
				}
				if err := t.store.IndexKnowledgeWithOpts(bgCtx, "", fact.Fact, cat, opts); err != nil {
					logger.WarnCF("specialist", "Failed to index fact", map[string]interface{}{
						"error": err.Error(),
						"fact":  fact.Fact,
					})
					continue
				}
				totalFacts++
			}
		}
		logger.InfoCF("specialist", "Feed completed", map[string]interface{}{
			"specialist": specialistName,
			"facts":      totalFacts,
			"chunks":     numChunks,
		})
	}()

	summary := fmt.Sprintf("Processing %d chunk(s) for specialist '%s', knowledge will be available shortly.", numChunks, specialistName)
	if sourceName != "" {
		summary += fmt.Sprintf(" Source: %s.", sourceName)
	}

	return SilentResult(summary)
}

// chunkContent splits text into overlapping chunks for processing.
func chunkContent(content string, chunkSize, overlap int) []string {
	runes := []rune(content)
	if len(runes) <= chunkSize {
		return []string{content}
	}

	var chunks []string
	start := 0
	for start < len(runes) {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
		start += chunkSize - overlap
		if start >= len(runes) {
			break
		}
	}

	return chunks
}

// ---------------------------------------------------------------------------
// LinkTopicTool — link/unlink forum topics to specialists
// ---------------------------------------------------------------------------

// LinkTopicTool lets the agent manage topic-to-specialist mappings.
type LinkTopicTool struct {
	topicMappings    *state.TopicMappingStore
	specialistLoader *specialists.SpecialistLoader
	channel          string
	chatID           string
	metadata         map[string]string
}

func NewLinkTopicTool(topicMappings *state.TopicMappingStore, loader *specialists.SpecialistLoader) *LinkTopicTool {
	return &LinkTopicTool{
		topicMappings:    topicMappings,
		specialistLoader: loader,
	}
}

func (t *LinkTopicTool) Name() string { return "link_topic" }

func (t *LinkTopicTool) Description() string {
	desc := "Link or unlink a forum topic to a specialist. When linked, all messages in that topic are handled by the specialist persona."
	all := t.specialistLoader.ListSpecialists()
	if len(all) > 0 {
		var names []string
		for _, s := range all {
			names = append(names, s.Name)
		}
		desc += " Available specialists: " + strings.Join(names, ", ") + "."
	}
	return desc
}

func (t *LinkTopicTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform: link, unlink, or status",
				"enum":        []string{"link", "unlink", "status"},
			},
			"specialist": map[string]interface{}{
				"type":        "string",
				"description": "Name of the specialist to link (required for 'link' action)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *LinkTopicTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

func (t *LinkTopicTool) SetMetadata(metadata map[string]string) {
	t.metadata = metadata
}

func (t *LinkTopicTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, _ := args["action"].(string)
	specialist, _ := args["specialist"].(string)

	if action == "" {
		return ErrorResult("action is required (link, unlink, or status)")
	}

	// Extract thread_id from metadata
	threadID := ""
	if t.metadata != nil {
		threadID = t.metadata["thread_id"]
	}
	if threadID == "" {
		return ErrorResult("This tool must be used from within a forum topic (no thread_id in metadata).")
	}

	chatID := t.chatID
	if chatID == "" {
		return ErrorResult("No chat context available.")
	}

	switch action {
	case "link":
		if specialist == "" {
			// List available specialists
			all := t.specialistLoader.ListSpecialists()
			if len(all) == 0 {
				return ErrorResult("No specialists available. Create one first with create_specialist.")
			}
			var names []string
			for _, s := range all {
				if s.Description != "" {
					names = append(names, fmt.Sprintf("%s (%s)", s.Name, s.Description))
				} else {
					names = append(names, s.Name)
				}
			}
			return ErrorResult("specialist name is required for 'link' action. Available: " + strings.Join(names, ", "))
		}

		if !t.specialistLoader.Exists(specialist) {
			all := t.specialistLoader.ListSpecialists()
			var names []string
			for _, s := range all {
				names = append(names, s.Name)
			}
			return ErrorResult(fmt.Sprintf("Specialist %q not found. Available: %s", specialist, strings.Join(names, ", ")))
		}

		if err := t.topicMappings.SetMapping(chatID, threadID, specialist); err != nil {
			return ErrorResult(fmt.Sprintf("Failed to link topic: %v", err))
		}
		return SilentResult(fmt.Sprintf("Topic (thread %s) linked to specialist '%s'. All messages in this topic will now be handled by the '%s' specialist.", threadID, specialist, specialist))

	case "unlink":
		current := t.topicMappings.LookupSpecialist(chatID, threadID)
		if current == "" {
			return SilentResult("This topic is not linked to any specialist.")
		}
		if err := t.topicMappings.RemoveMapping(chatID, threadID); err != nil {
			return ErrorResult(fmt.Sprintf("Failed to unlink topic: %v", err))
		}
		return SilentResult(fmt.Sprintf("Topic unlinked from specialist '%s'.", current))

	case "status":
		current := t.topicMappings.LookupSpecialist(chatID, threadID)
		if current == "" {
			return SilentResult("This topic is not linked to any specialist.")
		}
		return SilentResult(fmt.Sprintf("This topic is linked to specialist '%s'.", current))

	default:
		return ErrorResult(fmt.Sprintf("Unknown action %q. Use link, unlink, or status.", action))
	}
}
