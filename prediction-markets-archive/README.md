# pmxt Data Archive Sync

Downloads historical prediction market data (parquet files) from the [pmxt Data Archive](https://archive.pmxt.dev/) and imports them into per-market DuckDB databases.

## Source

The archive hosts hourly orderbook snapshots for three prediction market platforms:

| Archive | URL |
|---------|-----|
| Kalshi | https://archive.pmxt.dev/Kalshi |
| Opinion | https://archive.pmxt.dev/Opinion |
| Polymarket | https://archive.pmxt.dev/Polymarket |

Each archive is paginated (50 files per page). Files are updated daily.

## Directory Structure

```
prediction-markets-archive/
├── README.md
├── sync.sh           # download parquet files
├── import.sh         # import parquet files into DuckDB
├── data/
│   ├── Kalshi/       # raw parquet files
│   ├── Opinion/
│   └── Polymarket/
└── db/
    ├── Kalshi.duckdb
    ├── Opinion.duckdb
    └── Polymarket.duckdb
```

## Usage

Both scripts require a market flag. Use `--all` for everything, or pick specific markets:

```bash
./sync.sh --all                      # download all markets
./sync.sh --polymarket               # download Polymarket only
./sync.sh --kalshi --opinion         # download Kalshi and Opinion

./import.sh --all                    # import all markets
./import.sh --polymarket             # import Polymarket only
```

`sync.sh` is idempotent — skips files already on disk.
`import.sh` is idempotent — tracks imported files in a `_imported_files` metadata table per database.

### Cron example (daily at 14:00 UTC)

```
0 14 * * * cd /path/to/prediction-markets-archive && ./sync.sh --all >> /tmp/pmxt-sync.log 2>&1 && ./import.sh --all >> /tmp/pmxt-import.log 2>&1
```

### Querying

```bash
duckdb db/Polymarket.duckdb -c "SELECT COUNT(*) FROM orderbook"
duckdb db/Polymarket.duckdb -c "SELECT * FROM _imported_files"
```

## Requirements

- `bash`, `curl`, `grep` (with `-P` / PCRE support)
- `duckdb` (for import.sh)
