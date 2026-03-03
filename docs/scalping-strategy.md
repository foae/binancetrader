# Scalping Strategy & Drawdown Management

How to handle underwater positions in a buy-low/sell-high scalping strategy, with data-backed volatility analysis and tiered drawdown response.

## BTC Daily Volatility — How Much Room Do We Have?

Data as of February 2026, BTC ~$59-68k:

| Metric | Value |
|--------|-------|
| Average Daily Range (9-day) | $2,999 / 4.57% |
| Average Daily Range (14-day) | $3,118 / 4.75% |
| Average Daily Range (50-day) | $3,455 / 5.27% |
| Average Daily Range (20-day) | $4,521 / 6.89% |
| 30-day volatility (std dev) | 2.63% |
| 2025 avg intraday swing | ~$4,800 / ~7-8% |

With a 0.3-0.5% take-profit target, we're looking for moves that are 1/10th to 1/20th of the typical daily range. There's plenty of room on any given day — the daily range alone could theoretically contain 10-20+ complete scalp cycles.

The problem isn't the size of the move — it's the direction.

## The Risk: Directional Trend Against You

Example: buy at $68,000, place sell at $68,204 (+0.3%). Market drops to $65,000. The sell order is stranded $3,200 above market. Capital is locked.

Two natural instincts, each with trade-offs:

### Close at End of Day (Stop-Loss / Reset)

- **Pro:** frees capital immediately, gets back in at the new price level
- **Pro:** capital efficiency — the freed amount can start cycling again at $65k
- **Con:** crystallizes a loss (potentially 2-5%)
- **Con:** creates a taxable event in most jurisdictions
- **Con:** if BTC bounces back next day, you sold the bottom

### Park Inventory, Cancel Sell, Continue Later

- **Pro:** no realized loss, BTC tends to recover over weeks/months
- **Pro:** aligns with an existing passive investment strategy
- **Con:** capital is locked and can't be used for scalping
- **Con:** if parking happens too often, all capital ends up parked and the bot becomes a HODLer with extra steps

## Recommended Approach: Tiered Drawdown Response

Neither pure stop-loss nor pure parking is optimal. A tiered response based on drawdown severity balances capital efficiency with loss avoidance.

### Tier 1 — Position < 0.5% Underwater

**Action: do nothing.** Keep the sell order active.

This is noise. With a 4-7% daily range, a 0.5% dip recovers frequently within the same hour. No intervention needed.

### Tier 2 — Position 0.5-2% Underwater (Sell Expired)

**Action: re-anchor the sell order.**

Cancel the old sell. Place a new sell at *current market price + TAKE_PROFIT* instead of *original entry + TAKE_PROFIT*. This accepts a partial loss on the entry but gets capital cycling again.

Example: bought at $68,000, market now at $67,500 (-0.74%). Re-anchor sell at $67,500 × 1.003 = $67,703. Realize a ~$297 loss on the $68,000 entry, but recover capital within one more cycle.

### Tier 3 — Position > 2% Underwater

**Action: park the position.**

At this point the move is a genuine trend, not intraday noise. Cancel the sell, mark the position as "parked" (long-term hold), and free up fresh capital for continued scalping at the new price level.

The parked position becomes a passive investment — effectively adding to a long-term BTC bag at a temporarily unfavorable price.

### Why This Beats Pure Strategies

**vs. always stop-loss:** Avoids selling at the worst moments. A 3% drop that recovers in two days would be a realized loss for nothing. Parking lets time heal it.

**vs. always park:** Doesn't let moderate drawdowns (0.5-2%) paralyze capital. Re-anchoring gets back in the game quickly with a small realized loss that's earned back in 2-3 cycles.

**vs. DCA (averaging down):** DCA doubles exposure to a move already going against you. If the market keeps dropping, you're 2x underwater. DCA makes sense for long-term investing. For scalping: cut, re-anchor, or park — don't add.

## Capital Budget

The tiered approach requires tracking available vs deployed capital. Without a budget, a sustained downtrend parks everything and the bot has nothing left to trade with.

- Define a **max active capital** budget (e.g., $100, $500)
- Each active position consumes from this budget
- Parked positions are removed from the budget — they're now "investment"
- If all budget is consumed (everything parked or active), the bot stops opening new positions

### Proposed Config

```env
# Drawdown thresholds
STALE_REANCHOR_THRESHOLD=0.005   # 0.5% — re-anchor sell at current market + TP
PARK_THRESHOLD=0.02              # 2% — park position, stop trading this slot
MAX_ACTIVE_CAPITAL_USDT=100      # Total budget for active scalping
```

## Volatility and Time of Day

BTC's daily range isn't uniformly distributed. Volatility clusters around:

- **US market open** (13:30-15:00 UTC) — highest volume
- **Asian session** (00:00-03:00 UTC) — second highest
- **US/Europe overlap** (13:00-17:00 UTC)

During low-volatility hours (late US night, ~05:00-08:00 UTC), the 0.3-0.5% moves needed for scalping are less frequent and more likely to be one-directional. Trading windows could be added as a future optimization.

## Summary

With 4.5-7% daily range and 0.3-0.5% take-profit targets, there is significant room for scalping on most days. The risk is multi-day directional moves (e.g., BTC drops 10% over a week). The tiered approach handles this:

1. **Small dips** — wait (self-correct within the daily range)
2. **Medium drawdown** — re-anchor sell (accept small loss, keep cycling)
3. **Large drawdown** — park as investment (acceptable if already holding BTC long-term)
4. **Capital budget** — prevents over-commitment and ensures trading can continue

## Sources

- [Barchart BTC/USD Technical Analysis (ADR/ADRP)](https://www.barchart.com/crypto/quotes/%5EBTCUSD/technical-analysis)
- [Bitbo Bitcoin Volatility Index](https://bitbo.io/volatility/)
- [Bitcoin Statistics 2025 — CoinLaw](https://coinlaw.io/bitcoin-statistics/)
- [BlackRock/iShares Bitcoin Volatility Guide](https://www.ishares.com/us/insights/bitcoin-volatility-trends)
- [Fidelity Digital Assets — Bitcoin Volatility](https://www.fidelitydigitalassets.com/research-and-insights/closer-look-bitcoins-volatility)
- [GainsCrypt — Stop-Loss/DCA Hybrid](https://www.gainscrypt.com/risk-management-styles/stop-loss-%2F-dca-hybrid)
- [Cornix — Trailing Stop-Loss Guide](https://cornix.io/stop-loss-trailing-stop-loss-the-ultimate-guide-for-automated-crypto-trading/)
