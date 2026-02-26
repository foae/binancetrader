# binancetrader

Automated trading bot for Binance. Single service orchestrates all configured trading pairs with a 1-minute main loop, debounced by async events. Trading strategies TBD.

## Build & Development

```bash
make run              # Run locally with race detector
make test             # fmt + vet + race-detector tests
make build-docker     # Build Docker image
make run-docker       # Build and run in Docker (host networking)
```

## Configuration

Copy `.env.example` to `.env` and fill in your values. The `.env` file is gitignored and must never be committed.
