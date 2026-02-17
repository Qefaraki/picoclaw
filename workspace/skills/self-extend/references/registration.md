# Tool Registration Reference

## Where Tools Get Registered

There are two places to register tools:

### 1. `pkg/agent/loop.go` — `NewAgentLoop()` function

This is where most tools live. The function creates the tool registry and registers all built-in tools. Look for the block that starts with `createToolRegistry()` and the individual `toolsRegistry.Register()` calls after it.

**Use this for:** Tools that only need workspace path, config values, or other things available inside `NewAgentLoop`.

```go
// Near line 265-270, after the specialist tools:
toolsRegistry.Register(NewMyTool())
```

### 2. `cmd/picoclaw/main.go` — `gatewayCmd()` function

This is where tools that need channel instances or other runtime objects get registered. Look for the `agentLoop.RegisterTool()` calls.

**Use this for:** Tools that need the Telegram bot, Discord client, or other channel-specific instances.

```go
// After channelManager creation (around line 632):
agentLoop.RegisterTool(tools.NewMyTool(someExternalDep))
```

## If Your Tool Implements ContextualTool

Add your tool's name to the `contextualTools` list in `updateToolContexts()` in `pkg/agent/loop.go`:

```go
func (al *AgentLoop) updateToolContexts(channel, chatID string) {
    contextualTools := []string{
        "message", "spawn", "subagent", "consult_specialist",
        "link_topic", "manage_telegram",
        "your_new_tool",  // <-- add here
    }
    for _, name := range contextualTools {
        if tool, ok := al.tools.Get(name); ok {
            if ct, ok := tool.(tools.ContextualTool); ok {
                ct.SetContext(channel, chatID)
            }
        }
    }
}
```

Note: MetadataAwareTool does NOT need manual registration here — the registry automatically calls SetMetadata for any tool that implements it.

## Available Dependencies for Injection

These are available in `NewAgentLoop`:
- `workspace string` — workspace directory path
- `provider providers.LLMProvider` — the LLM provider (can make LLM calls)
- `cfg *config.Config` — full application config
- `msgBus *bus.MessageBus` — message bus for sending messages
- `vectorStore *memory.VectorStore` — semantic memory (may be nil)
- `extractor *memory.KnowledgeExtractor` — knowledge extraction (may be nil)
- `specialistLoader *specialists.SpecialistLoader` — specialist management
- `topicMappings *state.TopicMappingStore` — topic-specialist mappings

These are available in `main.go` after channel setup:
- `agentLoop *agent.AgentLoop` — the agent loop instance
- `channelManager *channels.Manager` — access to all channel instances
- `cfg *config.Config` — full config
- `msgBus *bus.MessageBus` — message bus

## Tool Registry Methods

```go
registry.Register(tool)           // Add a tool
registry.Get("name") (Tool, bool) // Get a tool by name
registry.List() []string          // List all tool names
registry.Count() int              // Number of registered tools
```
