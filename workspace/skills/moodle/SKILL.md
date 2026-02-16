---
name: moodle
description: Access QM+ Moodle — courses, assignments, calendar, course contents. Auto-refreshes token via M365 SSO.
---

# Moodle (QM+) Integration

You have a `moodle` tool that connects to QM+ (qmplus.qmul.ac.uk), the Moodle instance for Queen Mary University of London.

## Available Actions

Use the `moodle` tool with these actions:

| Action | Description | Extra params |
|--------|-------------|--------------|
| `courses` | List all enrolled courses | — |
| `assignments` | Upcoming + overdue assignments | `days` (default 30) |
| `calendar` | Calendar events | `days` (default 30) |
| `course_contents` | Browse a course's sections/resources | `course_id` (required) |
| `site_info` | Show logged-in user info | — |

## Examples

```json
{"action": "assignments", "days": 14}
{"action": "courses"}
{"action": "course_contents", "course_id": 28907}
{"action": "calendar", "days": 60}
```

## How Authentication Works

1. The tool uses a **Moodle mobile service token** stored in config
2. If the token expires, the tool **automatically refreshes** it by running a SAML2 SSO flow:
   - Starts SAML at QM+ → redirects to Microsoft login
   - Submits M365 credentials (stored in config) → gets SAML assertion
   - Posts assertion back to QM+ → extracts new mobile token
3. The Python script that does this lives at `/usr/local/lib/picoclaw/scripts/moodle_sso_refresh.py`
4. The whole process takes ~5-10 seconds and happens transparently

## Troubleshooting

### "invalidtoken" error even after refresh
The M365 password may have changed. Ask the user to update it:
- Config location on VPS: `/data/picoclaw/config.local.json` → `tools.moodle.m365_password`
- Or set env var: `PICOCLAW_TOOLS_MOODLE_M365_PASSWORD`

### "AADSTS" errors from Microsoft
- `AADSTS50126`: Wrong password
- `AADSTS50076`: MFA required — user needs to do a manual SSO flow from laptop
- `AADSTS700016`: App not found — unlikely but would need client ID change

### SSO script not found
The refresh script should be at `/usr/local/lib/picoclaw/scripts/moodle_sso_refresh.py`. If missing, the Docker image needs rebuilding (it's copied from `scripts/` during build).

### Python/requests not available
The Dockerfile installs `python3` and `py3-requests` in the Alpine runtime image. If missing, `apk add python3 py3-requests` in the container.

## Key Course IDs (2025/26)

| ID | Code | Name |
|----|------|------|
| 28907 | ECN209 | International Finance |
| 28910 | ECN228 | International Trade |
| 28912 | ECN239 | Managerial Strategy |
| 28913 | ECN242 | Corporate Finance and Valuation |
| 27773 | ECN005 | Personal and Career Development Plan 2 |

## Architecture

- **Go tool**: `pkg/tools/moodle.go` — implements `Tool` interface, handles API calls + auto-refresh
- **Python SSO**: `scripts/moodle_sso_refresh.py` — standalone script, takes username+password as args, prints token to stdout
- **Config**: `pkg/config/config.go` → `MoodleConfig` struct with `url`, `token`, `m365_username`, `m365_password`
- **Registration**: `pkg/agent/loop.go` → `createToolRegistry()`, enabled when `tools.moodle.enabled = true`
- **Moodle API**: Standard Web Services REST API at `{url}/webservice/rest/server.php` using `moodle_mobile_app` service
