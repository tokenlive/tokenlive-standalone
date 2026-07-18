#!/usr/bin/env bash
# Start tokenlive all-in-one (bundled admin config + forced SQLite).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

CONF="${CONF:-config/all-in-one.example.yml}"
DATA_DIR="${DATA_DIR:-$ROOT/data}"
ADMIN_WORKDIR="${ADMIN_WORKDIR:-$ROOT/configs/admin}"

mkdir -p "$DATA_DIR"

echo "conf=$CONF data-dir=$DATA_DIR admin-workdir=$ADMIN_WORKDIR"
exec go run ./cmd/tokenlive \
  -conf "$CONF" \
  -data-dir "$DATA_DIR" \
  -admin-workdir "$ADMIN_WORKDIR" \
  "$@"
