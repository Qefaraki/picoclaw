---
name: email
description: Access M365 Outlook email — list, search, read, send, reply, mark read, archive. OAuth2 with auto-refresh.
---

# Email (M365 Outlook) Integration

You have an `email` tool that connects to a Microsoft 365 mailbox via IMAP (read) and SMTP (send) using OAuth2.

## Available Actions

Use the `email` tool with these actions:

| Action | Description | Required params | Optional params |
|--------|-------------|-----------------|-----------------|
| `recent` | List recent inbox emails | — | `days` (default 7) |
| `unread` | List unread emails | — | — |
| `search` | Search by sender and/or subject | — | `sender`, `subject` |
| `read` | Get full email body by UID | `uid` | — |
| `mark_read` | Mark an email as read | `uid` | — |
| `archive` | Move email to archive folder | `uid` | — |
| `folders` | List all mailbox folders | — | — |
| `send` | Send a new email | `to`, `subject`, `body` | `cc`, `bcc` |
| `reply` | Reply to an email by UID | `uid`, `body` | — |

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
{"action": "send", "to": "someone@example.com", "subject": "Hello", "body": "Message text here"}
{"action": "send", "to": "someone@example.com", "subject": "Hello", "body": "Message text", "cc": "other@example.com"}
{"action": "reply", "uid": "12345", "body": "Thanks for your email!"}
```

## Send & Reply Notes

- **send**: Composes a new email from the authenticated account. The `to`, `subject`, and `body` params are all required. `cc` and `bcc` are optional.
- **reply**: Replies to the sender of the given UID. Automatically sets `In-Reply-To` and `References` headers for proper threading, and prepends `Re:` to the subject. Only `uid` and `body` are needed — the recipient and subject are derived from the original email.
- Both use SMTP with XOAUTH2 auth against `smtp.office365.com:587`.

## How Authentication Works

1. The tool uses **OAuth2 device code flow** for initial setup (one-time, interactive)
2. After initial auth, tokens are stored in `~/.email_dashboard/credentials.json`
3. Access tokens auto-refresh via refresh token (~90 day lifetime)
4. IMAP uses `XOAUTH2` against `outlook.office365.com:993`; SMTP uses `XOAUTH2` against `smtp.office365.com:587`
5. Scopes: `IMAP.AccessAsUser.All`, `SMTP.Send`, `offline_access`

## Troubleshooting

### "Token expired and refresh failed"
The OAuth2 refresh token has expired (~90 days). Re-run device code flow on VPS:
```
python3 /usr/local/lib/picoclaw/scripts/email_dashboard.py --email <address> logout
python3 /usr/local/lib/picoclaw/scripts/email_dashboard.py --email <address> recent
```
The first command clears stale tokens; the second triggers re-authentication.

### "AUTHENTICATE failed"
Same as above — refresh token expired or revoked. Run `logout` then re-auth.

### "SMTP AUTH failed"
The OAuth2 token may be missing the `SMTP.Send` scope (e.g. from an old auth session). Fix:
```
python3 /usr/local/lib/picoclaw/scripts/email_dashboard.py --email <address> logout
python3 /usr/local/lib/picoclaw/scripts/email_dashboard.py --email <address> recent
```
This forces a fresh token with all current scopes including SMTP.

### Script not found
The email script should be at `/usr/local/lib/picoclaw/scripts/email_dashboard.py`. If missing, rebuild the Docker image (it's copied from `scripts/` during build).

### Email bodies truncated
Long email bodies are truncated at 8000 characters to avoid flooding the LLM context window. If the user needs the full body, let them know it was truncated.

## Architecture

- **Go tool**: `pkg/tools/email.go` — implements `Tool` interface, shells out to Python script
- **Python script**: `scripts/email_dashboard.py` — handles IMAP/SMTP connections, OAuth2 token lifecycle
- **Config**: `pkg/config/config.go` → `EmailConfig` struct with `enabled`, `address`
- **Registration**: `pkg/agent/loop.go` → `createToolRegistry()`, enabled when `tools.email.enabled = true`
- **Credentials**: `~/.email_dashboard/credentials.json` (bind-mount `/data/picoclaw/.email_dashboard/` in Docker)
