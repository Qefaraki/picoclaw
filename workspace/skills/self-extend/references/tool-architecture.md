# Tool Architecture Reference

Module: `github.com/sipeed/picoclaw`

## Core Interface — `pkg/tools/base.go`

Every tool must implement this:

```go
type Tool interface {
    Name() string                    // Unique tool name (snake_case)
    Description() string             // What the LLM sees — be specific
    Parameters() map[string]interface{} // JSON Schema for arguments
    Execute(ctx context.Context, args map[string]interface{}) *ToolResult
}
```

## Optional Interfaces — `pkg/tools/base.go`

### ContextualTool — receive channel and chatID

```go
type ContextualTool interface {
    Tool
    SetContext(channel, chatID string)
}
```

Implement this when your tool needs to know which channel (telegram, discord, etc.) and chat ID the message came from. SetContext is called before Execute on each invocation.

### MetadataAwareTool — receive message metadata

```go
type MetadataAwareTool interface {
    Tool
    SetMetadata(metadata map[string]string)
}
```

Implement this when your tool needs message-level metadata. Available keys depend on the channel:
- `thread_id` — forum topic thread ID (Telegram)
- `message_id` — the message ID
- `user_id` — sender's user ID
- `username` — sender's username
- `first_name` — sender's first name
- `is_group` — "true" if group chat
- `is_forum_topic` — "true" if from a forum topic

### AsyncTool — background execution with callback

```go
type AsyncCallback func(ctx context.Context, result *ToolResult)

type AsyncTool interface {
    Tool
    SetCallback(cb AsyncCallback)
}
```

Implement this for long-running operations. Return `AsyncResult()` from Execute immediately, do work in a goroutine, call the callback when done.

## Result Types — `pkg/tools/result.go`

```go
// Most common — LLM gets the content, relays to user
SilentResult(forLLM string) *ToolResult

// Error — LLM sees the error and can explain/retry
ErrorResult(message string) *ToolResult

// Show directly to user AND give to LLM
UserResult(content string) *ToolResult

// Async — tool is running in background
AsyncResult(forLLM string) *ToolResult

// Basic — LLM gets content, default behavior
NewToolResult(forLLM string) *ToolResult

// Chain error info for logging
result.WithError(err)
```

## Parameters() Format

Returns a JSON Schema object. Example:

```go
func (t *MyTool) Parameters() map[string]interface{} {
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "query": map[string]interface{}{
                "type":        "string",
                "description": "Search query",
            },
            "limit": map[string]interface{}{
                "type":        "integer",
                "description": "Max results to return",
            },
        },
        "required": []string{"query"},
    }
}
```

Supported JSON Schema types: `string`, `integer`, `number`, `boolean`, `array`, `object`.

For enum values:
```go
"action": map[string]interface{}{
    "type": "string",
    "enum": []string{"start", "stop", "status"},
},
```

## Important Imports

```go
import (
    "context"
    "fmt"
    "github.com/sipeed/picoclaw/pkg/tools"     // only needed outside the tools package
    "github.com/sipeed/picoclaw/pkg/logger"     // structured logging
    "github.com/sipeed/picoclaw/pkg/state"      // persistent state
    "github.com/sipeed/picoclaw/pkg/memory"     // vector store
    "github.com/sipeed/picoclaw/pkg/providers"  // LLM provider interface
    "github.com/sipeed/picoclaw/pkg/bus"        // message bus
)
```

When writing tools inside `pkg/tools/`, you don't need to import the tools package — you're already in it. Just use `SilentResult()`, `ErrorResult()`, etc. directly.

## Logging

```go
logger.InfoCF("mytool", "Something happened", map[string]interface{}{
    "key": value,
})
logger.ErrorCF("mytool", "Something failed", map[string]interface{}{
    "error": err.Error(),
})
logger.DebugCF("mytool", "Debug info", map[string]interface{}{...})
```
