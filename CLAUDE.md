# CLAUDE.md

This file provides guidance when working with code in this repository.

## Project Overview

binancetrader is an automated trading bot for Binance. Currently a skeleton — the service-per-asset goroutine architecture is in place (ported from a production Polymarket bot), but trading strategies, exchange client, and storage schema are TBD.

Each enabled asset runs as an independent service instance sharing a single storage backend. The architecture supports multiple concurrent strategies across different trading pairs.

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
                               Parses ENABLED_ASSETS, creates one Service per asset

service/service.go         →  Skeleton service (one instance per asset)
                               - Two goroutines per asset: discoveryLoop (30s ticker)
                                 finds windows, tradingLoop processes them
                               - Window alignment to :00/:15/:30/:45 UTC boundaries
                               - Asset-scoped structured logging
                               - sleepUntil helper for checkpoint timing
                               - TODO: exchange client interface, strategy logic, order placement

indicators/                →  taapi.io bulk API client (optional, enabled via TAAPI_SECRET)
                               - Exchange-agnostic technical indicator fetching
                               - 10 indicators across 3 intervals (1m, 5m, 15m)
                               - Partial failure tolerant

storage/client.go          →  Generic Redis/DragonFly JSON store
                               - Key scheme: {table}:{id}
                               - CRUD: Set, Get, Delete, Exists, List
                               - Reusable for any record type
```

## Configuration

Environment variables loaded from `.env` (see `.env.example`). Key vars:
- `ENV_MODE`: `dev` (DEBUG logs) or `prod` (INFO logs)
- `REDIS_URL`: DragonFly/Redis connection string

## Testing Patterns

Tests use table-driven style. Indicator client tests use `net/http/httptest` to mock the taapi.io bulk endpoint. Service tests should use interface mocks for the exchange client and storage layer.
