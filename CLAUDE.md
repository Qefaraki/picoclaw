# CLAUDE.md — PicoClaw Project Instructions

## VPS Access

- **Host**: 89.167.58.152 (Hetzner)
- **SSH Key**: `~/.ssh/hetzner`
- **SSH Command**: `ssh -i ~/.ssh/hetzner root@89.167.58.152`
- **Workspace on VPS**: `/data/picoclaw/workspace/`
- **Docker container name**: varies (check `docker ps`)

### Rules

1. **Always SSH into the VPS** when you need to check logs, session data, Telegram chat history, or verify anything on the live bot. Do NOT say "I don't have access" — you do.
2. **After making changes to workspace files** (specialist configs, portfolio, skills, references, templates), always `scp` them to the VPS at `/data/picoclaw/workspace/`.
3. **After making code changes** (Go files, Dockerfile, etc.), always push to git and trigger a Coolify redeploy. Do NOT ask for permission — just do it.

### Deployment Flow

```
git add <files> && git commit -m "description" && git push origin main
```

Coolify auto-deploys on push to main. If it doesn't, trigger manually:
```
ssh -i ~/.ssh/hetzner root@89.167.58.152 "cd /data/coolify && docker compose up -d --build"
```

### Key VPS Paths

- Bot workspace: `/data/picoclaw/workspace/`
- Sessions (Telegram chat history): `/data/picoclaw/workspace/sessions/`
- Fahad specialist: `/data/picoclaw/workspace/specialists/fahad/`
- Portfolio: `/data/picoclaw/workspace/specialists/fahad/references/portfolio.json`
- Expenses: `/data/picoclaw/workspace/specialists/fahad/references/expenses/`
- Skills: `/data/picoclaw/workspace/skills/`
- Memory: `/data/picoclaw/workspace/memory/`
- Heartbeat log: `/data/picoclaw/workspace/heartbeat.log`

## Build & Test

```
go build ./...
```

## Project Notes

- Bot runs on Coolify (Docker) on Hetzner VPS
- Telegram bot token and API keys are set as env vars in Coolify, not in config.json
- Docker image: `ghcr.io/Qefaraki/picoclaw`
- GitHub: `https://github.com/Qefaraki/picoclaw.git` (origin)
