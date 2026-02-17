#!/bin/bash
# substack-feeds.sh — Financial newsletter aggregator
# Sources: The Diff, Kyla Scanlon, Doomberg, Odd Lots, Noahpinion, Matt Levine
# Usage: bash substack-feeds.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REFERENCES_DIR="$SCRIPT_DIR/../references"
FEEDS_FILE="$REFERENCES_DIR/feeds.json"

MAX_PER_FEED=3

if [ ! -f "$FEEDS_FILE" ]; then
    echo '{"error": "feeds.json not found"}'
    exit 1
fi

FEEDS=$(jq -r '.substacks | to_entries[] | "\(.key)|\(.value)"' "$FEEDS_FILE")

echo '{"newsletters": ['
FIRST=true

while IFS='|' read -r NAME URL; do
    [ -z "$URL" ] && continue

    RSS=$(curl -s --max-time 15 -L -H "User-Agent: Mozilla/5.0" "$URL" 2>/dev/null || echo "")
    [ -z "$RSS" ] && continue

    ARTICLES=$(echo "$RSS" | python3 -c "
import sys, xml.etree.ElementTree as ET, json

data = sys.stdin.read()
try:
    root = ET.fromstring(data)
except:
    sys.exit(0)

items = []
for item in root.findall('.//item')[:$MAX_PER_FEED]:
    title = item.findtext('title', '').strip()
    link = item.findtext('link', '').strip()
    pub = item.findtext('pubDate', '').strip()
    desc = item.findtext('description', '').strip()
    # Truncate description
    if len(desc) > 200:
        desc = desc[:200] + '...'
    if title:
        items.append({'title': title, 'url': link, 'published': pub, 'summary': desc})

print(json.dumps({'author': '$NAME', 'articles': items}))
" 2>/dev/null || echo "")

    if [ -n "$ARTICLES" ] && [ "$ARTICLES" != "" ]; then
        if [ "$FIRST" = true ]; then
            FIRST=false
        else
            echo ","
        fi
        echo "$ARTICLES"
    fi

    sleep 0.3
done <<< "$FEEDS"

echo '],'
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'
