#!/bin/bash
# morning-briefing.sh — Master script: runs all data scripts, outputs combined JSON
# Usage: bash morning-briefing.sh [FINNHUB_API_KEY]

set -uo pipefail
# Don't use set -e: one script failure shouldn't abort the whole briefing

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="$SCRIPT_DIR/../references/.env"
[ -f "$ENV_FILE" ] && source "$ENV_FILE"
API_KEY="${1:-${FINNHUB_API_KEY:-}}"

# Validate JSON output from child scripts; return error JSON on failure
validate_json() {
    local label="$1"
    local output="$2"
    if [ -z "$output" ]; then
        echo "{\"error\": \"${label} returned empty output\"}"
    elif echo "$output" | jq . >/dev/null 2>&1; then
        echo "$output"
    else
        echo "{\"error\": \"${label} returned invalid JSON\"}"
    fi
}

echo '{"morning_briefing": {'
echo "\"generated_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
echo "\"day\": \"$(date +%A)\","

# 1. Market Data
MARKET_OUT=$(timeout 60 bash "$SCRIPT_DIR/market-data.sh" 2>/dev/null || echo '{"error": "market-data failed"}')
echo '"market_data": '
validate_json "market-data" "$MARKET_OUT"
echo ','

# 2. Fear & Greed + VIX
SENTIMENT_OUT=$(timeout 60 bash "$SCRIPT_DIR/fear-greed.sh" 2>/dev/null || echo '{"error": "fear-greed failed"}')
echo '"sentiment": '
validate_json "fear-greed" "$SENTIMENT_OUT"
echo ','

# 3. News
NEWS_OUT=$(timeout 90 bash "$SCRIPT_DIR/news-fetch.sh" all 2>/dev/null || echo '{"error": "news-fetch failed"}')
echo '"news": '
validate_json "news-fetch" "$NEWS_OUT"
echo ','

# 4. Congressional Trades
CONGRESS_OUT=$(timeout 60 bash "$SCRIPT_DIR/congress-trades.sh" 2>/dev/null || echo '{"error": "congress-trades failed"}')
echo '"congress": '
validate_json "congress-trades" "$CONGRESS_OUT"
echo ','

# 5. Substack Newsletters
NEWSLETTERS_OUT=$(timeout 60 bash "$SCRIPT_DIR/substack-feeds.sh" 2>/dev/null || echo '{"error": "substack-feeds failed"}')
echo '"newsletters": '
validate_json "substack-feeds" "$NEWSLETTERS_OUT"
echo ','

# 6. Saudi IPOs
IPO_OUT=$(timeout 60 bash "$SCRIPT_DIR/saudi-ipo.sh" 2>/dev/null || echo '{"error": "saudi-ipo failed"}')
echo '"saudi_ipos": '
validate_json "saudi-ipo" "$IPO_OUT"
echo ','

# 7. Economic Calendar (FF API free, Finnhub key optional for earnings)
CALENDAR_OUT=$(timeout 60 bash "$SCRIPT_DIR/economic-calendar.sh" "$API_KEY" 2>/dev/null || echo '{"error": "economic-calendar failed"}')
echo '"economic_calendar": '
validate_json "economic-calendar" "$CALENDAR_OUT"
echo ','

# 8. Portfolio Valuation (reads portfolio.json, fetches live prices, calculates P&L)
PORTFOLIO_OUT=$(timeout 60 bash "$SCRIPT_DIR/portfolio-value.sh" 2>/dev/null || echo '{"portfolio_value": {"status": "empty"}}')
echo '"portfolio": '
validate_json "portfolio-value" "$PORTFOLIO_OUT"

echo '}'
echo '}'
