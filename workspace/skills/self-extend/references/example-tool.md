# Example Tool — Complete Working Code

This is a full tool you can copy and adapt. It demonstrates all common patterns.

## File: `pkg/tools/system_info.go`

```go
package tools

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
)

type SystemInfoTool struct {
	channel  string
	chatID   string
	metadata map[string]string
}

func NewSystemInfoTool() *SystemInfoTool {
	return &SystemInfoTool{}
}

func (t *SystemInfoTool) Name() string { return "system_info" }

func (t *SystemInfoTool) Description() string {
	return "Get system information about the VPS: OS, architecture, memory, disk, hostname, uptime."
}

func (t *SystemInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"section": map[string]interface{}{
				"type":        "string",
				"description": "What info to get: all, os, memory, disk, network",
				"enum":        []string{"all", "os", "memory", "disk", "network"},
			},
		},
		"required": []string{"section"},
	}
}

// Optional: implement ContextualTool if you need channel/chatID
func (t *SystemInfoTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

// Optional: implement MetadataAwareTool if you need thread_id etc.
func (t *SystemInfoTool) SetMetadata(metadata map[string]string) {
	t.metadata = metadata
}

func (t *SystemInfoTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	section, _ := args["section"].(string)
	if section == "" {
		section = "all"
	}

	var info strings.Builder

	switch section {
	case "os", "all":
		hostname, _ := os.Hostname()
		info.WriteString(fmt.Sprintf("OS: %s/%s\n", runtime.GOOS, runtime.GOARCH))
		info.WriteString(fmt.Sprintf("Hostname: %s\n", hostname))
		info.WriteString(fmt.Sprintf("CPUs: %d\n", runtime.NumCPU()))
		info.WriteString(fmt.Sprintf("Go: %s\n", runtime.Version()))
		if section != "all" {
			break
		}
		fallthrough
	case "memory":
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		info.WriteString(fmt.Sprintf("Go Heap: %.1f MB\n", float64(m.HeapAlloc)/1024/1024))
		info.WriteString(fmt.Sprintf("Go Sys: %.1f MB\n", float64(m.Sys)/1024/1024))
		if section != "all" {
			break
		}
		fallthrough
	case "disk", "network":
		// For disk/network, use exec to get system-level info
		info.WriteString("(Use exec tool for detailed disk/network info)")
	}

	return SilentResult(info.String())
}
```

## Registration in `pkg/agent/loop.go`

In the `NewAgentLoop` function, after the other tool registrations:

```go
toolsRegistry.Register(tools.NewSystemInfoTool())
```

That's it. No other changes needed for simple tools.

## Example: Tool That Needs External Dependencies

If your tool needs something injected (like an API client or database connection), accept it in the constructor:

```go
type MyAPITool struct {
	apiKey string
}

func NewMyAPITool(apiKey string) *MyAPITool {
	return &MyAPITool{apiKey: apiKey}
}
```

Then register it where the dependency is available. For example in `cmd/picoclaw/main.go`:

```go
agentLoop.RegisterTool(tools.NewMyAPITool(cfg.SomeAPIKey))
```

## Example: Tool Using the Message Bus

If your tool needs to send messages to users:

```go
type NotifyTool struct {
	bus     *bus.MessageBus
	channel string
	chatID  string
}

func NewNotifyTool(msgBus *bus.MessageBus) *NotifyTool {
	return &NotifyTool{bus: msgBus}
}

func (t *NotifyTool) SetContext(channel, chatID string) {
	t.channel = channel
	t.chatID = chatID
}

func (t *NotifyTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	message, _ := args["message"].(string)

	t.bus.PublishOutbound(bus.OutboundMessage{
		Channel: t.channel,
		ChatID:  t.chatID,
		Content: message,
	})

	return SilentResult("Message sent to user")
}
```
