#!/bin/bash
# expenses-parse.sh — Bank statement parser (CSV)
# Parses Revolut-style CSV transactions, categorizes and summarizes
# Usage: bash expenses-parse.sh <csv_file>

set -euo pipefail

CSV_FILE="${1:-}"

if [ -z "$CSV_FILE" ]; then
    echo '{"error": "Usage: bash expenses-parse.sh <csv_file>"}'
    exit 1
fi

if [ ! -f "$CSV_FILE" ]; then
    echo "{\"error\": \"File not found: $CSV_FILE\"}"
    exit 1
fi

python3 << 'PYEOF'
import csv
import json
import sys
from collections import defaultdict
from datetime import datetime

csv_file = sys.argv[1] if len(sys.argv) > 1 else ""

# Category mapping for common merchants
CATEGORY_MAP = {
    "transport": ["uber", "tfl", "transport", "lyft", "bolt", "taxi", "train", "bus", "flight", "airline", "easyjet", "ryanair"],
    "food_dining": ["restaurant", "cafe", "coffee", "starbucks", "mcdonald", "kfc", "nandos", "deliveroo", "uber eats", "just eat"],
    "groceries": ["tesco", "sainsbury", "asda", "waitrose", "lidl", "aldi", "co-op", "morrisons", "ocado", "grocery"],
    "shopping": ["amazon", "asos", "zara", "h&m", "primark", "apple", "john lewis", "argos"],
    "entertainment": ["netflix", "spotify", "cinema", "theatre", "gaming", "steam", "playstation", "xbox"],
    "health": ["pharmacy", "boots", "gym", "fitness", "doctor", "hospital", "dental"],
    "education": ["book", "course", "udemy", "university", "tuition"],
    "bills": ["rent", "electricity", "gas", "water", "internet", "phone", "insurance", "council tax"],
    "transfers": ["transfer", "sent", "received", "exchange"],
    "subscriptions": ["subscription", "monthly", "annual", "premium"],
}

def categorize(description):
    desc_lower = description.lower()
    for category, keywords in CATEGORY_MAP.items():
        if any(kw in desc_lower for kw in keywords):
            return category
    return "other"

transactions = []
monthly = defaultdict(lambda: defaultdict(float))
category_totals = defaultdict(float)
total_spend = 0
total_income = 0

try:
    with open(csv_file, 'r', encoding='utf-8-sig') as f:
        reader = csv.DictReader(f)
        for row in reader:
            # Try common CSV column names
            amount = None
            for col in ['Amount', 'amount', 'Value', 'value', 'Completed (GBP)', 'Amount (GBP)']:
                if col in row and row[col]:
                    try:
                        amount = float(row[col].replace(',', '').replace('£', '').replace('$', '').replace('﷼', ''))
                        break
                    except ValueError:
                        continue

            description = ""
            for col in ['Description', 'description', 'Reference', 'Merchant', 'Name']:
                if col in row and row[col]:
                    description = row[col]
                    break

            date_str = ""
            for col in ['Date', 'date', 'Started Date', 'Completed Date', 'Transaction Date']:
                if col in row and row[col]:
                    date_str = row[col]
                    break

            if amount is None:
                continue

            category = row.get('Category', '') or row.get('category', '') or categorize(description)

            # Parse date for monthly grouping
            month_key = "unknown"
            for fmt in ["%Y-%m-%d", "%d/%m/%Y", "%m/%d/%Y", "%Y-%m-%d %H:%M:%S", "%d %b %Y"]:
                try:
                    dt = datetime.strptime(date_str.strip()[:10], fmt)
                    month_key = dt.strftime("%Y-%m")
                    break
                except ValueError:
                    continue

            tx = {
                "date": date_str.strip(),
                "description": description.strip(),
                "amount": amount,
                "category": category.lower(),
                "month": month_key
            }
            transactions.append(tx)

            if amount < 0:
                total_spend += abs(amount)
                monthly[month_key][category.lower()] += abs(amount)
                category_totals[category.lower()] += abs(amount)
            else:
                total_income += amount

except Exception as e:
    print(json.dumps({"error": str(e)}))
    sys.exit(1)

# Build monthly summaries
monthly_summaries = {}
for month, cats in sorted(monthly.items()):
    monthly_summaries[month] = {
        "total": round(sum(cats.values()), 2),
        "categories": {k: round(v, 2) for k, v in sorted(cats.items(), key=lambda x: -x[1])}
    }

result = {
    "summary": {
        "total_transactions": len(transactions),
        "total_spend": round(total_spend, 2),
        "total_income": round(total_income, 2),
        "net": round(total_income - total_spend, 2),
        "months_covered": len(monthly_summaries)
    },
    "category_totals": {k: round(v, 2) for k, v in sorted(category_totals.items(), key=lambda x: -x[1])},
    "monthly": monthly_summaries,
    "top_expenses": sorted(
        [t for t in transactions if t["amount"] < 0],
        key=lambda x: x["amount"]
    )[:20]
}

print(json.dumps(result, indent=2))
PYEOF
