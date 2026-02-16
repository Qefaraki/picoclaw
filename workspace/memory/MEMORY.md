# Long-term Memory

## User Information

- Name: Saleh (Muhammad Abdullah Suliman Alqefari)
- QMUL student, Economics & Finance
- QM+ Moodle user ID: 27655920
- Email: ml23251@qmul.ac.uk

## Services

### Moodle (QM+)
- Connected via `moodle` tool with auto-refresh
- Token refreshes automatically via M365 SAML SSO when expired
- M365 credentials stored in config (`tools.moodle.m365_username/m365_password`)
- SSO refresh script: `/usr/local/lib/picoclaw/scripts/moodle_sso_refresh.py`
- If refresh fails, likely password changed or MFA was enabled — ask user

### M365 Email
- OAuth2 device code flow via email_dashboard.py
- Credentials at `~/.email_dashboard/credentials.json`
- Scopes: IMAP access only (can be extended for Graph API)

## Configuration

- VPS config: `/data/picoclaw/config.local.json`
- Model: claude-opus-4-6 (fallback: gemini-2.0-flash)
- Workspace: `~/.picoclaw/workspace`
- Moodle skill docs: `workspace/skills/moodle/SKILL.md`

## Important Notes

- QMUL blocks basic auth for all services — everything goes through M365 SSO
- Moodle mobile tokens obtained via `/admin/tool/mobile/launch.php` after SSO session
- The SSO flow parses Microsoft login pages programmatically (fragile if MS changes UI)
