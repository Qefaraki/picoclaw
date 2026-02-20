#!/bin/bash
# fear-greed.sh — CNN Fear & Greed Index + VIX + Crypto Fear & Greed
# Usage: bash fear-greed.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo '{"sentiment": {'

# --- CNN Fear & Greed Index ---
# NOTE: CNN blocks server-side requests (HTTP 418). Try alternative endpoint first.
echo '"fear_greed": '
FG_DATA=$(curl -s --max-time 15 \
    -H "User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/121.0.0.0" \
    -H "Referer: https://www.cnn.com/markets/fear-and-greed" \
    -H "Accept: application/json" \
    "https://production.dataviz.cnn.io/index/fearandgreed/graphdata/" 2>/dev/null || echo "")

FG_PARSED=""
if [ -n "$FG_DATA" ]; then
    FG_PARSED=$(echo "$FG_DATA" | jq '{
        score: .fear_and_greed.score,
        rating: .fear_and_greed.rating,
        previous_close: .fear_and_greed_historical.data[-2].x // null,
        previous_score: .fear_and_greed_historical.data[-2].y // null,
        one_week_ago: .fear_and_greed.previous_1_week,
        one_month_ago: .fear_and_greed.previous_1_month,
        one_year_ago: .fear_and_greed.previous_1_year
    }' 2>/dev/null || echo "")
fi

if [ -n "$FG_PARSED" ] && [ "$FG_PARSED" != "" ]; then
    echo "$FG_PARSED"
else
    echo '{"note": "CNN Fear & Greed API blocked (HTTP 418). Use web_fetch tool on https://www.cnn.com/markets/fear-and-greed for manual check. VIX below provides equivalent sentiment signal."}'
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

echo ','

# --- Crypto Fear & Greed Index (Alternative.me) ---
# Free API, no key needed. Returns 0-100 scale (0=Extreme Fear, 100=Extreme Greed)
echo '"crypto_fear_greed": '
CRYPTO_FG=$(curl -s --max-time 10 "https://api.alternative.me/fng/?limit=2" 2>/dev/null || echo "")

if [ -n "$CRYPTO_FG" ]; then
    echo "$CRYPTO_FG" | jq '{
        value: (.data[0].value | tonumber),
        classification: .data[0].value_classification,
        previous_value: (.data[1].value | tonumber),
        previous_classification: .data[1].value_classification,
        trend: (
            if (.data[0].value | tonumber) > (.data[1].value | tonumber) then "improving"
            elif (.data[0].value | tonumber) < (.data[1].value | tonumber) then "deteriorating"
            else "stable"
            end
        ),
        timestamp: .data[0].timestamp
    }' 2>/dev/null || echo '{"error": "Failed to parse crypto Fear & Greed data"}'
else
    echo '{"error": "Failed to fetch crypto Fear & Greed data"}'
fi

echo '},'
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'
