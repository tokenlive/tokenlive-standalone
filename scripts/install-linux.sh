#!/usr/bin/env bash
# Install tokenlive on Linux from a release tarball.
#
# Usage:
#   ./install-linux.sh [VERSION] [TARBALL_DIR]
#
#   VERSION      — e.g. 0.6.0 (default: latest from GitHub releases)
#   TARBALL_DIR  — directory containing the pre-downloaded tarball
#                  (skips GitHub download)
#
# What it does:
#   1. Downloads linux tarball for the host arch (if not local)
#   2. Installs binary        -> /usr/local/bin/tokenlive
#   3. Installs admin configs -> /usr/share/tokenlive/admin
#   4. Installs web SPA       -> /usr/share/tokenlive/web
#   5. Installs config        -> /etc/tokenlive/config.yml
#   6. Creates data dir       -> /var/lib/tokenlive
#   7. Installs systemd unit  -> /etc/systemd/system/tokenlive.service
#   8. Enables + starts the service
#
set -euo pipefail

REPO="tokenlive/tokenlive-standalone"
PREFIX="/usr/local"
SHARE_DIR="/usr/share/tokenlive"
CONF_DIR="/etc/tokenlive"
DATA_DIR="/var/lib/tokenlive"
UNIT_DIR="/etc/systemd/system"

die() { echo "error: $*" >&2; exit 1; }

# --- root check --------------------------------------------------------------
[[ "$(id -u)" -eq 0 ]] || die "must run as root (use sudo)"

# --- arch --------------------------------------------------------------------
case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

# --- version -----------------------------------------------------------------
VERSION="${1:-}"
TARBALL_DIR="${2:-}"

if [[ -z "$VERSION" ]]; then
  echo "==> resolving latest version from GitHub"
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep -m1 '"tag_name"' | sed -E 's/.*"v?([^"]+)".*/\1/')"
  [[ -n "$VERSION" ]] || die "cannot resolve latest version"
fi
echo "==> version: $VERSION"

ASSET="tokenlive-${VERSION}-linux-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${VERSION}/${ASSET}"

# --- obtain tarball ----------------------------------------------------------
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

if [[ -n "$TARBALL_DIR" && -f "$TARBALL_DIR/$ASSET" ]]; then
  echo "==> using local tarball: $TARBALL_DIR/$ASSET"
  cp "$TARBALL_DIR/$ASSET" "$WORK_DIR/$ASSET"
else
  echo "==> downloading $URL"
  curl -fSL "$URL" -o "$WORK_DIR/$ASSET"
fi

echo "==> extracting"
mkdir -p "$WORK_DIR/stage"
tar -xzf "$WORK_DIR/$ASSET" -C "$WORK_DIR/stage"

STAGE="$WORK_DIR/stage"
[[ -f "$STAGE/bin/tokenlive" ]] || die "tarball missing bin/tokenlive (unexpected layout)"

# --- install -----------------------------------------------------------------
echo "==> installing binary -> $PREFIX/bin"
install -m 755 "$STAGE/bin/tokenlive" "$PREFIX/bin/tokenlive"

if [[ -d "$STAGE/share/tokenlive/admin" ]]; then
  echo "==> installing admin configs -> $SHARE_DIR/admin"
  mkdir -p "$SHARE_DIR/admin"
  rsync -a --delete "$STAGE/share/tokenlive/admin/" "$SHARE_DIR/admin/"
fi

if [[ -d "$STAGE/share/tokenlive/web" ]]; then
  echo "==> installing web SPA -> $SHARE_DIR/web"
  mkdir -p "$SHARE_DIR/web"
  rsync -a --delete "$STAGE/share/tokenlive/web/" "$SHARE_DIR/web/"
fi

echo "==> installing config -> $CONF_DIR"
mkdir -p "$CONF_DIR"
if [[ -f "$CONF_DIR/config.yml" ]]; then
  echo "    preserving existing config; shipping default as config.yml.default"
  cp "$STAGE/etc/tokenlive/config.yml" "$CONF_DIR/config.yml.default"
else
  cp "$STAGE/etc/tokenlive/config.yml" "$CONF_DIR/config.yml"
  cp "$STAGE/etc/tokenlive/config.yml" "$CONF_DIR/config.yml.default"
fi
cp "$STAGE/etc/tokenlive/config.example.yml" "$CONF_DIR/config.example.yml" 2>/dev/null || true

echo "==> creating data dir -> $DATA_DIR"
mkdir -p "$DATA_DIR"

# --- systemd -----------------------------------------------------------------
UNIT="tokenlive.service"
echo "==> installing systemd unit -> $UNIT_DIR/$UNIT"

# Try to download the bundled unit; fall back to a minimal inline one
UNIT_URL="https://raw.githubusercontent.com/${REPO}/v${VERSION}/packaging/systemd/tokenlive.service"
if curl -fsSL "$UNIT_URL" -o "$WORK_DIR/$UNIT" 2>/dev/null; then
  install -m 644 "$WORK_DIR/$UNIT" "$UNIT_DIR/$UNIT"
elif [[ -f "$TARBALL_DIR/systemd/$UNIT" ]]; then
  install -m 644 "$TARBALL_DIR/systemd/$UNIT" "$UNIT_DIR/$UNIT"
else
  cat > "$UNIT_DIR/$UNIT" <<'EOF'
[Unit]
Description=TokenLive all-in-one LLM API gateway and admin console
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/tokenlive
WorkingDirectory=/var/lib/tokenlive
Restart=on-failure
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
fi

echo "==> enabling + starting tokenlive"
systemctl daemon-reload
systemctl enable tokenlive
systemctl restart tokenlive

sleep 2
if systemctl is-active --quiet tokenlive; then
  echo "==> tokenlive is running"
else
  echo "warn: service not active — check: journalctl -u tokenlive -n 50" >&2
fi

echo
echo "Open http://127.0.0.1:2525 — login admin / admin"
echo "Config: $CONF_DIR/config.yml"
echo "Data:   $DATA_DIR"
echo "Logs:   journalctl -u tokenlive -f"
