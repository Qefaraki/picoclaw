#!/bin/bash
# economic-calendar.sh — Economic events + earnings calendar
# Uses Finnhub API (free tier)
# Usage: bash economic-calendar.sh [FINNHUB_API_KEY]
# If no key provided, checks FINNHUB_API_KEY env var

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="$SCRIPT_DIR/../references/.env"
[ -f "$ENV_FILE" ] && source "$ENV_FILE"

API_KEY="${1:-${FINNHUB_API_KEY:-}}"

if [ -z "$API_KEY" ]; then
    echo '{"error": "Finnhub API key required. Pass as argument or set FINNHUB_API_KEY env var. Get free key at https://finnhub.io"}'
    exit 1
fi

# Date range: today + next 7 days
FROM_DATE=$(date -u +%Y-%m-%d)
TO_DATE=$(date -u -v+7d +%Y-%m-%d 2>/dev/null || date -u -d "+7 days" +%Y-%m-%d 2>/dev/null || echo "")

if [ -z "$TO_DATE" ]; then
    # Fallback: manual date calculation
    TO_DATE=$(python3 -c "from datetime import datetime, timedelta; print((datetime.now() + timedelta(days=7)).strftime('%Y-%m-%d'))")
fi

echo '{"economic_calendar": {'

# --- Economic Calendar ---
echo '"events": '
ECON_DATA=$(curl -s --max-time 15 \
    "https://finnhub.io/api/v1/calendar/economic?from=${FROM_DATE}&to=${TO_DATE}&token=${API_KEY}" 2>/dev/null || echo "")

if [ -n "$ECON_DATA" ]; then
    echo "$ECON_DATA" | jq '{
        events: [.economicCalendar[] |
            select(.impact == "high" or .impact == "medium") |
            {
                date: .date,
                time: .time,
                country: .country,
                event: .event,
                impact: .impact,
                actual: .actual,
                estimate: .estimate,
                previous: .prev
            }
        ] | sort_by(.date, .time) | .[0:20]
    }' 2>/dev/null || echo '{"events": []}'
else
    echo '{"events": [], "error": "Failed to fetch economic calendar"}'
fi

echo ','

# --- Earnings Calendar ---
echo '"earnings": '
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

echo '},'
echo "\"from\": \"$FROM_DATE\","
echo "\"to\": \"$TO_DATE\","
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'
