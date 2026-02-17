#!/bin/bash
# market-data.sh — Fetch live stock/commodity/index quotes from Yahoo Finance
# Usage: bash market-data.sh [SYMBOL1] [SYMBOL2] ...
# If no symbols provided, reads from watchlist.json

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REFERENCES_DIR="$SCRIPT_DIR/../references"
WATCHLIST="$REFERENCES_DIR/watchlist.json"

# Get symbols from args or watchlist
if [ $# -gt 0 ]; then
    SYMBOLS=("$@")
else
    if [ ! -f "$WATCHLIST" ]; then
        echo '{"error": "No symbols provided and watchlist.json not found"}'
        exit 1
    fi
    # Extract all symbols from watchlist (all categories except alert_thresholds)
    SYMBOLS=($(jq -r '
        [.indices, .saudi_stocks, .us_stocks, .commodities, .crypto_alerts_only, .currencies]
        | map(keys) | flatten | .[]
    ' "$WATCHLIST"))
fi

echo '{"quotes": ['

FIRST=true
for SYMBOL in "${SYMBOLS[@]}"; do
    # URL-encode the symbol
    ENCODED=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$SYMBOL'))" 2>/dev/null || echo "$SYMBOL")

    # Fetch from Yahoo Finance v8 API
    RESPONSE=$(curl -s --max-time 10 \
        -H "User-Agent: Mozilla/5.0" \
        "https://query1.finance.yahoo.com/v8/finance/chart/${ENCODED}?range=1d&interval=5m" 2>/dev/null || echo "")

    if [ -z "$RESPONSE" ]; then
        continue
    fi

    # Parse response with jq
    QUOTE=$(echo "$RESPONSE" | jq -r '
        .chart.result[0] as $r |
        $r.meta as $m |
        {
            symbol: $m.symbol,
            name: ($m.longName // $m.shortName // $m.symbol),
            price: $m.regularMarketPrice,
            previous_close: $m.previousClose,
            change: (($m.regularMarketPrice - $m.previousClose) * 100 | round | . / 100),
            change_pct: ((($m.regularMarketPrice - $m.previousClose) / $m.previousClose * 100) * 100 | round | . / 100),
            currency: $m.currency,
            market_state: $m.marketState,
            exchange: $m.exchangeName
        }
    ' 2>/dev/null || echo "")

    if [ -n "$QUOTE" ] && [ "$QUOTE" != "null" ] && [ "$QUOTE" != "" ]; then
        if [ "$FIRST" = true ]; then
            FIRST=false
        else
            echo ","
        fi
        echo "$QUOTE"
    fi

    # Small delay to avoid rate limiting
    sleep 0.2
done

echo '],'
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
echo "\"count\": ${#SYMBOLS[@]}"
echo '}'
