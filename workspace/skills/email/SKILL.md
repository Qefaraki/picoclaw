---
name: email
description: Access M365 Outlook email — list, search, read, mark read, archive. OAuth2 with auto-refresh.
---

# Email (M365 Outlook) Integration

You have an `email` tool that connects to a Microsoft 365 mailbox via IMAP/OAuth2.

## Available Actions

Use the `email` tool with these actions:

| Action | Description | Extra params |
|--------|-------------|--------------|
| `recent` | List recent inbox emails | `days` (default 7) |
| `unread` | List unread emails | — |
| `search` | Search by sender and/or subject | `sender`, `subject` |
| `read` | Get full email body by UID | `uid` (required) |
| `mark_read` | Mark an email as read | `uid` (required) |
| `archive` | Move email to archive folder | `uid` (required) |
| `folders` | List all mailbox folders | — |

## Examples

```json
{"action": "recent", "days": 3}
{"action": "unread"}
{"action": "search", "sender": "professor@qmul.ac.uk"}
{"action": "search", "subject": "assignment"}
{"action": "read", "uid": "12345"}
{"action": "mark_read", "uid": "12345"}
{"action": "archive", "uid": "12345"}
{"action": "folders"}
```

## How Authentication Works

1. The tool uses **OAuth2 device code flow** for initial setup (one-time, interactive)
2. After initial auth, tokens are stored in `~/.email_dashboard/credentials.json`
3. Access tokens auto-refresh via refresh token (~90 day lifetime)
4. IMAP connection uses `XOAUTH2` authentication against `outlook.office365.com`

## Troubleshooting

### "Token expired and refresh failed"
The OAuth2 refresh token has expired (~90 days). Re-run device code flow on VPS:
```
python3 /usr/local/lib/picoclaw/scripts/email_dashboard.py --email <address> recent
```

### "AUTHENTICATE failed"
Same as above — refresh token expired or revoked.

### Script not found
The email script should be at `/usr/local/lib/picoclaw/scripts/email_dashboard.py`. If missing, rebuild the Docker image (it's copied from `scripts/` during build).

### Email bodies truncated
Long email bodies are truncated at 8000 characters to avoid flooding the LLM context window. If the user needs the full body, let them know it was truncated.

## Architecture

- **Go tool**: `pkg/tools/email.go` — implements `Tool` interface, shells out to Python script
- **Python script**: `scripts/email_dashboard.py` — handles IMAP connections, OAuth2 token lifecycle
- **Config**: `pkg/config/config.go` → `EmailConfig` struct with `enabled`, `address`
- **Registration**: `pkg/agent/loop.go` → `createToolRegistry()`, enabled when `tools.email.enabled = true`
- **Credentials**: `~/.email_dashboard/credentials.json` (bind-mount `/data/picoclaw/.email_dashboard/` in Docker)
