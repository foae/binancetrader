#!/usr/bin/env bash
set -uo pipefail

for cmd in curl grep; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "ERROR: ${cmd} is required but not found in PATH." >&2
    exit 1
  fi
done

if ! echo "test" | grep -P "test" &>/dev/null; then
  echo "ERROR: grep with PCRE support (-P) is required." >&2
  exit 1
fi

BASE_URL="https://archive.pmxt.dev"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ALL_ARCHIVES=("Kalshi" "Opinion" "Polymarket")

usage() {
  echo "Usage: $0 --all | --kalshi | --opinion | --polymarket [...]"
  exit 1
}

if [[ $# -eq 0 ]]; then
  usage
fi

# Parse flags: --all, --kalshi, --opinion, --polymarket (case-insensitive).
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

any_failed=0

sync_archive() {
  local archive="$1"
  local index_url="${BASE_URL}/${archive}"
  local data_dir="${SCRIPT_DIR}/data/${archive}"

  mkdir -p "$data_dir"

  echo "=== ${archive} ==="

  # Crawl pages until one returns zero parquet links.
  # The pagination text is client-side rendered (Next.js), so we can't parse
  # "Page X of Y". Instead we just increment and stop when a page is empty.
  local all_files=()
  local page=1
  while true; do
    echo "  Scanning page ${page}..."
    local html
    html=$(curl -sS --fail "${index_url}?page=${page}" 2>/dev/null) || {
      echo "  WARNING: Failed to fetch page ${page}. Stopping pagination." >&2
      break
    }

    local page_files=()
    while IFS= read -r href; do
      page_files+=("$href")
    done < <(echo "$html" | grep -oP 'href="\K/dumps/[^"]+\.parquet')

    if [[ ${#page_files[@]} -eq 0 ]]; then
      break
    fi

    all_files+=("${page_files[@]}")
    ((page++))
  done

  echo "  ${#all_files[@]} file(s) listed."

  local downloaded=0 skipped=0 failed=0

  for href in "${all_files[@]}"; do
    local filename
    filename=$(basename "$href")
    local dest="${data_dir}/${filename}"

    if [[ -f "$dest" ]]; then
      ((skipped++))
      continue
    fi

    local url="${BASE_URL}${href}"
    echo "  Downloading ${filename}..."
    if curl -sSf -o "$dest" "$url"; then
      ((downloaded++))
    else
      echo "    FAILED: ${filename}" >&2
      rm -f "$dest"
      ((failed++))
    fi
  done

  echo "  Done. Downloaded: ${downloaded}, Skipped: ${skipped}, Failed: ${failed}"
  echo ""

  [[ "$failed" -eq 0 ]] && return 0 || return 1
}

for archive in "${SELECTED[@]}"; do
  sync_archive "$archive" || any_failed=1
done

if [[ "$any_failed" -ne 0 ]]; then
  echo "Some archives had errors. Re-run to retry."
  exit 1
fi

echo "All archives synced successfully."
