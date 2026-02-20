package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// jsonRPCRequest is a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolDefinition represents a tool exposed by an MCP server.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPServer represents a running MCP server process.
type MCPServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	mu     sync.Mutex
	nextID atomic.Int64
	tools  []ToolDefinition
}

// MCPManager manages multiple MCP server processes.
type MCPManager struct {
	servers map[string]*MCPServer
	mu      sync.RWMutex
}

// NewMCPManager creates a new MCP manager.
func NewMCPManager() *MCPManager {
	return &MCPManager{
		servers: make(map[string]*MCPServer),
	}
}

// StartFromConfig starts all enabled MCP servers from config.
func (m *MCPManager) StartFromConfig(configs []config.MCPServerConfig) {
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		if err := m.Start(cfg.Name, cfg.Command, cfg.Args, cfg.Env); err != nil {
			logger.WarnCF("mcp", "Failed to start MCP server", map[string]interface{}{
				"name":  cfg.Name,
				"error": err.Error(),
			})
		}
	}
}

// Start launches an MCP server process.
func (m *MCPManager) Start(name, command string, args []string, env map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[name]; exists {
		return fmt.Errorf("MCP server %q already running", name)
	}

	server := &MCPServer{
		Name:    name,
		Command: command,
		Args:    args,
		Env:     env,
	}

	if err := server.start(); err != nil {
		return err
	}

	// Initialize the server
	if err := server.initialize(); err != nil {
		server.stop()
		return fmt.Errorf("initialize %s: %w", name, err)
	}

	// Discover tools
	tools, err := server.listTools()
	if err != nil {
		server.stop()
		return fmt.Errorf("list tools from %s: %w", name, err)
	}
	server.tools = tools

	m.servers[name] = server

	logger.InfoCF("mcp", "MCP server started", map[string]interface{}{
		"name":       name,
		"tools":      len(tools),
		"command":    command,
	})

	return nil
}

// ListTools returns all tools from a specific server.
func (m *MCPManager) ListTools(server string) []ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if s, ok := m.servers[server]; ok {
		return s.tools
	}
	return nil
}

// AllTools returns all tools across all servers, prefixed with server name.
func (m *MCPManager) AllTools() map[string][]ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]ToolDefinition)
	for name, server := range m.servers {
		result[name] = server.tools
	}
	return result
}

// CallTool calls a tool on a specific server.
func (m *MCPManager) CallTool(serverName, toolName string, args map[string]interface{}) (string, error) {
	m.mu.RLock()
	server, ok := m.servers[serverName]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("MCP server %q not found", serverName)
	}

	return server.callTool(toolName, args)
}

// StopAll stops all running MCP servers.
func (m *MCPManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, server := range m.servers {
		server.stop()
		logger.InfoCF("mcp", "MCP server stopped", map[string]interface{}{"name": name})
	}
	m.servers = make(map[string]*MCPServer)
}

// -- MCPServer methods --

func (s *MCPServer) start() error {
	s.cmd = exec.Command(s.Command, s.Args...)

	// Set environment variables
	s.cmd.Env = os.Environ()
	for k, v := range s.Env {
		s.cmd.Env = append(s.cmd.Env, k+"="+v)
	}

	stdin, err := s.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	s.stdin = stdin

	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	s.stdout = bufio.NewScanner(stdout)
	s.stdout.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	return nil
}

func (s *MCPServer) stop() {
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}

func (s *MCPServer) send(req jsonRPCRequest) (*jsonRPCResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	// Write request
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write to MCP server: %w", err)
	}

	// Read response
	if !s.stdout.Scan() {
		if err := s.stdout.Err(); err != nil {
			return nil, fmt.Errorf("read from MCP server: %w", err)
		}
		return nil, fmt.Errorf("MCP server closed connection")
	}

	var resp jsonRPCResponse
	if err := json.Unmarshal(s.stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parse MCP response: %w", err)
	}

	return &resp, nil
}

func (s *MCPServer) initialize() error {
	id := s.nextID.Add(1)
	resp, err := s.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "initialize",
		Params: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":   map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "picoclaw",
				"version": "1.0.0",
			},
		},
	})
	if err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("MCP initialize error: %s", resp.Error.Message)
	}

	// Send initialized notification
	notif, _ := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
	s.mu.Lock()
	s.stdin.Write(append(notif, '\n'))
	s.mu.Unlock()

	return nil
}

func (s *MCPServer) listTools() ([]ToolDefinition, error) {
	id := s.nextID.Add(1)
	resp, err := s.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/list",
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("MCP tools/list error: %s", resp.Error.Message)
	}

	var result struct {
		Tools []ToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse tools list: %w", err)
	}

	return result.Tools, nil
}

func (s *MCPServer) callTool(toolName string, args map[string]interface{}) (string, error) {
	id := s.nextID.Add(1)
	resp, err := s.send(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: map[string]interface{}{
			"name":      toolName,
			"arguments": args,
		},
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("MCP tool call error: %s", resp.Error.Message)
	}

	// Parse result content
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return string(resp.Result), nil
	}

	var texts []string
	for _, c := range result.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) > 0 {
		return texts[0], nil
	}
	return string(resp.Result), nil
}

// DiscoverMCPTools returns a flat list of all MCP tools with server names.
// Used by the bridge to register tools in the PicoClaw registry.
func (m *MCPManager) DiscoverMCPTools() []struct {
	Server string
	Tool   ToolDefinition
} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []struct {
		Server string
		Tool   ToolDefinition
	}
	for name, server := range m.servers {
		for _, tool := range server.tools {
			all = append(all, struct {
				Server string
				Tool   ToolDefinition
			}{Server: name, Tool: tool})
		}
	}
	return all
}

// Ensure context is used (for future timeout support)
var _ = context.Background
