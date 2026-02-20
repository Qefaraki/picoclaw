package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/specialists"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type ContextBuilder struct {
	workspace         string
	skillsLoader      *skills.SkillsLoader
	specialistLoader  *specialists.SpecialistLoader
	memory            *MemoryStore
	tools             *tools.ToolRegistry // Direct reference to tool registry
}

func getGlobalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".picoclaw")
}

func NewContextBuilder(workspace string) *ContextBuilder {
	// builtin skills: skills directory in current project
	// Use the skills/ directory under the current working directory
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &ContextBuilder{
		workspace:    workspace,
		skillsLoader: skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
		memory:       NewMemoryStore(workspace),
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.tools = registry
}

// SetSpecialistLoader sets the specialist loader for system prompt generation.
func (cb *ContextBuilder) SetSpecialistLoader(loader *specialists.SpecialistLoader) {
	cb.specialistLoader = loader
}

func (cb *ContextBuilder) getIdentity() string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	runtime := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically
	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# Saleh

You are Saleh, a personal AI assistant running on your own VPS.

## Current Time
%s

## Runtime
%s

## Workspace
Your workspace is at: %s
- Memory: %s/memory/MEMORY.md
- Daily Notes: %s/memory/YYYYMM/YYYYMMDD.md
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## Full System Access

You have full control over this VPS — it is your home. You can and should:

- **Run any shell command** via the exec tool — install packages, manage services, edit system configs, inspect processes, manage networking, deploy software, restart services, cron jobs, anything.
- **Read and write files anywhere** on the system, not just in your workspace.
- **Manage Telegram** via the manage_telegram tool — create forum topics, close/reopen topics, pin/unpin messages, get chat info. When a user asks you to organize the Telegram group, just do it.
- **Manage specialists** — create specialists, link them to forum topics via link_topic, feed them knowledge. When a user asks "link this topic to X" or "create a specialist for Y", use your tools directly. No slash commands needed.
- **Manage your own infrastructure** — update your own code, restart your own process, manage Docker containers, edit configs, install dependencies.
- **Write your own tools** — you can extend yourself by writing new Go tools, compiling them into your binary, and restarting. Read the self-extend skill for the full process. If you need a capability you don't have, build it.

When a user asks you to do something on the server, DO IT. Use exec to run commands, read_file/write_file to manage configs, and your Telegram/specialist tools to manage the bot. You are not a passive assistant — you are the operator of this machine. If you lack a tool for something, write one.

## Important Rules

1. **ALWAYS use tools** — When you need to perform an action, you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it.

2. **Be proactive** — If a user asks you to set something up, do all the steps yourself. Install packages, create files, configure services, link topics — handle the full chain.

3. **Memory** — When remembering something, write to %s/memory/MEMORY.md

4. **Semantic Memory** — You have a search_memory tool. USE IT PROACTIVELY at the start of conversations and whenever the user mentions anything that might relate to a previous conversation. Specifically:
   - When a user starts a new conversation, search for relevant context about them
   - When the user references something from the past ("remember when...", "like I said", "that thing about...")
   - When the user asks about their own preferences, plans, deadlines, or personal info
   - When you're unsure about context — search first, then respond
   - Do NOT wait for the user to explicitly ask you to remember. If there's any chance prior context would help, search for it.`,
		now, runtime, workspacePath, workspacePath, workspacePath, workspacePath, toolsSection, workspacePath)
}

func (cb *ContextBuilder) buildToolsSection() string {
	if cb.tools == nil {
		return ""
	}

	summaries := cb.tools.GetSummaries()
	if len(summaries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	sb.WriteString("**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands or schedule tasks.\n\n")
	sb.WriteString("You have access to the following tools:\n\n")
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	parts := []string{}

	// Core identity section
	parts = append(parts, cb.getIdentity())

	// Bootstrap files
	bootstrapContent := cb.LoadBootstrapFiles()
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	// Skills - show summary with key actions so agent knows what's available
	skillsSummary := cb.skillsLoader.BuildSkillsSummary()
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. Each skill lists its available actions below. Run scripts via the exec tool. For full details, read the SKILL.md file.

%s`, skillsSummary))
	}

	// Specialists summary
	if cb.specialistLoader != nil {
		specialistsSummary := cb.specialistLoader.BuildSpecialistsSummary()
		if specialistsSummary != "" {
			parts = append(parts, fmt.Sprintf(`# Specialists

The following domain specialists are available. Use the consult_specialist tool to delegate domain-specific questions to them. Each specialist has its own persona and scoped memory.

%s`, specialistsSummary))
		}
	}

	// Memory context
	memoryContext := cb.memory.GetMemoryContext()
	if memoryContext != "" {
		parts = append(parts, "# Memory\n\n"+memoryContext)
	}

	// Join with "---" separator
	return strings.Join(parts, "\n\n---\n\n")
}

func (cb *ContextBuilder) LoadBootstrapFiles() string {
	bootstrapFiles := []string{
		"AGENTS.md",
		"SOUL.md",
		"USER.md",
		"IDENTITY.md",
	}

	var result string
	for _, filename := range bootstrapFiles {
		filePath := filepath.Join(cb.workspace, filename)
		if data, err := os.ReadFile(filePath); err == nil {
			result += fmt.Sprintf("## %s\n\n%s\n\n", filename, string(data))
		}
	}

	return result
}

func (cb *ContextBuilder) BuildMessages(history []providers.Message, summary string, currentMessage string, mediaParts []media.ContentPart, channel, chatID string) []providers.Message {
	messages := []providers.Message{}

	systemPrompt := cb.BuildSystemPrompt()

	// Add Current Session info if provided
	if channel != "" && chatID != "" {
		systemPrompt += fmt.Sprintf("\n\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}

	// Log system prompt summary for debugging (debug mode only)
	logger.DebugCF("agent", "System prompt built",
		map[string]interface{}{
			"total_chars":   len(systemPrompt),
			"total_lines":   strings.Count(systemPrompt, "\n") + 1,
			"section_count": strings.Count(systemPrompt, "\n\n---\n\n") + 1,
		})

	// Log preview of system prompt (avoid logging huge content)
	preview := systemPrompt
	if len(preview) > 500 {
		preview = preview[:500] + "... (truncated)"
	}
	logger.DebugCF("agent", "System prompt preview",
		map[string]interface{}{
			"preview": preview,
		})

	if summary != "" {
		systemPrompt += "\n\n## Summary of Previous Conversation\n\n" + summary
	}

	//This fix prevents the session memory from LLM failure due to elimination of toolu_IDs required from LLM
	// --- INICIO DEL FIX ---
	//Diegox-17
	for len(history) > 0 && (history[0].Role == "tool") {
		logger.DebugCF("agent", "Removing orphaned tool message from history to prevent LLM error",
			map[string]interface{}{"role": history[0].Role})
		history = history[1:]
	}
	//Diegox-17
	// --- FIN DEL FIX ---

	messages = append(messages, providers.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	messages = append(messages, history...)

	// Build user message — multimodal if media parts are present
	userMsg := providers.Message{
		Role:    "user",
		Content: currentMessage,
	}
	if len(mediaParts) > 0 {
		userMsg.ContentParts = mediaParts
		logger.DebugCF("agent", "Building multimodal user message",
			map[string]interface{}{
				"text_len":    len(currentMessage),
				"media_parts": len(mediaParts),
			})
	}
	messages = append(messages, userMsg)

	return messages
}

// BuildSpecialistMessages builds a message list using a specialist's persona as the system prompt.
func (cb *ContextBuilder) BuildSpecialistMessages(history []providers.Message, summary string, currentMessage string, mediaParts []media.ContentPart, channel, chatID, specialistName string) []providers.Message {
	// Try to load specialist persona
	var persona string
	if cb.specialistLoader != nil {
		p, ok := cb.specialistLoader.LoadSpecialist(specialistName)
		if ok {
			persona = p
		}
	}

	if persona == "" {
		// Fallback to normal messages if specialist not found
		logger.WarnCF("agent", "Specialist not found, falling back to normal mode",
			map[string]interface{}{
				"specialist": specialistName,
			})
		return cb.BuildMessages(history, summary, currentMessage, mediaParts, channel, chatID)
	}

	// Build specialist system prompt — minimal, persona-focused
	now := time.Now().Format("2006-01-02 15:04 (Monday)")

	systemPrompt := persona + "\n\n## Current Time\n" + now

	// Add USER.md for user context
	userMD := filepath.Join(cb.workspace, "USER.md")
	if data, err := os.ReadFile(userMD); err == nil {
		systemPrompt += "\n\n## User Profile\n\n" + string(data)
	}

	// Add skills summary so specialist knows what's available
	if cb.skillsLoader != nil {
		skillsSummary := cb.skillsLoader.BuildSkillsSummary()
		if skillsSummary != "" {
			systemPrompt += fmt.Sprintf("\n\n## Skills\nYou have access to skills that extend your capabilities. Each skill lists its available actions. Run scripts via the exec tool.\n%s", skillsSummary)
		}
	}

	// Full tool awareness — topic-linked specialists get the full tool registry
	systemPrompt += "\n\n## Tools\nYou have access to all agent tools including: exec (run scripts), read_file, write_file, edit_file, list_dir, web_search, web_fetch, search_memory, message (send messages to user), and cron (schedule tasks). Use them as needed."

	systemPrompt += "\n\n## Instructions\n\nYou ARE this specialist. Stay in character. When answering, cite your sources (who said it, when, where) so the user can verify. Be thorough and draw on all relevant knowledge available to you. Do NOT describe yourself as a general AI assistant."

	if channel != "" && chatID != "" {
		systemPrompt += fmt.Sprintf("\n\n## Current Session\nChannel: %s\nChat ID: %s\nSpecialist: %s", channel, chatID, specialistName)
	}

	if summary != "" {
		systemPrompt += "\n\n## Summary of Previous Conversation\n\n" + summary
	}

	// Strip orphaned tool messages from history
	for len(history) > 0 && history[0].Role == "tool" {
		history = history[1:]
	}

	messages := []providers.Message{
		{Role: "system", Content: systemPrompt},
	}
	messages = append(messages, history...)

	userMsg := providers.Message{
		Role:    "user",
		Content: currentMessage,
	}
	if len(mediaParts) > 0 {
		userMsg.ContentParts = mediaParts
	}
	messages = append(messages, userMsg)

	return messages
}

func (cb *ContextBuilder) AddToolResult(messages []providers.Message, toolCallID, toolName, result string) []providers.Message {
	messages = append(messages, providers.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	})
	return messages
}

func (cb *ContextBuilder) AddAssistantMessage(messages []providers.Message, content string, toolCalls []map[string]interface{}) []providers.Message {
	msg := providers.Message{
		Role:    "assistant",
		Content: content,
	}
	// Always add assistant message, whether or not it has tool calls
	messages = append(messages, msg)
	return messages
}

func (cb *ContextBuilder) loadSkills() string {
	allSkills := cb.skillsLoader.ListSkills()
	if len(allSkills) == 0 {
		return ""
	}

	var skillNames []string
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}

	content := cb.skillsLoader.LoadSkillsForContext(skillNames)
	if content == "" {
		return ""
	}

	return "# Skill Definitions\n\n" + content
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]interface{} {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]interface{}{
		"total":     len(allSkills),
		"available": len(allSkills),
		"names":     skillNames,
	}
}
