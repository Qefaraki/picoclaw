#!/bin/bash
# saudi-ipo.sh — Monitor Saudi CMA/Tadawul for new IPO announcements
# Compares against state file for new-only detection
# Usage: bash saudi-ipo.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SPECIALIST_DIR="$(cd "$SCRIPT_DIR/../../specialists/fahad/references" 2>/dev/null && pwd || echo "")"
STATE_FILE="${SPECIALIST_DIR:+$SPECIALIST_DIR/ipo-tracker.json}"

echo '{"saudi_ipos": {'

# --- CMA Announcements ---
echo '"cma_announcements": ['

# Fetch CMA website for IPO-related announcements
CMA_PAGE=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://cma.gov.sa/en/Market/NEWS/Pages/default.aspx" 2>/dev/null || echo "")

if [ -n "$CMA_PAGE" ]; then
    echo "$CMA_PAGE" | python3 -c "
import sys, re, json

html = sys.stdin.read()
# Look for IPO-related keywords in announcements
ipo_keywords = ['IPO', 'offering', 'listing', 'subscription', 'prospectus', 'الطرح', 'اكتتاب']
pattern = r'<a[^>]*href=\"([^\"]+)\"[^>]*>([^<]*(?:' + '|'.join(ipo_keywords) + ')[^<]*)</a>'
matches = re.findall(pattern, html, re.IGNORECASE)

results = []
for url, title in matches[:10]:
    results.append({'title': title.strip(), 'url': url.strip()})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
fi

echo '],'

# --- Argaam IPO News ---
echo '"argaam_ipos": ['

ARGAAM_PAGE=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.argaam.com/en/article/articlelist/IPOs" 2>/dev/null || echo "")

if [ -n "$ARGAAM_PAGE" ]; then
    echo "$ARGAAM_PAGE" | python3 -c "
import sys, re, json

html = sys.stdin.read()
# Extract article titles and links
matches = re.findall(r'<a[^>]*href=\"(/en/article/[^\"]+)\"[^>]*>([^<]+)</a>', html)

results = []
seen = set()
for url, title in matches[:10]:
    title = title.strip()
    if title and title not in seen:
        seen.add(title)
        results.append({'title': title, 'url': 'https://www.argaam.com' + url.strip()})

print(json.dumps(results))
" 2>/dev/null || echo "[]"
fi

echo '],'

# --- Tadawul New Listings ---
echo '"tadawul_listings": ['

TADAWUL_PAGE=$(curl -s --max-time 20 -L \
    -H "User-Agent: Mozilla/5.0" \
    "https://www.saudiexchange.sa/wps/portal/saudiexchange/newsandreports/IPO-news" 2>/dev/null || echo "")

if [ -n "$TADAWUL_PAGE" ]; then
    echo "$TADAWUL_PAGE" | python3 -c "
import sys, re, json

html = sys.stdin.read()
# Extract IPO news items
matches = re.findall(r'<a[^>]*href=\"([^\"]+)\"[^>]*>\s*<[^>]*>\s*([^<]+)', html)

results = []
seen = set()
for url, title in matches[:10]:
    title = title.strip()
    if title and len(title) > 10 and title not in seen:
        seen.add(title)
        results.append({'title': title, 'url': url.strip() if url.startswith('http') else 'https://www.saudiexchange.sa' + url.strip()})

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
