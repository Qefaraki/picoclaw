#!/bin/bash
# market-monitor.sh — Check watchlist against alert thresholds
# Outputs NO_ALERT if nothing significant, otherwise outputs alerts JSON
# Usage: bash market-monitor.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REFERENCES_DIR="$SCRIPT_DIR/../references"
WATCHLIST="$REFERENCES_DIR/watchlist.json"

if [ ! -f "$WATCHLIST" ]; then
    echo '{"error": "watchlist.json not found"}'
    exit 1
fi

# Read thresholds
INDEX_THRESHOLD=$(jq -r '.alert_thresholds.index_pct' "$WATCHLIST")
STOCK_THRESHOLD=$(jq -r '.alert_thresholds.stock_pct' "$WATCHLIST")
COMMODITY_THRESHOLD=$(jq -r '.alert_thresholds.commodity_pct' "$WATCHLIST")
CRYPTO_THRESHOLD=$(jq -r '.alert_thresholds.crypto_pct' "$WATCHLIST")

# Fetch all market data
MARKET_DATA=$(bash "$SCRIPT_DIR/market-data.sh")

# Check each quote against appropriate threshold
ALERTS=$(echo "$MARKET_DATA" | jq --argjson idx "$INDEX_THRESHOLD" --argjson stk "$STOCK_THRESHOLD" \
    --argjson cmd "$COMMODITY_THRESHOLD" --argjson cry "$CRYPTO_THRESHOLD" \
    --slurpfile watchlist "$WATCHLIST" '
    .quotes | map(
        . as $q |
        ($watchlist[0].indices | keys) as $indices |
        ($watchlist[0].commodities | keys) as $commodities |
        ($watchlist[0].crypto_alerts_only | keys) as $crypto |
        if ($q.symbol | IN($indices[])) then
            if (($q.change_pct | abs) >= $idx) then . + {category: "index", threshold: $idx} else empty end
        elif ($q.symbol | IN($commodities[])) then
            if (($q.change_pct | abs) >= $cmd) then . + {category: "commodity", threshold: $cmd} else empty end
        elif ($q.symbol | IN($crypto[])) then
            if (($q.change_pct | abs) >= $cry) then . + {category: "crypto", threshold: $cry} else empty end
        else
            if (($q.change_pct | abs) >= $stk) then . + {category: "stock", threshold: $stk} else empty end
        end
    )
' 2>/dev/null || echo "[]")

# Check if any alerts
ALERT_COUNT=$(echo "$ALERTS" | jq 'length' 2>/dev/null || echo "0")

if [ "$ALERT_COUNT" = "0" ] || [ "$ALERT_COUNT" = "" ]; then
    echo "NO_ALERT"
else
    echo "{\"alerts\": $ALERTS, \"count\": $ALERT_COUNT, \"checked_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
fi
