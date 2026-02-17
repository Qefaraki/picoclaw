#!/bin/bash
# fear-greed.sh — CNN Fear & Greed Index + VIX
# Usage: bash fear-greed.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo '{"sentiment": {'

# --- CNN Fear & Greed Index ---
echo '"fear_greed": '
FG_DATA=$(curl -s --max-time 15 \
    -H "User-Agent: Mozilla/5.0" \
    "https://production.dataviz.cnn.io/index/fearandgreed/graphdata/" 2>/dev/null || echo "")

if [ -n "$FG_DATA" ] && [ "$FG_DATA" != "" ]; then
    echo "$FG_DATA" | jq '{
        score: .fear_and_greed.score,
        rating: .fear_and_greed.rating,
        previous_close: .fear_and_greed_historical.data[-2].x // null,
        previous_score: .fear_and_greed_historical.data[-2].y // null,
        one_week_ago: .fear_and_greed.previous_1_week,
        one_month_ago: .fear_and_greed.previous_1_month,
        one_year_ago: .fear_and_greed.previous_1_year
    }' 2>/dev/null || echo '{"error": "Failed to parse Fear & Greed data"}'
else
    echo '{"error": "Failed to fetch Fear & Greed data"}'
fi

echo ','

# --- VIX (CBOE Volatility Index) ---
echo '"vix": '
VIX_DATA=$(curl -s --max-time 10 \
    -H "User-Agent: Mozilla/5.0" \
    "https://query1.finance.yahoo.com/v8/finance/chart/%5EVIX?range=1d&interval=5m" 2>/dev/null || echo "")

if [ -n "$VIX_DATA" ]; then
    echo "$VIX_DATA" | jq '
        .chart.result[0].meta as $m |
        {
            value: $m.regularMarketPrice,
            previous_close: $m.previousClose,
            change: (($m.regularMarketPrice - $m.previousClose) * 100 | round | . / 100),
            change_pct: ((($m.regularMarketPrice - $m.previousClose) / $m.previousClose * 100) * 100 | round | . / 100),
            interpretation: (
                if $m.regularMarketPrice < 15 then "Low volatility (complacency)"
                elif $m.regularMarketPrice < 20 then "Normal"
                elif $m.regularMarketPrice < 25 then "Elevated"
                elif $m.regularMarketPrice < 30 then "High (fear)"
                else "Extreme fear / panic"
                end
            )
        }
    ' 2>/dev/null || echo '{"error": "Failed to parse VIX data"}'
else
    echo '{"error": "Failed to fetch VIX data"}'
fi

echo '},'
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'
