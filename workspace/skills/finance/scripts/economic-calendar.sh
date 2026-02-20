#!/bin/bash
# economic-calendar.sh — Economic events + earnings calendar
# Economic events: Forex Factory API (free, no key needed)
# Earnings: Finnhub API (free tier, key optional)
# Usage: bash economic-calendar.sh [FINNHUB_API_KEY]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="$SCRIPT_DIR/../references/.env"
[ -f "$ENV_FILE" ] && source "$ENV_FILE"

API_KEY="${1:-${FINNHUB_API_KEY:-}}"

# Date range
FROM_DATE=$(date -u +%Y-%m-%d)
TO_DATE=$(date -u -v+7d +%Y-%m-%d 2>/dev/null || date -u -d "+7 days" +%Y-%m-%d 2>/dev/null || echo "")
if [ -z "$TO_DATE" ]; then
    TO_DATE=$(python3 -c "from datetime import datetime, timedelta; print((datetime.now() + timedelta(days=7)).strftime('%Y-%m-%d'))")
fi

# --- Forex Factory Economic Events (free, no key) ---
# Cache to respect rate limit (2 requests per 5 minutes)
FF_CACHE="/tmp/ff_calendar_cache.json"
CACHE_MAX_AGE=7200  # 2 hours in seconds

fetch_ff_calendar() {
    local need_fetch=1

    if [ -f "$FF_CACHE" ]; then
        # Check cache age (BSD stat vs GNU stat)
        local file_mtime
        file_mtime=$(stat -f %m "$FF_CACHE" 2>/dev/null || stat -c %Y "$FF_CACHE" 2>/dev/null || echo "0")
        local now
        now=$(date +%s)
        local age=$(( now - file_mtime ))
        if [ "$age" -lt "$CACHE_MAX_AGE" ]; then
            need_fetch=0
        fi
    fi

    if [ "$need_fetch" -eq 1 ]; then
        local data
        data=$(curl -s --max-time 15 "https://nfs.faireconomy.media/ff_calendar_thisweek.json" 2>/dev/null || echo "")
        if [ -n "$data" ] && echo "$data" | jq . >/dev/null 2>&1; then
            echo "$data" > "$FF_CACHE"
        fi
    fi

    if [ -f "$FF_CACHE" ]; then
        cat "$FF_CACHE"
    else
        echo "[]"
    fi
}

# Fetch and filter FF calendar
FF_RAW=$(fetch_ff_calendar)

# Calculate 48-hour cutoff timestamp
CUTOFF_ISO=$(date -u -v+48H +%Y-%m-%dT%H:%M:%S 2>/dev/null || date -u -d "+48 hours" +%Y-%m-%dT%H:%M:%S 2>/dev/null || echo "")
if [ -z "$CUTOFF_ISO" ]; then
    CUTOFF_ISO=$(python3 -c "from datetime import datetime, timedelta; print((datetime.utcnow() + timedelta(hours=48)).strftime('%Y-%m-%dT%H:%M:%S'))")
fi
NOW_ISO=$(date -u +%Y-%m-%dT%H:%M:%S)

ECON_EVENTS=$(echo "$FF_RAW" | jq --arg now "$NOW_ISO" --arg cutoff "$CUTOFF_ISO" '
    [.[] |
        select(.impact == "High" or .impact == "Medium") |
        select(.country == "USD" or .country == "GBP" or .country == "EUR" or .country == "CAD" or .country == "AUD" or .country == "JPY" or .country == "CHF" or .country == "CNY" or .country == "NZD") |
        select(.date >= $now and .date <= $cutoff) |
        {
            date: .date,
            country: .country,
            event: .title,
            impact: .impact,
            forecast: .forecast,
            previous: .previous
        }
    ] | sort_by(.date) | .[0:25]
' 2>/dev/null || echo "[]")

echo '{"economic_calendar": {'

# Economic events section
echo '"events": '
if [ "$ECON_EVENTS" != "[]" ] && [ -n "$ECON_EVENTS" ]; then
    echo "$ECON_EVENTS"
else
    echo '[]'
fi

echo ','

# --- Earnings Calendar (Finnhub free tier, optional) ---
echo '"earnings": '
if [ -n "$API_KEY" ]; then
    EARNINGS_DATA=$(curl -s --max-time 15 \
        "https://finnhub.io/api/v1/calendar/earnings?from=${FROM_DATE}&to=${TO_DATE}&token=${API_KEY}" 2>/dev/null || echo "")

    if [ -n "$EARNINGS_DATA" ]; then
        echo "$EARNINGS_DATA" | jq '{
            earnings: [.earningsCalendar[] |
                {
                    date: .date,
                    symbol: .symbol,
                    eps_estimate: .epsEstimate,
                    eps_actual: .epsActual,
                    revenue_estimate: .revenueEstimate,
                    revenue_actual: .revenueActual,
                    hour: .hour
                }
            ] | sort_by(.date) | .[0:20]
        }' 2>/dev/null || echo '{"earnings": []}'
    else
        echo '{"earnings": [], "error": "Failed to fetch earnings calendar"}'
    fi
else
    echo '{"earnings": [], "note": "Finnhub API key not set, skipping earnings. Get free key at https://finnhub.io"}'
fi

echo '},'
echo "\"source\": \"forex_factory+finnhub\","
echo "\"from\": \"$FROM_DATE\","
echo "\"to\": \"$TO_DATE\","
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'
