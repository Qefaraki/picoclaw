#!/bin/bash
# price-history.sh — Fetch historical OHLCV data from Yahoo Finance
# Usage: bash price-history.sh SYMBOL [range] [interval]
# Defaults: range=6mo, interval=1d
# Examples:
#   bash price-history.sh AAPL                  # 6 months daily
#   bash price-history.sh 2222.SR 1y 1wk        # 1 year weekly
#   bash price-history.sh GC=F 1mo 1d           # 1 month daily

set -euo pipefail

SYMBOL="${1:-}"
RANGE="${2:-6mo}"
INTERVAL="${3:-1d}"

if [ -z "$SYMBOL" ]; then
    echo '{"error": "Usage: bash price-history.sh SYMBOL [range] [interval]"}'
    exit 1
fi

# URL-encode the symbol
ENCODED=$(python3 -c "import urllib.parse; print(urllib.parse.quote('$SYMBOL'))" 2>/dev/null || echo "$SYMBOL")

# Fetch from Yahoo Finance
RESPONSE=$(curl -s --max-time 15 \
    -H "User-Agent: Mozilla/5.0" \
    "https://query1.finance.yahoo.com/v8/finance/chart/${ENCODED}?range=${RANGE}&interval=${INTERVAL}" 2>/dev/null || echo "")

if [ -z "$RESPONSE" ]; then
    echo "{\"error\": \"Failed to fetch data for $SYMBOL\"}"
    exit 1
fi

# Parse into structured OHLCV data with moving averages
echo "$RESPONSE" | python3 -c "
import json, sys
from datetime import datetime

data = json.loads(sys.stdin.read())
result_data = data.get('chart', {}).get('result', [])
if not result_data:
    print(json.dumps({'error': 'No data returned for symbol'}))
    sys.exit(0)

r = result_data[0]
meta = r.get('meta', {})
timestamps = r.get('timestamp', [])
indicators = r.get('indicators', {})
quotes = indicators.get('quote', [{}])[0]

opens = quotes.get('open', [])
highs = quotes.get('high', [])
lows = quotes.get('low', [])
closes = quotes.get('close', [])
volumes = quotes.get('volume', [])

# Build OHLCV array
ohlcv = []
for i in range(len(timestamps)):
    if closes[i] is None:
        continue
    ohlcv.append({
        'date': datetime.utcfromtimestamp(timestamps[i]).strftime('%Y-%m-%d'),
        'open': round(opens[i], 2) if opens[i] else None,
        'high': round(highs[i], 2) if highs[i] else None,
        'low': round(lows[i], 2) if lows[i] else None,
        'close': round(closes[i], 2) if closes[i] else None,
        'volume': volumes[i] if i < len(volumes) else None
    })

# Calculate moving averages from closing prices
close_prices = [p['close'] for p in ohlcv if p['close'] is not None]

def moving_avg(prices, period):
    if len(prices) < period:
        return None
    return round(sum(prices[-period:]) / period, 2)

ma_20 = moving_avg(close_prices, 20)
ma_50 = moving_avg(close_prices, 50)
ma_200 = moving_avg(close_prices, 200)

# Calculate support/resistance (simple: recent lows/highs)
if len(close_prices) >= 5:
    recent = close_prices[-20:] if len(close_prices) >= 20 else close_prices
    support = round(min(recent), 2)
    resistance = round(max(recent), 2)
else:
    support = None
    resistance = None

# Performance stats
if len(close_prices) >= 2:
    period_return = round(((close_prices[-1] - close_prices[0]) / close_prices[0]) * 100, 2)
    period_high = round(max(close_prices), 2)
    period_low = round(min(close_prices), 2)
    current = close_prices[-1]
    from_high = round(((current - period_high) / period_high) * 100, 2)
    from_low = round(((current - period_low) / period_low) * 100, 2)
else:
    period_return = 0
    period_high = close_prices[-1] if close_prices else 0
    period_low = close_prices[-1] if close_prices else 0
    from_high = 0
    from_low = 0

result = {
    'price_history': {
        'symbol': meta.get('symbol', '$SYMBOL'),
        'name': meta.get('longName') or meta.get('shortName') or meta.get('symbol', '$SYMBOL'),
        'currency': meta.get('currency', 'USD'),
        'range': '$RANGE',
        'interval': '$INTERVAL',
        'data_points': len(ohlcv),
        'latest': ohlcv[-1] if ohlcv else None,
        'moving_averages': {
            'ma_20': ma_20,
            'ma_50': ma_50,
            'ma_200': ma_200,
        },
        'levels': {
            'support': support,
            'resistance': resistance,
        },
        'performance': {
            'period_return_pct': period_return,
            'period_high': period_high,
            'period_low': period_low,
            'from_high_pct': from_high,
            'from_low_pct': from_low,
        },
        'ohlcv': ohlcv[-30:],  # Last 30 data points to keep output manageable
    }
}

print(json.dumps(result, indent=2))
" 2>/dev/null || echo "{\"error\": \"Failed to parse history for $SYMBOL\"}"
