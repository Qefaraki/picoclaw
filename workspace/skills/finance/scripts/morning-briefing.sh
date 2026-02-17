#!/bin/bash
# morning-briefing.sh — Master script: runs all data scripts, outputs combined JSON
# Usage: bash morning-briefing.sh [FINNHUB_API_KEY]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="$SCRIPT_DIR/../references/.env"
[ -f "$ENV_FILE" ] && source "$ENV_FILE"
API_KEY="${1:-${FINNHUB_API_KEY:-}}"

echo '{"morning_briefing": {'
echo "\"generated_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
echo "\"day\": \"$(date +%A)\","

# 1. Market Data
echo '"market_data": '
bash "$SCRIPT_DIR/market-data.sh" 2>/dev/null || echo '{"error": "market-data failed"}'
echo ','

# 2. Fear & Greed + VIX
echo '"sentiment": '
bash "$SCRIPT_DIR/fear-greed.sh" 2>/dev/null || echo '{"error": "fear-greed failed"}'
echo ','

# 3. News
echo '"news": '
bash "$SCRIPT_DIR/news-fetch.sh" all 2>/dev/null || echo '{"error": "news-fetch failed"}'
echo ','

# 4. Congressional Trades
echo '"congress": '
bash "$SCRIPT_DIR/congress-trades.sh" 2>/dev/null || echo '{"error": "congress-trades failed"}'
echo ','

# 5. Substack Newsletters
echo '"newsletters": '
bash "$SCRIPT_DIR/substack-feeds.sh" 2>/dev/null || echo '{"error": "substack-feeds failed"}'
echo ','

# 6. Saudi IPOs
echo '"saudi_ipos": '
bash "$SCRIPT_DIR/saudi-ipo.sh" 2>/dev/null || echo '{"error": "saudi-ipo failed"}'
echo ','

# 7. Economic Calendar (if API key available)
echo '"economic_calendar": '
if [ -n "$API_KEY" ]; then
    bash "$SCRIPT_DIR/economic-calendar.sh" "$API_KEY" 2>/dev/null || echo '{"error": "economic-calendar failed"}'
else
    echo '{"note": "Finnhub API key not set, skipping economic calendar"}'
fi

echo '}'
echo '}'
