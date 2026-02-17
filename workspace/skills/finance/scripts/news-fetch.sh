#!/bin/bash
# news-fetch.sh — RSS news aggregator
# Usage: bash news-fetch.sh [category]
# Categories: all (default), saudi, us, commodities, substacks

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REFERENCES_DIR="$SCRIPT_DIR/../references"
FEEDS_FILE="$REFERENCES_DIR/feeds.json"

CATEGORY="${1:-all}"
MAX_PER_FEED=5

if [ ! -f "$FEEDS_FILE" ]; then
    echo '{"error": "feeds.json not found"}'
    exit 1
fi

# Select feeds based on category
case "$CATEGORY" in
    saudi)
        FEEDS=$(jq -r '.saudi | to_entries[] | .value' "$FEEDS_FILE")
        ;;
    us)
        FEEDS=$(jq -r '.news | to_entries[] | .value' "$FEEDS_FILE")
        ;;
    commodities)
        FEEDS=$(jq -r '.commodities | to_entries[] | .value' "$FEEDS_FILE")
        ;;
    substacks)
        FEEDS=$(jq -r '.substacks | to_entries[] | .value' "$FEEDS_FILE")
        ;;
    all)
        FEEDS=$(jq -r '[.news, .saudi, .commodities] | map(to_entries[] | .value) | .[]' "$FEEDS_FILE")
        ;;
    *)
        echo "{\"error\": \"Unknown category: $CATEGORY. Use: all, saudi, us, commodities, substacks\"}"
        exit 1
        ;;
esac

echo '{"articles": ['
FIRST=true

while IFS= read -r FEED_URL; do
    [ -z "$FEED_URL" ] && continue

    # Fetch RSS feed
    RSS=$(curl -s --max-time 15 -L -H "User-Agent: Mozilla/5.0" "$FEED_URL" 2>/dev/null || echo "")
    [ -z "$RSS" ] && continue

    # Parse RSS items (extract title, link, pubDate) using basic text processing
    # This handles both RSS and Atom feeds
    echo "$RSS" | python3 -c "
import sys, xml.etree.ElementTree as ET
from datetime import datetime

data = sys.stdin.read()
try:
    root = ET.fromstring(data)
except:
    sys.exit(0)

ns = {'atom': 'http://www.w3.org/2005/Atom'}
items = []

# RSS 2.0
for item in root.findall('.//item')[:$MAX_PER_FEED]:
    title = item.findtext('title', '').strip()
    link = item.findtext('link', '').strip()
    pub = item.findtext('pubDate', '').strip()
    if title:
        items.append((title, link, pub))

# Atom
if not items:
    for entry in root.findall('.//atom:entry', ns)[:$MAX_PER_FEED]:
        title = entry.findtext('atom:title', '', ns).strip()
        link_el = entry.find('atom:link', ns)
        link = link_el.get('href', '') if link_el is not None else ''
        pub = entry.findtext('atom:published', '', ns).strip() or entry.findtext('atom:updated', '', ns).strip()
        if title:
            items.append((title, link, pub))

import json
for title, link, pub in items:
    print(json.dumps({'title': title, 'url': link, 'published': pub, 'source': '$FEED_URL'.split('/')[2]}))
" 2>/dev/null | while IFS= read -r ARTICLE; do
        if [ "$FIRST" = true ]; then
            FIRST=false
            echo "$ARTICLE"
        else
            echo ",$ARTICLE"
        fi
    done

    sleep 0.3
done <<< "$FEEDS"

echo '],'
echo "\"category\": \"$CATEGORY\","
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'
