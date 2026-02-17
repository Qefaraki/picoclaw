---
name: self-extend
description: Write new Go tools that compile into your own binary. Use when the user asks you to add a new capability, integration, or tool to yourself — or when you realize you need a tool that doesn't exist yet. Covers the full cycle: write Go code, register the tool, rebuild, and restart.
---

# Self-Extension: Writing Your Own Tools

You can write new tools in Go, compile them into your own binary, and restart yourself to gain new capabilities. This skill covers the full process.

## Quick Reference

1. Write a `.go` file in `pkg/tools/`
2. Register it in `pkg/agent/loop.go` (inside `NewAgentLoop`) or `cmd/picoclaw/main.go` (for tools needing external deps like bot instances)
3. Run `go build -o picoclaw ./cmd/picoclaw/` from the project root
4. Restart yourself

## Before You Start

Find your source code root by reading the go.mod:

```bash
# Typical locations — check which exists
ls /root/picoclaw/go.mod
ls /home/*/picoclaw/go.mod
```

All file paths below are relative to that project root.

## Architecture

Read `references/tool-architecture.md` for the complete interface definitions, result types, and optional interfaces.

Read `references/example-tool.md` for a complete working tool example you can copy and adapt.

Read `references/registration.md` for how to wire your tool into the agent.

## The Process

### Step 1: Write the Tool

Create a new file `pkg/tools/my_tool.go`. Every tool must:

- Be in `package tools`
- Implement the `Tool` interface: `Name()`, `Description()`, `Parameters()`, `Execute()`
- Return `*ToolResult` from Execute using the helper constructors

Key decisions:
- **Need chat context (channel/chatID)?** Also implement `ContextualTool`
- **Need message metadata (thread_id, user_id)?** Also implement `MetadataAwareTool`
- **Need to run in background?** Also implement `AsyncTool`
- **Need external dependencies (API clients, bot instances)?** Accept them in your constructor

### Step 2: Register the Tool

Where you register depends on what the tool needs:

- **Simple tools (no external deps):** Register in `NewAgentLoop()` in `pkg/agent/loop.go`, near the other tool registrations
- **Tools needing channel instances (Telegram bot, etc.):** Register in `cmd/picoclaw/main.go` after channel setup, using `agentLoop.RegisterTool()`

### Step 3: Build

```bash
cd /path/to/project/root
go build -o picoclaw ./cmd/picoclaw/
```

If there are compile errors, fix them and rebuild. Common issues:
- Missing imports (add them to the import block)
- Wrong field names on external library structs (check with `go doc`)
- Unused imports (remove them)

### Step 4: Deploy and Restart

Check how you're running:

```bash
# Check if running in Docker
cat /proc/1/cgroup 2>/dev/null | grep docker

# Check if running as systemd service
systemctl status picoclaw 2>/dev/null

# Check if running via supervisor or screen/tmux
ps aux | grep picoclaw
```

Then restart appropriately:

```bash
# If systemd service:
sudo systemctl restart picoclaw

# If Docker: rebuild and recreate the container
docker build -t picoclaw . && docker compose up -d

# If running directly: replace the binary and re-run
cp picoclaw /usr/local/bin/picoclaw  # or wherever the running binary is
# Then signal the old process to stop and start fresh
kill -TERM $(pgrep picoclaw) && sleep 2 && picoclaw gateway &
```

### Step 5: Verify

After restart, check that your new tool appears:
- The startup logs should show an increased tool count
- Try using the tool to confirm it works

## Tips

- Keep tools focused — one tool per concern
- Use `SilentResult()` for results the LLM should relay to the user
- Use `ErrorResult()` for failures — the LLM gets the error and can explain/retry
- For tools that call external APIs, handle timeouts with `context.WithTimeout`
- If you need a new Go dependency: `go get <package>@latest` before building
