#!/usr/bin/env bash
# Install tokenlive into the local Homebrew prefix from sibling source checkouts.
#
# This does NOT require a published tap. It:
#   1) packages bin + share + etc via scripts/package-release.sh
#   2) installs into $(brew --prefix)
#   3) writes a launchd plist for `brew services` when possible
#
# Usage:
#   ./scripts/brew-install-local.sh
#   VERSION=0.1.0 ./scripts/brew-install-local.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GATEWAY_SRC="${TOKENLIVE_GATEWAY_SRC:-$ROOT/../tokenlive-gateway}"
ADMIN_SRC="${TOKENLIVE_ADMIN_SRC:-$ROOT/../tokenlive-admin}"
VERSION="${VERSION:-0.1.0}"

die() { echo "error: $*" >&2; exit 1; }
command -v brew >/dev/null || die "Homebrew not found"
command -v rsync >/dev/null || die "rsync not found"

GATEWAY_SRC="$(cd "$GATEWAY_SRC" && pwd)"
ADMIN_SRC="$(cd "$ADMIN_SRC" && pwd)"
[[ -f "$GATEWAY_SRC/go.mod" ]] || die "gateway not found: $GATEWAY_SRC"
[[ -f "$ADMIN_SRC/go.mod" ]] || die "admin not found: $ADMIN_SRC"

PREFIX="$(brew --prefix)"
BIN_DIR="$PREFIX/bin"
ETC_DIR="$PREFIX/etc/tokenlive"
SHARE_DIR="$PREFIX/share/tokenlive"
VAR_DIR="$PREFIX/var/tokenlive"
LOG_DIR="$PREFIX/var/log"
STAGE="$ROOT/dist/tokenlive-${VERSION}"

export TOKENLIVE_GATEWAY_SRC="$GATEWAY_SRC"
export TOKENLIVE_ADMIN_SRC="$ADMIN_SRC"
export VERSION
export OUT_DIR="$STAGE"

echo "==> package"
"$ROOT/scripts/package-release.sh"

echo "==> install into $PREFIX"
mkdir -p "$BIN_DIR" "$ETC_DIR" "$SHARE_DIR/admin" "$SHARE_DIR/web" "$VAR_DIR" "$LOG_DIR"

install -m 755 "$STAGE/bin/tokenlive" "$BIN_DIR/tokenlive"
rsync -a --delete "$STAGE/share/tokenlive/admin/" "$SHARE_DIR/admin/"
if [[ -f "$STAGE/share/tokenlive/web/index.html" ]]; then
  rsync -a --delete "$STAGE/share/tokenlive/web/" "$SHARE_DIR/web/"
fi

if [[ ! -f "$ETC_DIR/config.yml" ]]; then
  install -m 644 "$STAGE/etc/tokenlive/config.yml" "$ETC_DIR/config.yml"
else
  echo "    keep existing $ETC_DIR/config.yml"
fi
install -m 644 "$STAGE/etc/tokenlive/config.example.yml" "$ETC_DIR/config.example.yml"

# launchd plist for brew services compatibility (user domain)
PLIST_LABEL="homebrew.mxcl.tokenlive"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/${PLIST_LABEL}.plist"
mkdir -p "$PLIST_DIR"

# Stop old service if loaded
if launchctl print "gui/$(id -u)/${PLIST_LABEL}" &>/dev/null; then
  launchctl bootout "gui/$(id -u)/${PLIST_LABEL}" 2>/dev/null || true
fi

cat >"$PLIST_PATH" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>${PLIST_LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${BIN_DIR}/tokenlive</string>
    <string>-conf</string>
    <string>${ETC_DIR}/config.yml</string>
    <string>-data-dir</string>
    <string>${VAR_DIR}</string>
    <string>-admin-workdir</string>
    <string>${SHARE_DIR}/admin</string>
    <string>-admin-static</string>
    <string>${SHARE_DIR}/web</string>
  </array>
  <key>WorkingDirectory</key>
  <string>${VAR_DIR}</string>
  <key>RunAtLoad</key>
  <false/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${LOG_DIR}/tokenlive.log</string>
  <key>StandardErrorPath</key>
  <string>${LOG_DIR}/tokenlive.err.log</string>
</dict>
</plist>
EOF

# Helper scripts
cat >"$BIN_DIR/tokenlive-start" <<EOF
#!/bin/bash
launchctl bootstrap "gui/\$(id -u)" "$PLIST_PATH" 2>/dev/null || launchctl load -w "$PLIST_PATH"
echo "tokenlive started (http://127.0.0.1:2525)"
EOF
cat >"$BIN_DIR/tokenlive-stop" <<EOF
#!/bin/bash
launchctl bootout "gui/\$(id -u)/${PLIST_LABEL}" 2>/dev/null || launchctl unload -w "$PLIST_PATH" 2>/dev/null || true
echo "tokenlive stopped"
EOF
chmod +x "$BIN_DIR/tokenlive-start" "$BIN_DIR/tokenlive-stop"

echo
echo "==> installed"
echo "    binary:  $BIN_DIR/tokenlive  ($("$BIN_DIR/tokenlive" -version 2>/dev/null || echo ok))"
echo "    config:  $ETC_DIR/config.yml"
echo "    data:    $VAR_DIR"
echo "    admin:   $SHARE_DIR/admin"
echo "    web:     $SHARE_DIR/web"
echo
echo "Start:"
echo "  tokenlive-start"
echo "  # or: brew-style"
echo "  launchctl bootstrap gui/\$(id -u) $PLIST_PATH"
echo
echo "Stop:"
echo "  tokenlive-stop"
echo
echo "Open:  http://127.0.0.1:2525"
echo "Login: admin / admin"
