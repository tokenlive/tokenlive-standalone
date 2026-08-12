#!/usr/bin/env bash
set -euo pipefail

die() {
  echo "error: $*" >&2
  exit 1
}

[[ "$#" -eq 2 ]] || die "usage: $0 <new-default-file> <target-config-directory>"

NEW_DEFAULT="$1"
CONFIG_DIR="$2"
ACTIVE="$CONFIG_DIR/config.yml"
BASELINE="$CONFIG_DIR/config.yml.default"
TEMP_FILE=""

cleanup() {
  if [[ -n "$TEMP_FILE" ]]; then
    rm -f "$TEMP_FILE"
  fi
}
trap cleanup EXIT

[[ -f "$NEW_DEFAULT" ]] || die "new default config not found: $NEW_DEFAULT"
mkdir -p "$CONFIG_DIR"

atomic_install() {
  local source="$1"
  local target="$2"
  TEMP_FILE="$(mktemp "$CONFIG_DIR/.$(basename "$target").tmp.XXXXXX")"
  install -m 644 "$source" "$TEMP_FILE"
  mv -f "$TEMP_FILE" "$target"
  TEMP_FILE=""
}

if [[ ! -f "$ACTIVE" ]]; then
  atomic_install "$NEW_DEFAULT" "$BASELINE"
  atomic_install "$NEW_DEFAULT" "$ACTIVE"
  echo "config: installed new default"
elif [[ ! -f "$BASELINE" ]]; then
  atomic_install "$NEW_DEFAULT" "$BASELINE"
  echo "config: preserved legacy config (no previous default baseline)"
elif cmp -s "$ACTIVE" "$BASELINE"; then
  atomic_install "$NEW_DEFAULT" "$BASELINE"
  atomic_install "$NEW_DEFAULT" "$ACTIVE"
  echo "config: upgraded unmodified config"
else
  atomic_install "$NEW_DEFAULT" "$BASELINE"
  echo "config: preserved user-modified config"
  echo "config: compare with: diff \"$ACTIVE\" \"$BASELINE\""
fi
