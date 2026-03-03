# Binance Fee Analysis & Scalping Config Guide

Analysis of Binance spot trading fees as they apply to our GTC limit order strategy, with config recommendations for profitability at various aggressiveness levels.

## Fee Structure (Regular User, 2026)

Both our buy and sell orders are GTC limit orders, so we pay **maker fees** on both sides.

| Scenario | Maker | Taker | Round-trip (buy + sell) |
|----------|-------|-------|------------------------|
| Standard | 0.100% | 0.100% | 0.200% |
| BNB discount (25% off) | 0.075% | 0.075% | 0.150% |
| USDT pair + BNB | 0.075% | ~0.071% | ~0.146% |

VIP tiers reduce fees further but require unrealistic volume for retail (VIP 1 = $1M/30d). Not considered here.

## Breakeven

`TAKE_PROFIT` must exceed the round-trip fee cost for a trade to be profitable.

| Fee mode | Round-trip cost | Breakeven `TAKE_PROFIT` | Minimum profitable `TAKE_PROFIT` |
|----------|----------------|------------------------|----------------------------------|
| Standard (no BNB) | 0.200% | 0.002 | > 0.003 |
| BNB discount | 0.150% | 0.0015 | > 0.002 |

## Config Options

### Option 1 — Conservative (current defaults)

```env
TAKE_PROFIT=0.01
BUY_OFFSET=0.001
ORDER_EXPIRY=1h
BUY_QUANTITY_USDT=5
```

- Net profit per trade: ~0.8% (BNB) / ~0.7% (no BNB)
- Trades infrequently — 1% swings are uncommon in short windows
- Low risk, low capital requirement
- Good for: validation, low-touch operation

### Option 2 — Moderate scalping

```env
TAKE_PROFIT=0.005
BUY_OFFSET=0.0005
ORDER_EXPIRY=30m
BUY_QUANTITY_USDT=10
```

- Net profit per trade: ~0.35% (BNB) / ~0.25% (no BNB)
- Higher frequency — catches smaller price oscillations
- Tighter buy offset means fills happen faster
- Good for: steady returns, reasonable risk/reward balance

### Option 3 — Aggressive scalping

```env
TAKE_PROFIT=0.003
BUY_OFFSET=0.0003
ORDER_EXPIRY=15m
BUY_QUANTITY_USDT=20
```

- Net profit per trade: ~0.15% (BNB) / ~0.1% (no BNB)
- High frequency, thin margins
- **Requires BNB discount** — without it, slippage and timing risk can eat the ~0.1% profit
- Good for: high-volume, active monitoring

### Option 4 — Multi-pair volume play

```env
ENABLED_PAIRS=BTC/USDT,ETH/USDT,SOL/USDT,BNB/USDT
TAKE_PROFIT=0.004
BUY_OFFSET=0.0004
ORDER_EXPIRY=20m
BUY_QUANTITY_USDT=10
```

- Net profit per trade: ~0.25% (BNB) / ~0.15% (no BNB)
- Runs several pairs in parallel to increase overall trade frequency
- More capital deployed across pairs
- Good for: maximizing cycles per day without going ultra-aggressive on margins

## Recommendations

1. **Enable BNB fee payment.** Hold a small BNB balance in your spot wallet and toggle "Use BNB to pay fees" in Binance account settings. 25% fee reduction for free.

2. **Don't go below `TAKE_PROFIT=0.003` without BNB discount.** At 0.2% round-trip, anything under 0.3% leaves almost no margin for timing variance.

3. **Keep `BUY_OFFSET` at roughly 10% of `TAKE_PROFIT`.** Too wide and buys rarely fill. Too tight and you're buying at market.

4. **Shorten `ORDER_EXPIRY` for aggressive configs.** Stale orders in a moving market are a liability — cancel and re-enter at current levels.

5. **Increase `BUY_QUANTITY_USDT` for thinner margins.** Thin margins need volume to justify operational overhead.

6. **Start with Option 2**, verify reliable cycling, then tighten toward Option 3 or expand to Option 4.

## Sources

- [Binance Spot Trading Fee Rate](https://www.binance.com/en/fee/spotMaker)
- [Binance Fee Schedule](https://www.binance.com/en/fee)
- [How to Use BNB to Pay for Fees and Earn 25% Discount](https://www.binance.com/en/support/faq/how-to-use-bnb-to-pay-for-fees-and-earn-25-discount-115000583311)
- [Binance Trading Fees 2026 Explained — TradersUnion](https://tradersunion.com/brokers/crypto/view/binance/fees/)
- [Binance Fees Breakdown 2026 — BitDegree](https://www.bitdegree.org/crypto/tutorials/binance-fees)
