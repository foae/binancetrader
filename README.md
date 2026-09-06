# binancetrader

A Go service for Binance spot trading, with separate utilities for downloading public prediction-market archives. Version **1.0.0** is the initial packaged release, not a certification of trading safety or profitability.

## What it does

For each configured pair, the service places a GTC limit buy below market, then a limit sell above the average execution cost. A one-minute loop reconciles orders and cancels expired orders. Partial fills remain tracked after cancellation. Durable submission intents prevent automatic resubmission after an uncertain exchange response; positions are rebuilt from cumulative order snapshots.

- `cmd/binancetrader/`: configuration, startup, HTTP health and metrics server.
- `exchange/`: Binance SDK wrapper supporting live, demo, and testnet endpoints.
- `service/`: strategy, durable order reconciliation, and regression tests.
- `storage/`: Redis-compatible JSON persistence.
- `prediction-markets-archive/`: independent public archive download and DuckDB import scripts; see its [README](prediction-markets-archive/README.md).
- `docs/`: [fee analysis](docs/fee-analysis.md) and [strategy research](docs/scalping-strategy.md). Research is not a description of fully implemented features or current exchange fees.

## Safety and limitations

**Start with demo keys and `DRY_RUN=true`. Never use withdrawal-enabled keys.** Restrict key permissions and network access. You are responsible for losses, fees, exchange limits, and legal eligibility.

Dry-run logs repeated buy intentions using current public market data. It does not simulate fills, write trading state, or call order endpoints. Startup still authenticates the configured API key and requires Redis. It is not a backtester.

Run exactly **one writer per Binance account and managed symbol set**, using one dedicated durable database. No replicas, manual orders, or other bots on those symbols. Open-order checks are protective, not an atomic account lock. Start a new ledger only after reconciling/closing pre-existing managed orders and inventory on Binance. Do not delete ledger records or restore an older database while the exchange has newer orders.

The bot has no stop-loss, portfolio risk limits, fee-adjusted inventory, or automatic recovery from dust/insufficient balance. Base-asset commissions can make its gross sell quantity exceed available inventory. Such rejections leave a blocked intent requiring manual reconciliation. Ledger replay grows with history. `/ready` checks connectivity, not profitability or whether a symbol is paused. Do not treat this release as production-ready unattended trading.

## Prerequisites

- Go **1.26 or newer**, a C compiler for race-detector checks, Make, and Git.
- Redis 8 (the supplied Compose configuration enables AOF with `appendfsync always`), or a compatible store with equivalent durability configured by the operator.
- Binance demo/testnet credentials for initial use, and outbound HTTPS access to the selected Binance API.
- Optional: Docker Engine with Compose v2 for container setup.
- Release maintainers additionally need authenticated `gh`, Python 3, Bash, and repository push/release permissions.

## Local setup

```sh
git clone https://github.com/foae/binancetrader.git
cd binancetrader
cp .env.example .env
# Edit .env: use your own demo credentials; keep DRY_RUN=true.
docker compose up -d redis
go mod download
make test
make run
```

Go modules are the dependency source; vendoring is not tracked. `.env`, `.private/`, logs, and generated archive databases are ignored. Never commit real credentials or database snapshots. Existing local Redis installations can be used instead of the Compose Redis service; configure their persistence before real trading.

In another terminal:

```sh
curl --fail http://127.0.0.1:8123/health
curl --fail http://127.0.0.1:8123/ready
curl --fail http://127.0.0.1:8123/metrics
```

## Configuration

The process loads `.env`; already-exported environment variables take precedence. See [.env.example](.env.example).

| Variable | Meaning |
| --- | --- |
| `BINANCE_API_KEY`, `BINANCE_API_SECRET` | Credentials for the selected environment; required even for dry-run startup |
| `BINANCE_MODE` | `demo` in the example; `live`, `demo`, or `testnet` supported; application default is `live` |
| `REDIS_URL` | Dedicated durable database, e.g. `redis://localhost:6379/0` |
| `ENABLED_PAIRS` | Comma-separated `BASE/QUOTE`, e.g. `BTC/USDT,ETH/USDT` |
| `DRY_RUN` | Defaults to `true`; `false` permits actual orders in the selected environment |
| `BUY_OFFSET` | Fraction below market for a buy; example `0.001` (0.1%) |
| `BUY_QUANTITY_USDT` | Per-order budget in the pair's **quote currency**, despite the legacy name; example `5` |
| `TAKE_PROFIT` | Fraction above average execution cost; example `0.01` (1%), before fees |
| `ORDER_EXPIRY` | Age at which the next tick cancels an open order; example `1h` |
| `HTTP_LISTEN_ADDRESS` | Example binds `127.0.0.1:8123`; do not expose health/metrics publicly |
| `ENV_MODE` | `dev` for debug logging, `prod` for info logging |
| `SERVICE_NAME` | Service log label |
| `LOG_FILE` | File path, or `-` for stdout only |

Orders below exchange minimum quantity/notional are refused rather than increasing the configured budget. Adjust the budget deliberately. Other exchange rules may reject a submission; the durable intent then blocks automatic retries until the outcome is reconciled.

## Containers

After creating and editing `.env`:

```sh
APP_VERSION="$(cat VERSION)" GIT_COMMIT="$(git rev-parse HEAD)" docker compose up --build -d
docker compose logs -f app
docker compose down
```

Compose binds Redis and HTTP ports to loopback, waits for Redis health, and logs the non-root application to stdout. `down` preserves its named Redis volume; **do not use `down -v` on trading state**. `make build-docker` builds with version/revision labels; `make run-docker` uses host networking on Linux and the Redis URL in `.env`.

## State and recovery

Real trading binds database metadata to schema version, API-key fingerprint, and Binance environment. Changing keys or modes refuses database reuse. Dry-run never modifies this metadata. Keep independent databases for independent accounts/environments, but never run simultaneous writers against the same account/symbols.

Pre-1.0.0 order/position state is intentionally rejected: it may contain synthetic IDs or incomplete cancellation fills. Stop the old process, back up its database privately, reconcile **all** managed inventory and orders against Binance, and close/settle them before starting with a fresh dedicated database. There is no automatic legacy importer. Do not merely delete the metadata or select an empty database while managed inventory remains.

An unresolved `intents:{symbol}` record means submission may or may not have reached Binance. The service queries its client order ID on subsequent ticks. If no authoritative result is available, it stays paused. Verify the client ID and exchange history manually; only clear an intent after proving no order was accepted, or completing an explicit ledger reconciliation. Do not blindly retry or restore an older snapshot. Protect backups as sensitive account data.

## Development and releases

```sh
make test   # formatting check, vet, race-detector tests
make build  # compile packages
```

CI checks formatting, vet, race tests, compilation, shell syntax, and the Docker build on the exact triggering commit.

Every shipped change gets a new stable SemVer release: patch for fixes/docs, minor for compatible features, major for incompatible changes. `VERSION` is the package version; Go module dependency versions are independent. Never move a published tag.

Commit the changes on `main`, prepare accurate notes outside the repository or in `.private/`, then run:

```sh
make release RELEASE_VERSION=1.0.1 RELEASE_NAME="Describe the changes" RELEASE_NOTES=.private/release-notes.md
```

The tool bumps `VERSION` if needed, commits that bump, pushes `main`, waits for successful CI on the exact commit, verifies remote `main` has not advanced, creates/pushes an annotated tag, and publishes a named stable non-draft GitHub release. Existing tags/releases are refused. If publication fails after pushing the tag, inspect it and finish the release manually with `gh`; never delete/repoint it silently.
