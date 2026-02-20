---
name: finance
description: "Financial intelligence toolkit — market data, news, congressional trades, hedge fund/PIF tracking, Saudi IPO monitoring, investment bank research, sentiment analysis, portfolio/expense management. Use when user asks about markets, stocks, portfolio, spending, investments, or financial news."
---

# Finance Skill

Complete financial intelligence toolkit with shell scripts for data collection and analysis.

## Scripts

All scripts are in `workspace/skills/finance/scripts/`. Run them via the `exec` tool.

### Market Data & Monitoring

| Script | Usage | Description |
|--------|-------|-------------|
| `market-data.sh` | `bash market-data.sh AAPL 2222.SR GC=F` | Live quotes from Yahoo Finance. Pass symbols as args or omit to use watchlist.json |
| `market-monitor.sh` | `bash market-monitor.sh` | Check watchlist against alert thresholds. Outputs `NO_ALERT` if nothing significant |
| `fear-greed.sh` | `bash fear-greed.sh` | CNN Fear & Greed Index + VIX level |

### News & Research

| Script | Usage | Description |
|--------|-------|-------------|
| `news-fetch.sh` | `bash news-fetch.sh [category]` | RSS news from FT, Reuters, WSJ, CNBC, Argaam. Categories: all, saudi, us, commodities |
| `substack-feeds.sh` | `bash substack-feeds.sh` | Financial newsletter aggregator (The Diff, Kyla Scanlon, Doomberg, etc.) |
| `bank-research.sh` | `bash bank-research.sh` | Detect new investment bank reports (GS, JPM, MS, BlackRock) |

### Institutional & Political

| Script | Usage | Description |
|--------|-------|-------------|
| `congress-trades.sh` | `bash congress-trades.sh` | Congressional stock trades. Filters >$50K, key politicians. Tracks state for new-only detection |
| `hedge-funds.sh` | `bash hedge-funds.sh` | 13F filings from SEC EDGAR + Dataroma superinvestors. Includes PIF (CIK 0001767640) |

### Saudi Market

| Script | Usage | Description |
|--------|-------|-------------|
| `saudi-ipo.sh` | `bash saudi-ipo.sh` | Monitor CMA/Tadawul for new IPO announcements. Tracks state to detect new listings |

### Calendar & Events

| Script | Usage | Description |
|--------|-------|-------------|
| `economic-calendar.sh` | `bash economic-calendar.sh` | Economic events (Forex Factory, free) + earnings (Finnhub free tier, key optional) |

### Portfolio & Analysis

| Script | Usage | Description |
|--------|-------|-------------|
| `portfolio-value.sh` | `bash portfolio-value.sh` | Portfolio valuation: current prices, unrealized P&L, benchmark comparison |
| `price-history.sh` | `bash price-history.sh SYMBOL [range] [interval]` | Historical OHLCV data with moving averages (20/50/200), support/resistance. Defaults: 6mo daily |

### Expense Tracking

| Script | Usage | Description |
|--------|-------|-------------|
| `expenses-parse.sh` | `bash expenses-parse.sh <file>` | Parse bank statements (CSV). Categorizes and summarizes spending |

### Email Digest

| Script | Usage | Description |
|--------|-------|-------------|
| `email-digest.sh` | `bash email-digest.sh` | Reads `workspace/email_digest.jsonl`, outputs JSON array, truncates file. Used by morning briefing |

### Master Script

| Script | Usage | Description |
|--------|-------|-------------|
| `morning-briefing.sh` | `bash morning-briefing.sh` | Runs all data scripts, outputs combined JSON for briefing generation |

## Reference Files

All in `workspace/skills/finance/references/`:

- **`watchlist.json`** — Symbols, names, and alert thresholds for all tracked assets
- **`feeds.json`** — RSS feed URLs for news and newsletters
- **`briefing-template.md`** — Morning briefing format template

## Data Sources

| Source | Auth | Endpoint |
|--------|------|----------|
| Yahoo Finance | None | `query1.finance.yahoo.com/v8/finance/chart/{SYMBOL}` |
| CNN Fear & Greed | None | `production.dataviz.cnn.io/index/fearandgreed/graphdata/` |
| House Stock Watcher | None | `house-stock-watcher-data.s3-us-west-2.amazonaws.com/data/all_transactions.json` |
| Senate Stock Watcher | None | `senate-stock-watcher-data.s3-us-west-2.amazonaws.com/aggregate/all_transactions.json` |
| SEC EDGAR | None | `efts.sec.gov/LATEST/search-index?q=...&dateRange=...&forms=13F-HR` |
| Dataroma | None | `dataroma.com/m/holdings.php?m=...` |
| Forex Factory | None | `nfs.faireconomy.media/ff_calendar_thisweek.json` |
| Finnhub | Free key (optional) | `finnhub.io/api/v1/calendar/earnings` |
| RSS Feeds | None | Various (see feeds.json) |

## Cron Jobs (set up via Saleh)

| Job | Schedule | Script | Logic |
|-----|----------|--------|-------|
| Morning Briefing | `0 7 * * 0-5` | morning-briefing.sh | Full briefing → consult Fahad → send |
| Market Monitor | `0 */3 * * 0-5` | market-monitor.sh | Alert only if thresholds breached |
| Saudi IPO | `0 12 * * 0-4` | saudi-ipo.sh | Alert only if new IPOs |
| Congress Trades | `0 20 * * 1-5` | congress-trades.sh | Alert only if new trades |
| Institutional | `0 8 * * 1` | hedge-funds.sh | Weekly 13F check |
| Bank Reports | `0 9 * * 1,4` | bank-research.sh | Bi-weekly report check |
| Weekly Digest | `0 18 * * 5` | morning-briefing.sh | Week summary + portfolio perf |
