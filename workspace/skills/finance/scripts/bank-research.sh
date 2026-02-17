#!/bin/bash
# bank-research.sh — Detect new investment bank research reports
# Checks: Goldman Sachs, JPMorgan, Morgan Stanley, BlackRock
# Usage: bash bank-research.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SPECIALIST_DIR="$(cd "$SCRIPT_DIR/../../specialists/fahad/references" 2>/dev/null && pwd || echo "")"
STATE_FILE="${SPECIALIST_DIR:+$SPECIALIST_DIR/reports-state.json}"

echo '{"bank_research": {'

# --- Goldman Sachs Research ---
echo '"goldman_sachs": ['
GS_PAGE=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.goldmansachs.com/insights/articles" 2>/dev/null || echo "")

if [ -n "$GS_PAGE" ]; then
    echo "$GS_PAGE" | python3 -c "
import sys, re, json

html = sys.stdin.read()
matches = re.findall(r'<a[^>]*href=\"(/insights/articles/[^\"]+)\"[^>]*>\s*(?:<[^>]*>)*\s*([^<]+)', html)

results = []
seen = set()
for url, title in matches[:5]:
    title = title.strip()
    if title and len(title) > 5 and title not in seen:
        seen.add(title)
        results.append({'title': title, 'url': 'https://www.goldmansachs.com' + url, 'bank': 'Goldman Sachs'})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
fi
echo '],'

# --- JPMorgan Research ---
echo '"jpmorgan": ['
JPM_PAGE=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.jpmorgan.com/insights/research" 2>/dev/null || echo "")

if [ -n "$JPM_PAGE" ]; then
    echo "$JPM_PAGE" | python3 -c "
import sys, re, json

html = sys.stdin.read()
matches = re.findall(r'<a[^>]*href=\"(/insights/[^\"]+)\"[^>]*>\s*(?:<[^>]*>)*\s*([^<]+)', html)

results = []
seen = set()
for url, title in matches[:5]:
    title = title.strip()
    if title and len(title) > 5 and title not in seen:
        seen.add(title)
        results.append({'title': title, 'url': 'https://www.jpmorgan.com' + url, 'bank': 'JPMorgan'})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
fi
echo '],'

# --- Morgan Stanley Research ---
echo '"morgan_stanley": ['
MS_PAGE=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.morganstanley.com/ideas" 2>/dev/null || echo "")

if [ -n "$MS_PAGE" ]; then
    echo "$MS_PAGE" | python3 -c "
import sys, re, json

html = sys.stdin.read()
matches = re.findall(r'<a[^>]*href=\"(/ideas/[^\"]+)\"[^>]*>\s*(?:<[^>]*>)*\s*([^<]+)', html)

results = []
seen = set()
for url, title in matches[:5]:
    title = title.strip()
    if title and len(title) > 5 and title not in seen:
        seen.add(title)
        results.append({'title': title, 'url': 'https://www.morganstanley.com' + url, 'bank': 'Morgan Stanley'})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
fi
echo '],'

# --- BlackRock Investment Institute ---
echo '"blackrock": ['
BLK_PAGE=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.blackrock.com/corporate/insights/blackrock-investment-institute" 2>/dev/null || echo "")

if [ -n "$BLK_PAGE" ]; then
    echo "$BLK_PAGE" | python3 -c "
import sys, re, json

html = sys.stdin.read()
matches = re.findall(r'<a[^>]*href=\"([^\"]*insights[^\"]+)\"[^>]*>\s*(?:<[^>]*>)*\s*([^<]+)', html)

results = []
seen = set()
for url, title in matches[:5]:
    title = title.strip()
    if title and len(title) > 5 and title not in seen:
        seen.add(title)
        full_url = url if url.startswith('http') else 'https://www.blackrock.com' + url
        results.append({'title': title, 'url': full_url, 'bank': 'BlackRock'})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
fi
echo ']'

echo '},'
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'

# Update state file
if [ -n "$STATE_FILE" ] && [ -f "$STATE_FILE" ]; then
    jq --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '.last_checked = $ts' "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
fi
