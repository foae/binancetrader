# CLAUDE.md

This file provides guidance when working with code in this repository.

## Project Overview

binancetrader is an automated trading bot for Binance. A single service orchestrates all configured trading pairs with a 1-minute main loop, debounced by async events (websocket, webhooks). Trading strategies and order placement are TBD.

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
                               Parses ENABLED_PAIRS (BTC/USDC → BTCUSDC), creates
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
                               - TODO: strategy logic, order placement

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
- `ENABLED_PAIRS`: Comma-separated trading pairs, format `BASE/QUOTE` (e.g., `BTC/USDC,ETH/USDC`)
- `DRY_RUN`: `true` (default) disables real order placement

## Testing Patterns

Tests use table-driven style. Service tests should use interface mocks for the exchange client and storage layer.
