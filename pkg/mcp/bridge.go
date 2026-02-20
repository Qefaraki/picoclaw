package mcp

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/tools"
)

// MCPBridgeTool wraps an MCP server tool as a PicoClaw Tool.
type MCPBridgeTool struct {
	manager    *MCPManager
	serverName string
	toolDef    ToolDefinition
}

// NewMCPBridgeTool creates a PicoClaw tool that delegates to an MCP server tool.
func NewMCPBridgeTool(manager *MCPManager, serverName string, toolDef ToolDefinition) *MCPBridgeTool {
	return &MCPBridgeTool{
		manager:    manager,
		serverName: serverName,
		toolDef:    toolDef,
	}
}

func (t *MCPBridgeTool) Name() string {
	return fmt.Sprintf("mcp_%s_%s", t.serverName, t.toolDef.Name)
}

func (t *MCPBridgeTool) Description() string {
	return fmt.Sprintf("[MCP:%s] %s", t.serverName, t.toolDef.Description)
}

func (t *MCPBridgeTool) Parameters() map[string]interface{} {
	if t.toolDef.InputSchema != nil {
		return t.toolDef.InputSchema
	}
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *MCPBridgeTool) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	result, err := t.manager.CallTool(t.serverName, t.toolDef.Name, args)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("MCP tool %s/%s error: %v", t.serverName, t.toolDef.Name, err))
	}
	return tools.SilentResult(result)
}

// RegisterMCPTools discovers all MCP tools and registers them in the PicoClaw tool registry.
func RegisterMCPTools(manager *MCPManager, registry *tools.ToolRegistry) int {
	discovered := manager.DiscoverMCPTools()
	for _, entry := range discovered {
		bridge := NewMCPBridgeTool(manager, entry.Server, entry.Tool)
		registry.Register(bridge)
	}
	return len(discovered)
}
