# CLAUDE.md

This file provides guidance when working with code in this repository.

## Project Overview

binancetrader is an automated trading bot for Binance. A single service orchestrates all configured trading pairs with a 1-minute main loop, debounced by async events (websocket, webhooks). Strategy: buy-low/sell-high — place GTC limit buys below market, sell at take-profit. Orders have manual expiry management (Binance spot has no GTT).

## Build & Development Commands

```bash
make run              # Run locally with race detector
make test             # fmt + vet + race-detector tests
make build-docker     # Build Docker image
make run-docker       # Build and run in Docker (host networking)
```

Run a single test:
```bash
go test -race -run TestName ./service/...
```


## Architecture

```
cmd/binancetrader/main.go  →  Entry point, config loading, HTTP server (Chi on :8123)
                               Routes: /health, /ready, /metrics
                               Parses ENABLED_PAIRS (BTC/USDT → BTCUSDT), creates
                               Binance client, single Service for all pairs

exchange/binance.go        →  Binance spot client (wraps github.com/adshao/go-binance/v2)
                               - Public: Ping, ServerTime, TickerPrice, Klines
                               - Authenticated: Account, CreateOrder, GetOrder,
                                 CancelOrder, ListOpenOrders
                               - Spot() exposes underlying go-binance client
                               - WithTestnet() option for testnet keys
                               - WebSocket intentionally excluded (go-binance#800
                                 data race); will build our own WS layer later

service/service.go         →  Single orchestrator for all configured pairs
                               - 1-minute ticker triggers main loop
                               - triggerCh (buffered 16) for async events
                               - Async events reset ticker (debounce)
                               - Inject() method for external event sources
                               - tick() iterates all pairs, processPair() per pair
                               - exchangeClient interface: Ping, TickerPrice,
                                 CreateOrder, GetOrder, CancelOrder, ListOpenOrders, Spot
                               - storageClient interface: Close, Set, Get, Delete, List

service/types.go           →  Domain types
                               - PairConfig{Symbol, Base, Quote}
                               - OrderRecord — local mirror of Binance order
                                 Key: orders:{symbol}:{orderID}
                               - Position — inventory per symbol (one max)
                                 Key: positions:{symbol}
                               - SymbolFilters — cached lot/price filter params
                               - All financial fields use shopspring/decimal

service/strategy.go        →  Buy-low/sell-high strategy
                               - processPair(): fetch price → syncOrders →
                                 checkExpiredOrders → evaluate state → place order
                               - syncOrders(): reconcile DB vs Binance open orders,
                                 handle fills (create/delete positions)
                               - placeBuyOrder(): market × (1-BUY_OFFSET), GTC limit
                               - placeSellOrder(): entry × (1+TAKE_PROFIT), GTC limit
                               - checkExpiredOrders(): cancel GTC > ORDER_EXPIRY
                               - Symbol filters cached per symbol (via Spot() escape hatch),
                                 includes MinNotional validation
                               - Immediate fills handled inline (no wait for next tick)
                               - DRY_RUN: logs intent, saves synthetic order records
                               - Rounding: roundToTickSize, roundToStepSize (floor)

storage/client.go          →  Generic Redis/DragonFly JSON store
                               - Key scheme: {table}:{id}
                               - CRUD: Set, Get, Delete, Exists, List
                               - Reusable for any record type
```

## Configuration

Environment variables loaded from `.env` (see `.env.example`). Key vars:
- `ENV_MODE`: `dev` (DEBUG logs) or `prod` (INFO logs)
- `REDIS_URL`: DragonFly/Redis connection string
- `BINANCE_API_KEY` / `BINANCE_API_SECRET`: Binance API credentials
- `BINANCE_MODE`: `live`, `demo`, or `testnet`
- `ENABLED_PAIRS`: Comma-separated trading pairs, format `BASE/QUOTE` (e.g., `BTC/USDT,ETH/USDT`)
- `DRY_RUN`: `true` (default) disables real order placement
- `BUY_OFFSET`: Decimal, how far below market to buy (default `0.001` = 0.1%)
- `BUY_QUANTITY_USDT`: Decimal, USDT amount per buy order (default `5`)
- `TAKE_PROFIT`: Decimal, sell target above entry (default `0.01` = 1%)
- `ORDER_EXPIRY`: Go duration, cancel stale GTC orders (default `1h`)

## Docs

- [docs/fee-analysis.md](docs/fee-analysis.md) — Binance fee breakdown, breakeven math, config presets for scalping profitability
- [docs/scalping-strategy.md](docs/scalping-strategy.md) — Volatility analysis, tiered drawdown response (re-anchor / park), capital budgeting

## Testing Patterns

Tests use table-driven style. Service tests should use interface mocks for the exchange client and storage layer.
