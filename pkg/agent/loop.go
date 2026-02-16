// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	chromem "github.com/philippgille/chromem-go"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/state"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// thinkTagRe matches <think>...</think> reasoning blocks (including multiline).
var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>\s*`)

func stripThinkingTags(s string) string {
	return strings.TrimSpace(thinkTagRe.ReplaceAllString(s, ""))
}

// stripThinkingTagsForStream strips both closed and unclosed <think> blocks.
// Used during streaming where the closing tag may not have arrived yet.
func stripThinkingTagsForStream(s string) string {
	// Strip closed <think>...</think> blocks
	s = thinkTagRe.ReplaceAllString(s, "")
	// Strip unclosed <think>... at the end (closing tag hasn't arrived yet)
	if idx := strings.LastIndex(s, "<think>"); idx != -1 {
		if !strings.Contains(s[idx:], "</think>") {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}

type AgentLoop struct {
	bus            *bus.MessageBus
	provider       providers.LLMProvider
	workspace      string
	model          string
	contextWindow  int // Maximum context window size in tokens
	maxIterations  int
	sessions       *session.SessionManager
	state          *state.Manager
	contextBuilder *ContextBuilder
	tools          *tools.ToolRegistry
	running        atomic.Bool
	summarizing    sync.Map // Tracks which sessions are currently being summarized
	streamUpdateFn func(channel, chatID string) func(fullText string)
	vectorStore    *memory.VectorStore
	extractor      *memory.KnowledgeExtractor

	// Message injection: routes new messages to active session or pending queue
	pendingMu     sync.Mutex
	pendingMsgs   chan bus.InboundMessage // messages for non-active sessions
	interruptCh   chan bus.InboundMessage // messages for the currently active session
	activeSession string                  // session key being processed right now
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey      string              // Session identifier for history/context
	Channel         string              // Target channel for tool execution
	ChatID          string              // Target chat ID for tool execution
	UserMessage     string              // User message content (may include prefix)
	Media           []media.ContentPart // Multimodal content (images, files)
	DefaultResponse string              // Response when LLM returns empty
	EnableSummary   bool                // Whether to trigger summarization
	SendResponse    bool                // Whether to send response via bus
	NoHistory       bool                // If true, don't load session history (for heartbeat)
}

// createToolRegistry creates a tool registry with common tools.
// This is shared between main agent and subagents.
func createToolRegistry(workspace string, restrict bool, cfg *config.Config, msgBus *bus.MessageBus, vectorStore *memory.VectorStore) *tools.ToolRegistry {
	registry := tools.NewToolRegistry()

	// File system tools
	registry.Register(tools.NewReadFileTool(workspace, restrict))
	registry.Register(tools.NewWriteFileTool(workspace, restrict))
	registry.Register(tools.NewListDirTool(workspace, restrict))
	registry.Register(tools.NewEditFileTool(workspace, restrict))
	registry.Register(tools.NewAppendFileTool(workspace, restrict))

	// Shell execution
	registry.Register(tools.NewExecTool(workspace, restrict))

	if searchTool := tools.NewWebSearchTool(tools.WebSearchToolOptions{
		BraveAPIKey:          cfg.Tools.Web.Brave.APIKey,
		BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
		BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
		DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
		DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
	}); searchTool != nil {
		registry.Register(searchTool)
	}
	registry.Register(tools.NewWebFetchTool(50000))

	// Moodle (QM+) tool
	if cfg.Tools.Moodle.Enabled {
		registry.Register(tools.NewMoodleTool(tools.MoodleToolOptions{
			BaseURL:      cfg.Tools.Moodle.URL,
			Token:        cfg.Tools.Moodle.Token,
			M365Username: cfg.Tools.Moodle.M365Username,
			M365Password: cfg.Tools.Moodle.M365Password,
			OnTokenRefresh: func(newToken string) {
				cfg.Tools.Moodle.Token = newToken
				logger.Info("Moodle token refreshed via SSO")
			},
		}))
	}

	// Email (M365 Outlook) tool
	if cfg.Tools.Email.Enabled {
		registry.Register(tools.NewEmailTool(tools.EmailToolOptions{
			EmailAddress: cfg.Tools.Email.Address,
		}))
	}

	// Semantic memory search
	if vectorStore != nil {
		registry.Register(tools.NewMemorySearchTool(vectorStore))
	}

	// Hardware tools (I2C, SPI) - Linux only, returns error on other platforms
	registry.Register(tools.NewI2CTool())
	registry.Register(tools.NewSPITool())

	// Message tool - available to both agent and subagent
	// Subagent uses it to communicate directly with user
	messageTool := tools.NewMessageTool()
	messageTool.SetSendCallback(func(channel, chatID, content string) error {
		msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
		})
		return nil
	})
	registry.Register(messageTool)

	return registry
}

func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus, provider providers.LLMProvider) *AgentLoop {
	workspace := cfg.WorkspacePath()
	os.MkdirAll(workspace, 0755)

	restrict := cfg.Agents.Defaults.RestrictToWorkspace

	// Initialize semantic memory (vector store) if configured
	var vectorStore *memory.VectorStore
	var extractor *memory.KnowledgeExtractor

	if cfg.Tools.Memory.SemanticSearch {
		embeddingFn := resolveEmbeddingFunc(cfg)
		if embeddingFn != nil {
			vs, err := memory.NewVectorStore(workspace, embeddingFn)
			if err != nil {
				logger.WarnCF("agent", "Failed to initialize vector store, semantic memory disabled", map[string]interface{}{
					"error": err.Error(),
				})
			} else {
				vectorStore = vs
				if cfg.Tools.Memory.KnowledgeExtract {
					extractor = memory.NewKnowledgeExtractor(provider, cfg.Agents.Defaults.Model, vs)
				}
				logger.InfoCF("agent", "Semantic memory initialized", map[string]interface{}{
					"knowledge_extract": cfg.Tools.Memory.KnowledgeExtract,
				})
			}
		} else {
			logger.InfoCF("agent", "No embedding API key available, semantic memory disabled", nil)
		}
	}

	// Create tool registry for main agent
	toolsRegistry := createToolRegistry(workspace, restrict, cfg, msgBus, vectorStore)

	// Create subagent manager with its own tool registry
	subagentManager := tools.NewSubagentManager(provider, cfg.Agents.Defaults.Model, workspace, msgBus)
	subagentTools := createToolRegistry(workspace, restrict, cfg, msgBus, vectorStore)
	// Subagent doesn't need spawn/subagent tools to avoid recursion
	subagentManager.SetTools(subagentTools)

	// Register spawn tool (for main agent)
	spawnTool := tools.NewSpawnTool(subagentManager)
	toolsRegistry.Register(spawnTool)

	// Register subagent tool (synchronous execution)
	subagentTool := tools.NewSubagentTool(subagentManager)
	toolsRegistry.Register(subagentTool)

	sessionsManager := session.NewSessionManager(filepath.Join(workspace, "sessions"))

	// Create state manager for atomic state persistence
	stateManager := state.NewManager(workspace)

	// Create context builder and set tools registry
	contextBuilder := NewContextBuilder(workspace)
	contextBuilder.SetToolsRegistry(toolsRegistry)

	return &AgentLoop{
		bus:            msgBus,
		provider:       provider,
		workspace:      workspace,
		model:          cfg.Agents.Defaults.Model,
		contextWindow:  cfg.Agents.Defaults.MaxTokens, // Restore context window for summarization
		maxIterations:  cfg.Agents.Defaults.MaxToolIterations,
		sessions:       sessionsManager,
		state:          stateManager,
		contextBuilder: contextBuilder,
		tools:          toolsRegistry,
		summarizing:    sync.Map{},
		vectorStore:    vectorStore,
		extractor:      extractor,
	}
}

func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)
	al.pendingMsgs = make(chan bus.InboundMessage, 100)
	al.interruptCh = make(chan bus.InboundMessage, 10)

	// Start router goroutine: reads from bus and routes to pending or interrupt
	go al.routeMessages(ctx)

	for al.running.Load() {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-al.pendingMsgs:
			if !ok {
				return nil
			}

			// Mark this session as active so new messages for it go to interruptCh
			al.pendingMu.Lock()
			al.activeSession = msg.SessionKey
			al.pendingMu.Unlock()

			response, err := al.processMessage(ctx, msg)
			if err != nil {
				response = fmt.Sprintf("Error processing message: %v", err)
			}

			// Clear active session
			al.pendingMu.Lock()
			al.activeSession = ""
			al.pendingMu.Unlock()

			if response != "" {
				// Check if the message tool already sent a response during this round.
				alreadySent := false
				if tool, ok := al.tools.Get("message"); ok {
					if mt, ok := tool.(*tools.MessageTool); ok {
						alreadySent = mt.HasSentInRound()
					}
				}

				if !alreadySent {
					al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: msg.Channel,
						ChatID:  msg.ChatID,
						Content: response,
					})
				}
			}
		}
	}

	return nil
}

// routeMessages reads from the bus and routes messages to either the interrupt
// channel (if the message targets the currently active session) or the pending
// queue (for normal sequential processing).
func (al *AgentLoop) routeMessages(ctx context.Context) {
	for {
		msg, ok := al.bus.ConsumeInbound(ctx)
		if !ok {
			return
		}

		al.pendingMu.Lock()
		active := al.activeSession
		al.pendingMu.Unlock()

		if active != "" && msg.SessionKey == active {
			// This message targets the session currently being processed — inject it
			logger.InfoCF("agent", "Routing message to interrupt channel",
				map[string]interface{}{
					"session_key": msg.SessionKey,
					"preview":     utils.Truncate(msg.Content, 60),
				})
			select {
			case al.interruptCh <- msg:
			default:
				// Buffer full — fall through to pending
				al.pendingMsgs <- msg
			}
		} else {
			al.pendingMsgs <- msg
		}
	}
}

func (al *AgentLoop) Stop() {
	al.running.Store(false)
}

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	al.tools.Register(tool)
}

// RecordLastChannel records the last active channel for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChannel(channel string) error {
	return al.state.SetLastChannel(channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChatID(chatID string) error {
	return al.state.SetLastChatID(chatID)
}

func (al *AgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return al.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

func (al *AgentLoop) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	return al.processMessage(ctx, msg)
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
func (al *AgentLoop) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      "heartbeat",
		Channel:         channel,
		ChatID:          chatID,
		UserMessage:     content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       true, // Don't load session history for heartbeat
	})
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(msg.Content, 80)
	}
	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]interface{}{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		})

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg)
	}

	// Handle /model command — lets user switch model mid-session
	if resp, handled := al.handleModelCommand(msg.Content); handled {
		return resp, nil
	}

	// Process as user message
	return al.runAgentLoop(ctx, processOptions{
		SessionKey:      msg.SessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		Media:           msg.Media,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
	})
}

func (al *AgentLoop) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Verify this is a system message
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]interface{}{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
	} else {
		// Fallback
		originChannel = "cli"
	}

	// Extract subagent result from message content
	// Format: "Task 'label' completed.\n\nResult:\n<actual content>"
	content := msg.Content
	if idx := strings.Index(content, "Result:\n"); idx >= 0 {
		content = content[idx+8:] // Extract just the result part
	}

	// Skip internal channels - only log, don't send to user
	if constants.IsInternalChannel(originChannel) {
		logger.InfoCF("agent", "Subagent completed (internal channel)",
			map[string]interface{}{
				"sender_id":   msg.SenderID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	// Agent acts as dispatcher only - subagent handles user interaction via message tool
	// Don't forward result here, subagent should use message tool to communicate with user
	logger.InfoCF("agent", "Subagent completed",
		map[string]interface{}{
			"sender_id":   msg.SenderID,
			"channel":     originChannel,
			"content_len": len(content),
		})

	// Agent only logs, does not respond to user
	return "", nil
}

// handleModelCommand intercepts /model commands from the user.
// /model — shows current model
// /model <name> — switches to a different model
func (al *AgentLoop) handleModelCommand(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "/model") {
		return "", false
	}

	parts := strings.Fields(trimmed)
	if len(parts) == 1 {
		// Just "/model" — show current
		return fmt.Sprintf("Current model: `%s`", al.model), true
	}

	newModel := parts[1]
	oldModel := al.model
	al.model = newModel
	logger.InfoCF("agent", fmt.Sprintf("Model switched: %s -> %s", oldModel, newModel), nil)
	return fmt.Sprintf("Model switched: `%s` -> `%s`", oldModel, newModel), true
}

// SetModel changes the active model at runtime.
func (al *AgentLoop) SetModel(model string) {
	al.model = model
}

// GetModel returns the current active model.
func (al *AgentLoop) GetModel() string {
	return al.model
}

// SetStreamUpdater sets the function used to create streaming update callbacks
// for channels that support progressive message editing.
func (al *AgentLoop) SetStreamUpdater(fn func(channel, chatID string) func(fullText string)) {
	al.streamUpdateFn = fn
}

// runAgentLoop is the core message processing logic.
// It handles context building, LLM calls, tool execution, and response handling.
func (al *AgentLoop) runAgentLoop(ctx context.Context, opts processOptions) (string, error) {
	// 0. Record last channel for heartbeat notifications (skip internal channels)
	if opts.Channel != "" && opts.ChatID != "" {
		// Don't record internal channels (cli, system, subagent)
		if !constants.IsInternalChannel(opts.Channel) {
			channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
			if err := al.RecordLastChannel(channelKey); err != nil {
				logger.WarnCF("agent", "Failed to record last channel: %v", map[string]interface{}{"error": err.Error()})
			}
		}
	}

	// 1. Update tool contexts
	al.updateToolContexts(opts.Channel, opts.ChatID)

	// 2. Build messages (skip history for heartbeat)
	var history []providers.Message
	var summary string
	if !opts.NoHistory {
		history = al.sessions.GetHistory(opts.SessionKey)
		summary = al.sessions.GetSummary(opts.SessionKey)
	}
	messages := al.contextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		opts.Media,
		opts.Channel,
		opts.ChatID,
	)

	// 3. Save user message to session
	al.sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

	// 4. Run LLM iteration loop
	finalContent, iteration, err := al.runLLMIteration(ctx, messages, opts)
	if err != nil {
		return "", err
	}

	// If last tool had ForUser content and we already sent it, we might not need to send final response
	// This is controlled by the tool's Silent flag and ForUser content

	// 5. Handle empty response
	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	// 6. Save final assistant message to session
	al.sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
	al.sessions.Save(opts.SessionKey)

	// 7. Async: index conversation and extract knowledge
	if al.vectorStore != nil && !opts.NoHistory {
		go al.vectorStore.IndexConversation(context.Background(), opts.SessionKey, opts.Channel, opts.ChatID, opts.UserMessage, finalContent)
		if al.extractor != nil {
			go al.extractor.ExtractAndConsolidate(context.Background(), opts.UserMessage, finalContent, opts.SessionKey)
		}
	}

	// 8. Optional: summarization
	if opts.EnableSummary {
		al.maybeSummarize(opts.SessionKey)
	}

	// 9. Optional: send response via bus
	if opts.SendResponse {
		al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: finalContent,
		})
	}

	// 10. Log response
	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]interface{}{
			"session_key":  opts.SessionKey,
			"iterations":   iteration,
			"final_length": len(finalContent),
		})

	return finalContent, nil
}

// runLLMIteration executes the LLM call loop with tool handling.
// Returns the final content, iteration count, and any error.
func (al *AgentLoop) runLLMIteration(ctx context.Context, messages []providers.Message, opts processOptions) (string, int, error) {
	iteration := 0
	var finalContent string

	for iteration < al.maxIterations {
		iteration++

		// Check for injected messages at each iteration boundary
		messages = al.drainInterrupts(messages, opts.SessionKey)

		logger.DebugCF("agent", "LLM iteration",
			map[string]interface{}{
				"iteration": iteration,
				"max":       al.maxIterations,
			})

		// Build tool definitions
		providerToolDefs := al.tools.ToProviderDefs()

		// Log LLM request details
		logger.DebugCF("agent", "LLM request",
			map[string]interface{}{
				"iteration":         iteration,
				"model":             al.model,
				"messages_count":    len(messages),
				"tools_count":       len(providerToolDefs),
				"max_tokens":        8192,
				"temperature":       0.7,
				"system_prompt_len": len(messages[0].Content),
			})

		// Log full messages (detailed)
		logger.DebugCF("agent", "Full LLM request",
			map[string]interface{}{
				"iteration":     iteration,
				"messages_json": formatMessagesForLog(messages),
				"tools_json":    formatToolsForLog(providerToolDefs),
			})

		// Call LLM (with streaming if available)
		llmOpts := map[string]interface{}{
			"max_tokens":  8192,
			"temperature": 0.7,
		}

		var response *providers.LLMResponse
		var err error
		var notifier *bus.StreamNotifier

		sp, canStream := al.provider.(providers.StreamingProvider)
		var streamCb func(fullText string)
		if canStream && al.streamUpdateFn != nil {
			streamCb = al.streamUpdateFn(opts.Channel, opts.ChatID)
		}

		if canStream && streamCb != nil {
			// Wrap callback to strip <think> blocks before they reach the channel
			filteredCb := func(fullText string) {
				cleaned := stripThinkingTagsForStream(fullText)
				if cleaned != "" {
					streamCb(cleaned)
				}
			}
			notifier = bus.NewStreamNotifier(1500*time.Millisecond, filteredCb)
			response, err = sp.ChatStream(ctx, messages, providerToolDefs, al.model, llmOpts, func(delta string) {
				notifier.Append(delta)
			})
			notifier.Flush()
		} else {
			response, err = al.provider.Chat(ctx, messages, providerToolDefs, al.model, llmOpts)
		}

		if err != nil {
			logger.ErrorCF("agent", "LLM call failed",
				map[string]interface{}{
					"iteration": iteration,
					"error":     err.Error(),
				})
			return "", iteration, fmt.Errorf("LLM call failed: %w", err)
		}

		// Strip <think>...</think> reasoning blocks (e.g. MiniMax, DeepSeek)
		response.Content = stripThinkingTags(response.Content)

		// Check if no tool calls - we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content

			// Edge case: LLM gave a final answer, but a new user message arrived.
			// Save the assistant answer, inject the new message, and continue the loop.
			messages = append(messages, providers.Message{Role: "assistant", Content: finalContent})
			al.sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
			injected := al.drainInterrupts(messages, opts.SessionKey)
			if len(injected) > len(messages) {
				// New messages were injected — send current answer and continue
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.ChatID,
					Content: finalContent,
				})
				messages = injected
				finalContent = ""
				continue
			}
			// No interrupts — remove the assistant message we just appended
			// (it will be saved by the caller in runAgentLoop step 6)
			messages = messages[:len(messages)-1]

			logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
				map[string]interface{}{
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})
			break
		}

		// Log tool calls
		toolNames := make([]string, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("agent", "LLM requested tool calls",
			map[string]interface{}{
				"tools":     toolNames,
				"count":     len(response.ToolCalls),
				"iteration": iteration,
			})

		// Build assistant message with tool calls
		assistantMsg := providers.Message{
			Role:    "assistant",
			Content: response.Content,
		}
		for _, tc := range response.ToolCalls {
			argumentsJSON, _ := json.Marshal(tc.Arguments)
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      tc.Name,
					Arguments: string(argumentsJSON),
				},
			})
		}
		messages = append(messages, assistantMsg)

		// Save assistant message with tool calls to session
		al.sessions.AddFullMessage(opts.SessionKey, assistantMsg)

		// Execute tool calls
		for _, tc := range response.ToolCalls {
			// Log tool call with arguments preview
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]interface{}{
					"tool":      tc.Name,
					"iteration": iteration,
				})

			// Create async callback for tools that implement AsyncTool
			asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
				if !result.Silent && result.ForUser != "" {
					logger.InfoCF("agent", "Async tool completed, agent will handle notification",
						map[string]interface{}{
							"tool":        tc.Name,
							"content_len": len(result.ForUser),
						})
				}
			}

			toolResult := al.tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, opts.Channel, opts.ChatID, asyncCallback)

			// Send ForUser content to user immediately if not Silent
			if !toolResult.Silent && toolResult.ForUser != "" && opts.SendResponse {
				al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.ChatID,
					Content: toolResult.ForUser,
				})
				logger.DebugCF("agent", "Sent tool result to user",
					map[string]interface{}{
						"tool":        tc.Name,
						"content_len": len(toolResult.ForUser),
					})
			}

			// Determine content for LLM based on tool result
			contentForLLM := toolResult.ForLLM
			if contentForLLM == "" && toolResult.Err != nil {
				contentForLLM = toolResult.Err.Error()
			}

			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			// Save tool result message to session
			al.sessions.AddFullMessage(opts.SessionKey, toolResultMsg)
		}
	}

	return finalContent, iteration, nil
}

// drainInterrupts non-blocking reads all pending messages from interruptCh
// and appends them as user messages to the conversation. Returns the updated
// messages slice (unchanged if no interrupts).
func (al *AgentLoop) drainInterrupts(messages []providers.Message, sessionKey string) []providers.Message {
	if al.interruptCh == nil {
		return messages
	}

	injected := false
	for {
		select {
		case msg := <-al.interruptCh:
			userMsg := providers.Message{
				Role:    "user",
				Content: msg.Content,
			}
			if len(msg.Media) > 0 {
				userMsg.ContentParts = msg.Media
			}
			messages = append(messages, userMsg)
			al.sessions.AddMessage(sessionKey, "user", msg.Content)
			injected = true
			logger.InfoCF("agent", "Injected interrupt message into conversation",
				map[string]interface{}{
					"session_key": sessionKey,
					"preview":     utils.Truncate(msg.Content, 60),
				})
		default:
			if injected {
				logger.InfoCF("agent", "Interrupt injection complete",
					map[string]interface{}{
						"total_messages": len(messages),
					})
			}
			return messages
		}
	}
}

// updateToolContexts updates the context for tools that need channel/chatID info.
func (al *AgentLoop) updateToolContexts(channel, chatID string) {
	// Use ContextualTool interface instead of type assertions
	if tool, ok := al.tools.Get("message"); ok {
		if mt, ok := tool.(tools.ContextualTool); ok {
			mt.SetContext(channel, chatID)
		}
	}
	if tool, ok := al.tools.Get("spawn"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
	if tool, ok := al.tools.Get("subagent"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
func (al *AgentLoop) maybeSummarize(sessionKey string) {
	newHistory := al.sessions.GetHistory(sessionKey)
	tokenEstimate := al.estimateTokens(newHistory)
	threshold := al.contextWindow * 75 / 100

	if len(newHistory) > 20 || tokenEstimate > threshold {
		if _, loading := al.summarizing.LoadOrStore(sessionKey, true); !loading {
			go func() {
				defer al.summarizing.Delete(sessionKey)
				al.summarizeSession(sessionKey)
			}()
		}
	}
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]interface{} {
	info := make(map[string]interface{})

	// Tools info
	tools := al.tools.List()
	info["tools"] = map[string]interface{}{
		"count": len(tools),
		"names": tools,
	}

	// Skills info
	info["skills"] = al.contextBuilder.GetSkillsInfo()

	return info
}

// formatMessagesForLog formats messages for logging
func formatMessagesForLog(messages []providers.Message) string {
	if len(messages) == 0 {
		return "[]"
	}

	var result string
	result += "[\n"
	for i, msg := range messages {
		result += fmt.Sprintf("  [%d] Role: %s\n", i, msg.Role)
		if msg.ToolCalls != nil && len(msg.ToolCalls) > 0 {
			result += "  ToolCalls:\n"
			for _, tc := range msg.ToolCalls {
				result += fmt.Sprintf("    - ID: %s, Type: %s, Name: %s\n", tc.ID, tc.Type, tc.Name)
				if tc.Function != nil {
					result += fmt.Sprintf("      Arguments: %s\n", utils.Truncate(tc.Function.Arguments, 200))
				}
			}
		}
		if msg.Content != "" {
			content := utils.Truncate(msg.Content, 200)
			result += fmt.Sprintf("  Content: %s\n", content)
		}
		if msg.ToolCallID != "" {
			result += fmt.Sprintf("  ToolCallID: %s\n", msg.ToolCallID)
		}
		result += "\n"
	}
	result += "]"
	return result
}

// formatToolsForLog formats tool definitions for logging
func formatToolsForLog(tools []providers.ToolDefinition) string {
	if len(tools) == 0 {
		return "[]"
	}

	var result string
	result += "[\n"
	for i, tool := range tools {
		result += fmt.Sprintf("  [%d] Type: %s, Name: %s\n", i, tool.Type, tool.Function.Name)
		result += fmt.Sprintf("      Description: %s\n", tool.Function.Description)
		if len(tool.Function.Parameters) > 0 {
			result += fmt.Sprintf("      Parameters: %s\n", utils.Truncate(fmt.Sprintf("%v", tool.Function.Parameters), 200))
		}
	}
	result += "]"
	return result
}

// summarizeSession summarizes the conversation history for a session.
func (al *AgentLoop) summarizeSession(sessionKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := al.sessions.GetHistory(sessionKey)
	summary := al.sessions.GetSummary(sessionKey)

	// Keep last 4 messages for continuity
	if len(history) <= 4 {
		return
	}

	toSummarize := history[:len(history)-4]

	// Oversized Message Guard
	// Skip messages larger than 50% of context window to prevent summarizer overflow
	maxMessageTokens := al.contextWindow / 2
	validMessages := make([]providers.Message, 0)
	omitted := false

	for _, m := range toSummarize {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		// Estimate tokens for this message
		msgTokens := len(m.Content) / 4
		if msgTokens > maxMessageTokens {
			omitted = true
			continue
		}
		validMessages = append(validMessages, m)
	}

	if len(validMessages) == 0 {
		return
	}

	// Multi-Part Summarization
	// Split into two parts if history is significant
	var finalSummary string
	if len(validMessages) > 10 {
		mid := len(validMessages) / 2
		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, _ := al.summarizeBatch(ctx, part1, "")
		s2, _ := al.summarizeBatch(ctx, part2, "")

		// Merge them
		mergePrompt := fmt.Sprintf("Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s", s1, s2)
		resp, err := al.provider.Chat(ctx, []providers.Message{{Role: "user", Content: mergePrompt}}, nil, al.model, map[string]interface{}{
			"max_tokens":  1024,
			"temperature": 0.3,
		})
		if err == nil {
			finalSummary = resp.Content
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = al.summarizeBatch(ctx, validMessages, summary)
	}

	if omitted && finalSummary != "" {
		finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}

	if finalSummary != "" {
		al.sessions.SetSummary(sessionKey, finalSummary)
		al.sessions.TruncateHistory(sessionKey, 4)
		al.sessions.Save(sessionKey)
	}
}

// summarizeBatch summarizes a batch of messages.
func (al *AgentLoop) summarizeBatch(ctx context.Context, batch []providers.Message, existingSummary string) (string, error) {
	prompt := "Provide a concise summary of this conversation segment, preserving core context and key points.\n"
	if existingSummary != "" {
		prompt += "Existing context: " + existingSummary + "\n"
	}
	prompt += "\nCONVERSATION:\n"
	for _, m := range batch {
		prompt += fmt.Sprintf("%s: %s\n", m.Role, m.Content)
	}

	response, err := al.provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, al.model, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.3,
	})
	if err != nil {
		return "", err
	}
	return response.Content, nil
}

// estimateTokens estimates the number of tokens in a message list.
// Uses rune count instead of byte length so that CJK and other multi-byte
// characters are not over-counted (a Chinese character is 3 bytes but roughly
// one token).
func (al *AgentLoop) estimateTokens(messages []providers.Message) int {
	total := 0
	for _, m := range messages {
		total += utf8.RuneCountInString(m.Content) / 3
	}
	return total
}

// resolveEmbeddingFunc returns an OpenAI embedding function if an API key is available.
// Tries OpenAI key first, then OpenRouter as OpenAI-compatible fallback.
// Returns nil if no key is available.
func resolveEmbeddingFunc(cfg *config.Config) chromem.EmbeddingFunc {
	model := cfg.Tools.Memory.EmbeddingModel
	if model == "" {
		model = "text-embedding-3-small"
	}

	// Try direct OpenAI key
	if cfg.Providers.OpenAI.APIKey != "" {
		return chromem.NewEmbeddingFuncOpenAI(cfg.Providers.OpenAI.APIKey, chromem.EmbeddingModelOpenAI(model))
	}

	// Try OpenRouter as OpenAI-compatible endpoint
	// OpenRouter requires "openai/" prefix for OpenAI embedding models
	if cfg.Providers.OpenRouter.APIKey != "" {
		baseURL := cfg.Providers.OpenRouter.APIBase
		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}
		orModel := model
		if !strings.Contains(orModel, "/") {
			orModel = "openai/" + orModel
		}
		return chromem.NewEmbeddingFuncOpenAICompat(baseURL, cfg.Providers.OpenRouter.APIKey, orModel, nil)
	}

	return nil
}
