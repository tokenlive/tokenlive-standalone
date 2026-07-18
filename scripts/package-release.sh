#!/usr/bin/env bash
# Build a release-ready tokenlive binary + share assets for Homebrew / tarball.
#
# Expects sibling checkouts (or overrides):
#   TOKENLIVE_GATEWAY_SRC  default: ../tokenlive-gateway
#   TOKENLIVE_ADMIN_SRC    default: ../tokenlive-admin
#
# Usage:
#   ./scripts/package-release.sh
#   VERSION=0.1.0 ./scripts/package-release.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.1.0-dev}"
OUT_DIR="${OUT_DIR:-$ROOT/dist/tokenlive-${VERSION}}"
GATEWAY_SRC="${TOKENLIVE_GATEWAY_SRC:-$ROOT/../tokenlive-gateway}"
ADMIN_SRC="${TOKENLIVE_ADMIN_SRC:-$ROOT/../tokenlive-admin}"
SKIP_WEB="${SKIP_WEB:-0}"

die() { echo "error: $*" >&2; exit 1; }

[[ -d "$GATEWAY_SRC" ]] || die "gateway source not found: $GATEWAY_SRC"
[[ -d "$ADMIN_SRC" ]] || die "admin source not found: $ADMIN_SRC"
[[ -f "$GATEWAY_SRC/go.mod" ]] || die "invalid gateway module: $GATEWAY_SRC"
[[ -f "$ADMIN_SRC/go.mod" ]] || die "invalid admin module: $ADMIN_SRC"

GATEWAY_SRC="$(cd "$GATEWAY_SRC" && pwd)"
ADMIN_SRC="$(cd "$ADMIN_SRC" && pwd)"

echo "==> packaging tokenlive ${VERSION}"
echo "    gateway: $GATEWAY_SRC"
echo "    admin:   $ADMIN_SRC"
echo "    out:     $OUT_DIR"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR/bin" "$OUT_DIR/share/tokenlive/admin" "$OUT_DIR/share/tokenlive/web" "$OUT_DIR/etc/tokenlive"

# Frontend
if [[ "$SKIP_WEB" != "1" && -f "$ADMIN_SRC/frontend/package.json" ]]; then
  if [[ ! -f "$ADMIN_SRC/frontend/dist/index.html" ]] || [[ "${FORCE_WEB_BUILD:-0}" == "1" ]]; then
    echo "==> building admin frontend"
    (
      cd "$ADMIN_SRC/frontend"
      if [[ ! -d node_modules ]]; then
        npm ci
      fi
      npm run build:prod
    )
  else
    echo "==> reuse existing frontend dist"
  fi
fi
if [[ -f "$ADMIN_SRC/frontend/dist/index.html" ]]; then
  rsync -a --delete "$ADMIN_SRC/frontend/dist/" "$OUT_DIR/share/tokenlive/web/"
else
  echo "warn: no frontend dist; package will run without console UI" >&2
fi

# Admin runtime configs (toml + menu + casbin)
rsync -a "$ROOT/configs/admin/" "$OUT_DIR/share/tokenlive/admin/"

# Default gateway config for brew
cp "$ROOT/config/brew.yml" "$OUT_DIR/etc/tokenlive/config.yml"
cp "$ROOT/config/all-in-one.example.yml" "$OUT_DIR/etc/tokenlive/config.example.yml"

# Build with temporary replace (do not dirty caller's go.mod permanently)
BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/tokenlive-build.XXXXXX")"
cleanup() { rm -rf "$BUILD_DIR"; }
trap cleanup EXIT

echo "==> staging sources in $BUILD_DIR"
rsync -a \
  --exclude '.git' \
  --exclude 'bin' \
  --exclude 'data' \
  --exclude 'dist' \
  --exclude 'node_modules' \
  "$ROOT/" "$BUILD_DIR/"

(
  cd "$BUILD_DIR"
  go mod edit -replace="github.com/tokenlive/tokenlive-gateway=${GATEWAY_SRC}"
  go mod edit -replace="github.com/tokenlive/tokenlive-admin=${ADMIN_SRC}"
  go mod tidy
  echo "==> go build"
  LDFLAGS="-s -w -X main.version=${VERSION}"
  go build -ldflags="$LDFLAGS" -o "bin/tokenlive" ./cmd/tokenlive
  cp bin/tokenlive "$OUT_DIR/bin/tokenlive"
)

(
  cd "$OUT_DIR/bin"
  shasum -a 256 tokenlive > tokenlive.sha256
)

# Also copy binary to repo bin/ for convenience
mkdir -p "$ROOT/bin"
cp "$OUT_DIR/bin/tokenlive" "$ROOT/bin/tokenlive"

echo "==> done: $OUT_DIR"
ls -lh "$OUT_DIR/bin/tokenlive"
