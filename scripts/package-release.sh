#!/usr/bin/env bash
# Build tokenlive binary + share assets for Homebrew / tarball install.
#
# TOKENLIVE_GATEWAY_SRC / TOKENLIVE_ADMIN_SRC — sibling modules
# BREW_PREFIX — if set, bake default paths into the binary
# VERSION / OUT_DIR / SKIP_WEB / FORCE_WEB_BUILD
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-0.2.0}"
OUT_DIR="${OUT_DIR:-$ROOT/dist/tokenlive-${VERSION}}"
GATEWAY_SRC="${TOKENLIVE_GATEWAY_SRC:-$ROOT/../tokenlive-gateway}"
ADMIN_SRC="${TOKENLIVE_ADMIN_SRC:-$ROOT/../tokenlive-admin}"
SKIP_WEB="${SKIP_WEB:-0}"
BREW_PREFIX="${BREW_PREFIX:-}"

die() { echo "error: $*" >&2; exit 1; }

[[ -f "$GATEWAY_SRC/go.mod" ]] || die "gateway not found: $GATEWAY_SRC"
[[ -f "$ADMIN_SRC/go.mod" ]] || die "admin not found: $ADMIN_SRC"
GATEWAY_SRC="$(cd "$GATEWAY_SRC" && pwd)"
ADMIN_SRC="$(cd "$ADMIN_SRC" && pwd)"

echo "==> packaging tokenlive ${VERSION}"
echo "    gateway: $GATEWAY_SRC"
echo "    admin:   $ADMIN_SRC"
echo "    out:     $OUT_DIR"
[[ -n "$BREW_PREFIX" ]] && echo "    brew:    $BREW_PREFIX (bake defaults)"

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR/bin" "$OUT_DIR/share/tokenlive/admin" "$OUT_DIR/share/tokenlive/web" "$OUT_DIR/etc/tokenlive"

if [[ "$SKIP_WEB" != "1" && -f "$ADMIN_SRC/frontend/package.json" ]]; then
  if [[ ! -f "$ADMIN_SRC/frontend/dist/index.html" ]] || [[ "${FORCE_WEB_BUILD:-0}" == "1" ]]; then
    echo "==> building admin frontend"
    ( cd "$ADMIN_SRC/frontend"
      [[ -d node_modules ]] || npm ci
      npm run build:prod
    )
  else
    echo "==> reuse existing frontend dist"
  fi
fi
if [[ -f "$ADMIN_SRC/frontend/dist/index.html" ]]; then
  rsync -a --delete "$ADMIN_SRC/frontend/dist/" "$OUT_DIR/share/tokenlive/web/"
else
  echo "warn: no frontend dist" >&2
fi

rsync -a "$ROOT/configs/admin/" "$OUT_DIR/share/tokenlive/admin/"
cp "$ROOT/config/brew.yml" "$OUT_DIR/etc/tokenlive/config.yml"
cp "$ROOT/config/all-in-one.example.yml" "$OUT_DIR/etc/tokenlive/config.example.yml"

BUILD_DIR="$(mktemp -d "${TMPDIR:-/tmp}/tokenlive-build.XXXXXX")"
trap 'rm -rf "$BUILD_DIR"' EXIT

rsync -a --exclude '.git' --exclude 'bin' --exclude 'data' --exclude 'dist' --exclude 'node_modules' \
  "$ROOT/" "$BUILD_DIR/"

if [[ -n "$BREW_PREFIX" ]]; then
  DEFAULT_CONF="${BREW_PREFIX}/etc/tokenlive/config.yml"
  DEFAULT_DATA="${BREW_PREFIX}/var/tokenlive"
  DEFAULT_ADMIN="${BREW_PREFIX}/share/tokenlive/admin"
  DEFAULT_WEB="${BREW_PREFIX}/share/tokenlive/web"
else
  DEFAULT_CONF=""
  DEFAULT_DATA=""
  DEFAULT_ADMIN=""
  DEFAULT_WEB=""
fi

LDFLAGS=(
  -s -w
  "-X main.version=${VERSION}"
)
[[ -n "$DEFAULT_CONF" ]] && LDFLAGS+=("-X main.DefaultConfigPath=${DEFAULT_CONF}")
[[ -n "$DEFAULT_DATA" ]] && LDFLAGS+=("-X main.DefaultDataDir=${DEFAULT_DATA}")
[[ -n "$DEFAULT_ADMIN" ]] && LDFLAGS+=("-X main.DefaultAdminWorkDir=${DEFAULT_ADMIN}")
[[ -n "$DEFAULT_WEB" ]] && LDFLAGS+=("-X main.DefaultAdminStatic=${DEFAULT_WEB}")

(
  cd "$BUILD_DIR"
  go mod edit -replace="github.com/tokenlive/tokenlive-gateway=${GATEWAY_SRC}"
  go mod edit -replace="github.com/tokenlive/tokenlive-admin=${ADMIN_SRC}"
  go mod tidy
  echo "==> go build"
  go build -ldflags="${LDFLAGS[*]}" -o "bin/tokenlive" ./cmd/tokenlive
  cp bin/tokenlive "$OUT_DIR/bin/tokenlive"
)

( cd "$OUT_DIR/bin" && shasum -a 256 tokenlive > tokenlive.sha256 )
mkdir -p "$ROOT/bin"
cp "$OUT_DIR/bin/tokenlive" "$ROOT/bin/tokenlive"

echo "==> done: $OUT_DIR"
ls -lh "$OUT_DIR/bin/tokenlive"
