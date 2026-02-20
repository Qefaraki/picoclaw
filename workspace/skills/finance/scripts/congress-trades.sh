#!/bin/bash
# congress-trades.sh — Congressional stock trades from House/Senate Stock Watcher
# Filters: recent (30 days), amount >$50K, tracks state for new-only detection
# Usage: bash congress-trades.sh [--all]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SPECIALIST_DIR="$(cd "$SCRIPT_DIR/../../../specialists/fahad/references" 2>/dev/null && pwd || echo "")"
STATE_FILE="${SPECIALIST_DIR:+$SPECIALIST_DIR/congress-state.json}"

SHOW_ALL=false
[ "${1:-}" = "--all" ] && SHOW_ALL=true

DAYS_BACK=30
MIN_AMOUNT=50000
CUTOFF_DATE=$(date -u -v-${DAYS_BACK}d +%Y-%m-%d 2>/dev/null || date -u -d "-${DAYS_BACK} days" +%Y-%m-%d 2>/dev/null || python3 -c "from datetime import datetime,timedelta;print((datetime.utcnow()-timedelta(days=$DAYS_BACK)).strftime('%Y-%m-%d'))" 2>/dev/null || echo "2025-01-01")

# Key politicians to always flag
KEY_POLITICIANS='["Nancy Pelosi", "Dan Crenshaw", "Tommy Tuberville", "Josh Gottheimer", "Mark Green", "Michael McCaul", "Pat Fallon", "Marjorie Taylor Greene"]'

echo '{"congress_trades": {'

# --- House trades ---
echo '"house": '
HOUSE_DATA=$(curl -s --max-time 30 \
    "https://house-stock-watcher-data.s3-us-west-2.amazonaws.com/data/all_transactions.json" 2>/dev/null || echo "[]")

if [ "$HOUSE_DATA" != "[]" ] && [ -n "$HOUSE_DATA" ]; then
    echo "$HOUSE_DATA" | jq --arg cutoff "$CUTOFF_DATE" --argjson min_amt "$MIN_AMOUNT" --argjson key "$KEY_POLITICIANS" '
        [.[] |
            select(.transaction_date >= $cutoff) |
            # Parse amount range to get lower bound
            (.amount | gsub("\\$|,"; "") | split(" - ") | .[0] | tonumber) as $amt |
            select($amt >= $min_amt or (.representative | IN($key[]))) |
            {
                politician: .representative,
                party: .party,
                state: .state,
                stock: .ticker,
                company: .asset_description,
                type: .type,
                amount: .amount,
                date: .transaction_date,
                disclosure_date: .disclosure_date,
                chamber: "House",
                is_key_politician: (.representative | IN($key[]))
            }
        ] | sort_by(.date) | reverse | .[0:50]
    ' 2>/dev/null || echo "[]"
else
    echo "[]"
fi

echo ','

# --- Senate trades ---
echo '"senate": '
SENATE_DATA=$(curl -s --max-time 30 \
    "https://senate-stock-watcher-data.s3-us-west-2.amazonaws.com/aggregate/all_transactions.json" 2>/dev/null || echo "[]")

if [ "$SENATE_DATA" != "[]" ] && [ -n "$SENATE_DATA" ]; then
    echo "$SENATE_DATA" | jq --arg cutoff "$CUTOFF_DATE" --argjson min_amt "$MIN_AMOUNT" --argjson key "$KEY_POLITICIANS" '
        [.[] |
            select(.transaction_date >= $cutoff) |
            (.amount | gsub("\\$|,"; "") | split(" - ") | .[0] | ltrimstr(" ") | tonumber) as $amt |
            select($amt >= $min_amt or (.senator | IN($key[]))) |
            {
                politician: .senator,
                party: .party,
                state: .state,
                stock: .ticker,
                company: .asset_description,
                type: .type,
                amount: .amount,
                date: .transaction_date,
                disclosure_date: .disclosure_date,
                chamber: "Senate",
                is_key_politician: (.senator | IN($key[]))
            }
        ] | sort_by(.date) | reverse | .[0:50]
    ' 2>/dev/null || echo "[]"
else
    echo "[]"
fi

echo '},'
echo "\"cutoff_date\": \"$CUTOFF_DATE\","
echo "\"min_amount\": $MIN_AMOUNT,"
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'

# Update state file if available
if [ -n "$STATE_FILE" ] && [ -f "$STATE_FILE" ]; then
    jq --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '.last_checked = $ts' "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
fi
