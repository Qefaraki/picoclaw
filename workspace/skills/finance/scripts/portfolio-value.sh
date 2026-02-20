#!/bin/bash
# portfolio-value.sh — Portfolio valuation and P&L calculator
# Reads portfolio.json, fetches current prices, calculates unrealized P&L
# Usage: bash portfolio-value.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SPECIALIST_DIR="$(cd "$SCRIPT_DIR/../../../specialists/fahad/references" 2>/dev/null && pwd || echo "")"
PORTFOLIO_FILE="${SPECIALIST_DIR:+$SPECIALIST_DIR/portfolio.json}"

if [ -z "$PORTFOLIO_FILE" ] || [ ! -f "$PORTFOLIO_FILE" ]; then
    echo '{"error": "portfolio.json not found"}'
    exit 1
fi

# Check if there are any holdings
HOLDING_COUNT=$(jq '.holdings | length' "$PORTFOLIO_FILE" 2>/dev/null || echo "0")
if [ "$HOLDING_COUNT" = "0" ]; then
    echo '{"portfolio_value": {"status": "empty", "message": "No holdings in portfolio. Add positions first."}}'
    exit 0
fi

# Extract all symbols (holdings + benchmarks)
ALL_SYMBOLS=$(jq -r '([.holdings[].symbol] + [.benchmarks | values[]]) | unique | .[]' "$PORTFOLIO_FILE" 2>/dev/null)

# Fetch current prices using market-data.sh
MARKET_DATA=$(bash "$SCRIPT_DIR/market-data.sh" $ALL_SYMBOLS 2>/dev/null || echo '{"quotes": []}')

# Calculate P&L
echo "$MARKET_DATA" | python3 -c "
import json, sys
from datetime import datetime

market_data = json.loads(sys.stdin.read())

with open('$PORTFOLIO_FILE', 'r') as f:
    portfolio = json.load(f)

# Build price lookup
prices = {}
for q in market_data.get('quotes', []):
    prices[q['symbol']] = {
        'price': q.get('price', 0),
        'change_pct': q.get('change_pct', 0),
        'currency': q.get('currency', 'USD'),
        'name': q.get('name', q['symbol'])
    }

# Calculate per-position P&L
positions = []
totals_by_currency = {}

for h in portfolio.get('holdings', []):
    symbol = h['symbol']
    quantity = h.get('quantity', 0)
    avg_cost = h.get('avg_cost', 0)
    currency = h.get('currency', 'USD')

    current = prices.get(symbol, {})
    current_price = current.get('price', 0)
    day_change_pct = current.get('change_pct', 0)

    if current_price > 0 and avg_cost > 0:
        market_value = current_price * quantity
        cost_basis = avg_cost * quantity
        unrealized_pnl = market_value - cost_basis
        unrealized_pct = ((current_price - avg_cost) / avg_cost) * 100
        day_pnl = market_value * (day_change_pct / 100)
    else:
        market_value = 0
        cost_basis = avg_cost * quantity
        unrealized_pnl = 0
        unrealized_pct = 0
        day_pnl = 0

    positions.append({
        'symbol': symbol,
        'name': h.get('name', current.get('name', symbol)),
        'quantity': quantity,
        'avg_cost': avg_cost,
        'current_price': round(current_price, 2),
        'market_value': round(market_value, 2),
        'cost_basis': round(cost_basis, 2),
        'unrealized_pnl': round(unrealized_pnl, 2),
        'unrealized_pct': round(unrealized_pct, 2),
        'day_change_pct': round(day_change_pct, 2),
        'day_pnl': round(day_pnl, 2),
        'currency': currency
    })

    if currency not in totals_by_currency:
        totals_by_currency[currency] = {'market_value': 0, 'cost_basis': 0, 'unrealized_pnl': 0, 'day_pnl': 0}
    totals_by_currency[currency]['market_value'] += market_value
    totals_by_currency[currency]['cost_basis'] += cost_basis
    totals_by_currency[currency]['unrealized_pnl'] += unrealized_pnl
    totals_by_currency[currency]['day_pnl'] += day_pnl

# Round totals and compute return %
for cur in totals_by_currency:
    for k in totals_by_currency[cur]:
        totals_by_currency[cur][k] = round(totals_by_currency[cur][k], 2)
    cb = totals_by_currency[cur]['cost_basis']
    totals_by_currency[cur]['total_return_pct'] = round((totals_by_currency[cur]['unrealized_pnl'] / cb) * 100, 2) if cb > 0 else 0

# Benchmark performance
benchmarks = {}
for bench_name, bench_sym in portfolio.get('benchmarks', {}).items():
    if bench_sym in prices:
        benchmarks[bench_name] = {
            'symbol': bench_sym,
            'name': prices[bench_sym].get('name', bench_sym),
            'price': prices[bench_sym]['price'],
            'day_change_pct': round(prices[bench_sym]['change_pct'], 2)
        }

result = {
    'portfolio_value': {
        'positions': positions,
        'totals_by_currency': totals_by_currency,
        'cash': portfolio.get('cash', {}),
        'benchmarks': benchmarks,
        'holdings_count': len(positions),
        'calculated_at': datetime.utcnow().strftime('%Y-%m-%dT%H:%M:%SZ')
    }
}

print(json.dumps(result, indent=2))
" 2>/dev/null || echo '{"error": "portfolio valuation failed"}'
