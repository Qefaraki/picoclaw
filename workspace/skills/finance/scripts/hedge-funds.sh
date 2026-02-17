#!/bin/bash
# hedge-funds.sh — 13F filings from SEC EDGAR + Dataroma superinvestors
# Includes PIF (Saudi Public Investment Fund, CIK 0001767640)
# Usage: bash hedge-funds.sh

set -euo pipefail

# SEC EDGAR requires identifying User-Agent
SEC_UA="PicoClaw/1.0 (financial research bot)"

# Key funds to track
declare -A FUND_CIKS=(
    ["PIF (Saudi Public Investment Fund)"]="0001767640"
    ["Berkshire Hathaway"]="0001067983"
    ["Bridgewater Associates"]="0001350694"
    ["Renaissance Technologies"]="0001037389"
    ["Pershing Square"]="0001336528"
    ["Third Point"]="0001040273"
    ["Appaloosa Management"]="0001656456"
)

# Dataroma superinvestors
declare -A DATAROMA_FUNDS=(
    ["Warren Buffett"]="brk"
    ["Bill Ackman"]="psq"
    ["Seth Klarman"]="baupost"
    ["David Tepper"]="app"
    ["Dan Loeb"]="third"
)

echo '{"institutional_activity": {'

# --- SEC EDGAR 13F filings ---
echo '"sec_13f": ['
FIRST=true

for FUND_NAME in "${!FUND_CIKS[@]}"; do
    CIK="${FUND_CIKS[$FUND_NAME]}"

    # Search for recent 13F filings
    FILINGS=$(curl -s --max-time 15 \
        -H "User-Agent: $SEC_UA" \
        -H "Accept: application/json" \
        "https://efts.sec.gov/LATEST/search-index?q=%22${CIK}%22&dateRange=custom&startdt=$(date -u -v-90d +%Y-%m-%d 2>/dev/null || date -u -d '-90 days' +%Y-%m-%d)&enddt=$(date -u +%Y-%m-%d)&forms=13F-HR" 2>/dev/null || echo "")

    # Also try the EDGAR submissions API
    SUBMISSIONS=$(curl -s --max-time 15 \
        -H "User-Agent: $SEC_UA" \
        -H "Accept: application/json" \
        "https://data.sec.gov/submissions/CIK${CIK}.json" 2>/dev/null || echo "")

    if [ -n "$SUBMISSIONS" ] && [ "$SUBMISSIONS" != "" ]; then
        LATEST=$(echo "$SUBMISSIONS" | jq -r --arg fund "$FUND_NAME" '
            .recentFilings // .filings.recent |
            if . then
                {
                    fund: $fund,
                    cik: (.cik // ""),
                    latest_filing: (
                        [range((.form // []) | length)] |
                        map(select((.form[.] // "") == "13F-HR")) |
                        first // null |
                        if . then {
                            form: "13F-HR",
                            date: (.filingDate[.] // ""),
                            accession: (.accessionNumber[.] // "")
                        } else null end
                    )
                }
            else null end
        ' 2>/dev/null || echo "")

        # Simpler approach: just get basic fund info
        FUND_INFO=$(echo "$SUBMISSIONS" | jq -r --arg fund "$FUND_NAME" '{
            fund: $fund,
            entity_name: (.name // $fund),
            cik: (.cik // ""),
            recent_filings_count: ((.filings.recent.form // []) | map(select(. == "13F-HR")) | length)
        }' 2>/dev/null || echo "")

        if [ -n "$FUND_INFO" ] && [ "$FUND_INFO" != "null" ]; then
            if [ "$FIRST" = true ]; then
                FIRST=false
            else
                echo ","
            fi
            echo "$FUND_INFO"
        fi
    fi

    sleep 0.5  # Be polite to SEC servers
done

echo '],'

# --- Dataroma superinvestors ---
echo '"dataroma": ['
FIRST=true

for INVESTOR in "${!DATAROMA_FUNDS[@]}"; do
    FUND_CODE="${DATAROMA_FUNDS[$INVESTOR]}"

    PAGE=$(curl -s --max-time 15 \
        -H "User-Agent: Mozilla/5.0" \
        "https://www.dataroma.com/m/holdings.php?m=${FUND_CODE}" 2>/dev/null || echo "")

    if [ -n "$PAGE" ]; then
        # Extract top holdings from HTML table
        HOLDINGS=$(echo "$PAGE" | python3 -c "
import sys, re, json

html = sys.stdin.read()
# Find stock entries in the holdings table
rows = re.findall(r'<td[^>]*>.*?<a[^>]*>([^<]+)</a>.*?</td>.*?<td[^>]*>([^<]*)</td>.*?<td[^>]*>([^<]*%)</td>', html, re.DOTALL)

results = []
for stock, name, pct in rows[:10]:
    results.append({'stock': stock.strip(), 'name': name.strip(), 'portfolio_pct': pct.strip()})

print(json.dumps({'investor': '$INVESTOR', 'fund_code': '$FUND_CODE', 'top_holdings': results}))
" 2>/dev/null || echo "")

        if [ -n "$HOLDINGS" ] && [ "$HOLDINGS" != "" ]; then
            if [ "$FIRST" = true ]; then
                FIRST=false
            else
                echo ","
            fi
            echo "$HOLDINGS"
        fi
    fi

    sleep 0.5
done

echo ']'

echo '},'
echo "\"fetched_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\""
echo '}'
