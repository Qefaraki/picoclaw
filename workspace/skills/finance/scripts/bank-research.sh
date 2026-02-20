#!/bin/bash
# bank-research.sh — Detect new investment bank research reports
# Uses RSS feeds and public APIs where available, falls back to web fetch
# Usage: bash bank-research.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SPECIALIST_DIR="$(cd "$SCRIPT_DIR/../../../specialists/fahad/references" 2>/dev/null && pwd || echo "")"
STATE_FILE="${SPECIALIST_DIR:+$SPECIALIST_DIR/reports-state.json}"

echo '{"bank_research": {'

# --- Goldman Sachs Insights (RSS) ---
echo '"goldman_sachs": '
GS_RSS=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.goldmansachs.com/insights/articles.rss" 2>/dev/null || echo "")

if [ -n "$GS_RSS" ]; then
    echo "$GS_RSS" | python3 -c "
import sys, xml.etree.ElementTree as ET, json

data = sys.stdin.read()
try:
    root = ET.fromstring(data)
except:
    print('[]')
    sys.exit(0)

results = []
for item in root.findall('.//item')[:5]:
    title = item.findtext('title', '').strip()
    link = item.findtext('link', '').strip()
    pub = item.findtext('pubDate', '').strip()
    if title and len(title) > 5:
        results.append({'title': title, 'url': link, 'published': pub, 'bank': 'Goldman Sachs'})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
else
    echo "[]"
fi
echo ','

# --- JPMorgan Research (RSS) ---
echo '"jpmorgan": '
JPM_RSS=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.jpmorgan.com/insights/research.rss" 2>/dev/null || echo "")

if [ -n "$JPM_RSS" ]; then
    echo "$JPM_RSS" | python3 -c "
import sys, xml.etree.ElementTree as ET, json

data = sys.stdin.read()
try:
    root = ET.fromstring(data)
except:
    print('[]')
    sys.exit(0)

results = []
for item in root.findall('.//item')[:5]:
    title = item.findtext('title', '').strip()
    link = item.findtext('link', '').strip()
    pub = item.findtext('pubDate', '').strip()
    if title and len(title) > 5:
        results.append({'title': title, 'url': link, 'published': pub, 'bank': 'JPMorgan'})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
else
    echo "[]"
fi
echo ','

# --- Morgan Stanley Ideas (RSS) ---
echo '"morgan_stanley": '
MS_RSS=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.morganstanley.com/ideas.rss" 2>/dev/null || echo "")

if [ -n "$MS_RSS" ]; then
    echo "$MS_RSS" | python3 -c "
import sys, xml.etree.ElementTree as ET, json

data = sys.stdin.read()
try:
    root = ET.fromstring(data)
except:
    print('[]')
    sys.exit(0)

results = []
for item in root.findall('.//item')[:5]:
    title = item.findtext('title', '').strip()
    link = item.findtext('link', '').strip()
    pub = item.findtext('pubDate', '').strip()
    if title and len(title) > 5:
        results.append({'title': title, 'url': link, 'published': pub, 'bank': 'Morgan Stanley'})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
else
    echo "[]"
fi
echo ','

# --- BlackRock Investment Institute (RSS) ---
echo '"blackrock": '
BLK_RSS=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.blackrock.com/corporate/insights/blackrock-investment-institute.rss" 2>/dev/null || echo "")

if [ -n "$BLK_RSS" ]; then
    echo "$BLK_RSS" | python3 -c "
import sys, xml.etree.ElementTree as ET, json

data = sys.stdin.read()
try:
    root = ET.fromstring(data)
except:
    print('[]')
    sys.exit(0)

results = []
for item in root.findall('.//item')[:5]:
    title = item.findtext('title', '').strip()
    link = item.findtext('link', '').strip()
    pub = item.findtext('pubDate', '').strip()
    if title and len(title) > 5:
        full_url = link if link.startswith('http') else 'https://www.blackrock.com' + link
        results.append({'title': title, 'url': full_url, 'published': pub, 'bank': 'BlackRock'})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
else
    echo "[]"
fi

echo '},'
echo '"note": "RSS-based detection. If feeds return empty, use web_fetch tool conversationally for specific bank research pages.",'
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'

# Update state file
if [ -n "$STATE_FILE" ] && [ -f "$STATE_FILE" ]; then
    jq --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '.last_checked = $ts' "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
fi
