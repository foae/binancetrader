#!/usr/bin/env bash
set -uo pipefail

if ! command -v duckdb &>/dev/null; then
  echo "ERROR: duckdb is required but not found in PATH." >&2
  echo "Install: brew install duckdb" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="${SCRIPT_DIR}/data"
DB_DIR="${SCRIPT_DIR}/db"
ALL_ARCHIVES=("Kalshi" "Opinion" "Polymarket")

usage() {
  echo "Usage: $0 --all | --kalshi | --opinion | --polymarket [...]"
  exit 1
}

if [[ $# -eq 0 ]]; then
  usage
fi

SELECTED=()
for arg in "$@"; do
  case "${arg,,}" in
    --all)         SELECTED=("${ALL_ARCHIVES[@]}"); break ;;
    --kalshi)      SELECTED+=("Kalshi") ;;
    --opinion)     SELECTED+=("Opinion") ;;
    --polymarket)  SELECTED+=("Polymarket") ;;
    -h|--help)     usage ;;
    *)             echo "Unknown flag: $arg"; usage ;;
  esac
done

mkdir -p "$DB_DIR"

any_failed=0

import_archive() {
  local archive="$1"
  local parquet_dir="${DATA_DIR}/${archive}"
  local db_file="${DB_DIR}/${archive}.duckdb"

  echo "=== ${archive} ==="

  if [[ ! -d "$parquet_dir" ]]; then
    echo "  No data directory. Run sync.sh first. Skipping."
    return 0
  fi

  local parquet_files=()
  while IFS= read -r -d '' f; do
    parquet_files+=("$f")
  done < <(find "$parquet_dir" -maxdepth 1 -name '*.parquet' -print0 | sort -z)

  if [[ ${#parquet_files[@]} -eq 0 ]]; then
    echo "  No parquet files found. Skipping."
    return 0
  fi

  # Ensure the imported-files tracking table exists.
  duckdb "$db_file" -c "
    CREATE TABLE IF NOT EXISTS _imported_files (
      filename VARCHAR PRIMARY KEY,
      imported_at TIMESTAMP DEFAULT current_timestamp
    );
  " || {
    echo "  ERROR: Failed to initialize database. Skipping." >&2
    return 1
  }

  # Get the list of already-imported filenames.
  local imported
  imported=$(duckdb "$db_file" -noheader -csv -c "SELECT filename FROM _imported_files;") || {
    echo "  ERROR: Failed to read imported files list. Skipping." >&2
    return 1
  }

  # Build a lookup set (associative array) for fast membership checks.
  declare -A imported_set
  while IFS= read -r line; do
    [[ -n "$line" ]] && imported_set["$line"]=1
  done <<< "$imported"

  local total=${#parquet_files[@]}
  local imported_count=0 skipped=0 failed=0

  for filepath in "${parquet_files[@]}"; do
    local filename
    filename=$(basename "$filepath")

    if [[ -n "${imported_set[$filename]+_}" ]]; then
      ((skipped++))
      continue
    fi

    echo "  Importing ${filename} ..."
    if duckdb "$db_file" -c "
      -- Auto-create the orderbook table from the first file if it doesn't exist.
      CREATE TABLE IF NOT EXISTS orderbook AS
        SELECT * FROM read_parquet('${filepath}') LIMIT 0;

      INSERT INTO orderbook
        SELECT * FROM read_parquet('${filepath}');

      INSERT INTO _imported_files (filename) VALUES ('${filename}');
    "; then
      ((imported_count++))
    else
      echo "    FAILED: ${filename}" >&2
      ((failed++))
    fi
  done

  echo "  Done. Imported: ${imported_count}, Skipped: ${skipped}, Failed: ${failed} (total on disk: ${total})"
  echo ""

  [[ "$failed" -eq 0 ]] && return 0 || return 1
}

for archive in "${SELECTED[@]}"; do
  import_archive "$archive" || any_failed=1
done

if [[ "$any_failed" -ne 0 ]]; then
  echo "Some archives had errors. Re-run to retry."
  exit 1
fi

echo "All archives imported successfully."
