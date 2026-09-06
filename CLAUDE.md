# Repository development guide

## Purpose and layout

A single Go service runs the Binance spot buy-low/sell-high strategy across configured pairs. `cmd/binancetrader` loads configuration and serves `/health`, `/ready`, and `/metrics`; `exchange` wraps the Binance SDK; `service` manages reconciliation and order placement; `storage` persists JSON in Redis. `prediction-markets-archive` contains independent public-data utilities, not trading inputs.

## Commands

```sh
make run           # local application with race detector; requires configured .env and Redis
make test          # formatting check, vet, race-detector regression tests
make build         # compile packages
make build-docker  # non-root image with VERSION and Git revision labels
make run-docker    # Linux host networking, configured .env and external Redis
```

Use Go modules, not a committed vendor directory. Update `go.mod` and `go.sum` together. Go 1.26 is the minimum toolchain; CI reads it from `go.mod`.

## Trading invariants

- One process owns each account/managed symbol set and dedicated durable Redis database. No replicas or external ledger mutations.
- Persist `intents:{symbol}` before submitting to Binance with a unique client order ID. Recover by that ID; never automatically resubmit an uncertain order.
- `orders:{symbol}:{orderID}` stores cumulative executed base and quote amounts. Replay snapshots in order to derive average-cost inventory. `positions:{symbol}` is only a derived cache.
- Any active order blocks further placement on that symbol, including partially filled buys. Unmanaged exchange orders block buys and sells.
- Reconcile expiry cancellation against final exchange state before deriving inventory. Partial sells must preserve unsold inventory.
- Malformed state, storage errors, unexpected statuses, and uncertain exchange outcomes pause trading. Never turn an error into an empty order set.
- Startup rejects unversioned legacy trading state and database/account/environment mismatches. Never bypass these guards to make startup succeed. See README recovery instructions.
- Dry-run only previews buy intentions: no order endpoints, synthetic orders, or trading-state writes. Startup still requires authenticated API access and Redis.
- Use decimal arithmetic; floor price and quantity to exchange increments. Never increase the configured budget to satisfy a minimum.
- Fees are not deducted from tracked inventory. Base-asset commissions, dust, and insufficient balances can require manual reconciliation; do not claim production trading safety.

The exchange SDK exposes `Spot()` for metadata and client-ID recovery. Websocket transport is not implemented. Strategy research in `docs/` is not necessarily implemented behavior.

## Verification

Service regression tests exercise the real SDK against a loopback HTTP fixture, never live trading credentials. Keep tests deterministic and isolated. Reproduce reconciliation bugs before fixing them; validate failure/recovery transitions and inventory rather than internal wiring. `make test` must pass, as must the Docker build and CI on the intended release commit.

Keep `.env`, `.private/`, database dumps, local logs, and generated archive data out of Git and Docker contexts. Never add credentials, personal infrastructure, or authorship/co-author credits for development tools.

## Required release workflow

Every shipped change must be versioned, tagged, and named in a GitHub release. Use stable SemVer: patch for fixes/documentation, minor for compatible additions, major for breaking changes. `VERSION` is the source of truth for package releases and Makefile image labels; independently versioned dependencies retain their own versions.

1. Make and verify the change; update README/config examples when behavior changes.
2. Commit the change on `main` using the maintainer's intended public Git identity.
3. Write accurate release notes in `.private/` or outside the repository, including compatibility changes, verification, and remaining risks.
4. Run `make release RELEASE_VERSION=X.Y.Z RELEASE_NAME="Descriptive release name" RELEASE_NOTES=.private/release-notes.md`.
5. `scripts/release.sh` bumps/commits `VERSION`, pushes `main`, waits for successful CI on that exact SHA, verifies remote `main`, then creates and pushes an annotated `vX.Y.Z` tag and publishes a stable, non-draft release using `gh`.
6. Verify the published release and tag target. If a tag or release already exists, stop and investigate; never move it silently. A failure after tag push requires manual release completion, not retagging.

No release should precede successful CI on its exact commit. The workflow does not change repository visibility.
