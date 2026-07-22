#!/usr/bin/env bash
# Install tokenlive for Homebrew services:
#
#   brew services start tokenlive
#   brew services stop tokenlive
#   tokenlive   # foreground, no args (paths baked in)
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GATEWAY_SRC="${TOKENLIVE_GATEWAY_SRC:-$ROOT/../tokenlive-gateway}"
ADMIN_SRC="${TOKENLIVE_ADMIN_SRC:-$ROOT/../tokenlive-admin}"
VERSION="${VERSION:-0.2.0}"

die() { echo "error: $*" >&2; exit 1; }
command -v brew >/dev/null || die "Homebrew not found"
command -v rsync >/dev/null || die "rsync required"

GATEWAY_SRC="$(cd "$GATEWAY_SRC" && pwd)"
ADMIN_SRC="$(cd "$ADMIN_SRC" && pwd)"
PREFIX="$(brew --prefix)"
STAGE="$ROOT/dist/tokenlive-${VERSION}"
KEG="$PREFIX/Cellar/tokenlive/${VERSION}"
OPT="$PREFIX/opt/tokenlive"
PLIST_LABEL="homebrew.mxcl.tokenlive"
USER_PLIST="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"

export TOKENLIVE_GATEWAY_SRC="$GATEWAY_SRC"
export TOKENLIVE_ADMIN_SRC="$ADMIN_SRC"
export VERSION
export OUT_DIR="$STAGE"
export BREW_PREFIX="$PREFIX"

echo "==> package (bake paths under $PREFIX)"
"$ROOT/scripts/package-release.sh"

echo "==> stop previous service"
brew services stop tokenlive 2>/dev/null || true
launchctl bootout "gui/$(id -u)/${PLIST_LABEL}" 2>/dev/null || true
launchctl unload -w "$USER_PLIST" 2>/dev/null || true
pkill -f "${PREFIX}/opt/tokenlive/bin/tokenlive" 2>/dev/null || true
pkill -f "${PREFIX}/bin/tokenlive" 2>/dev/null || true

echo "==> install into Cellar: $KEG"
rm -rf "$PREFIX/Cellar/tokenlive"
mkdir -p "$KEG/bin" "$KEG/share"

install -m 755 "$STAGE/bin/tokenlive" "$KEG/bin/tokenlive"
rsync -a "$STAGE/share/tokenlive/" "$KEG/share/tokenlive/"

mkdir -p "$PREFIX/etc/tokenlive" "$PREFIX/var/tokenlive" "$PREFIX/var/log" "$PREFIX/share"
if [[ ! -f "$PREFIX/etc/tokenlive/config.yml" ]]; then
  install -m 644 "$STAGE/etc/tokenlive/config.yml" "$PREFIX/etc/tokenlive/config.yml"
fi
install -m 644 "$STAGE/etc/tokenlive/config.example.yml" "$PREFIX/etc/tokenlive/config.example.yml"

# Links
rm -rf "$OPT"
ln -sfn "$KEG" "$OPT"
ln -sfn "$OPT/bin/tokenlive" "$PREFIX/bin/tokenlive"
ln -sfn "$OPT/share/tokenlive" "$PREFIX/share/tokenlive"

# LaunchAgent — same shape brew services uses (binary only; paths from ldflags)
mkdir -p "$HOME/Library/LaunchAgents"
cat >"$USER_PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>KeepAlive</key>
	<true/>
	<key>Label</key>
	<string>${PLIST_LABEL}</string>
	<key>LimitLoadToSessionType</key>
	<array>
		<string>Aqua</string>
		<string>Background</string>
		<string>LoginWindow</string>
		<string>StandardIO</string>
		<string>System</string>
	</array>
	<key>ProgramArguments</key>
	<array>
		<string>${OPT}/bin/tokenlive</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>WorkingDirectory</key>
	<string>${PREFIX}/var/tokenlive</string>
	<key>StandardOutPath</key>
	<string>${PREFIX}/var/log/tokenlive.log</string>
	<key>StandardErrorPath</key>
	<string>${PREFIX}/var/log/tokenlive.err.log</string>
</dict>
</plist>
EOF
# Also place plist where brew services discovers keg services
cp "$USER_PLIST" "$KEG/homebrew.mxcl.tokenlive.plist"
ln -sfn "$KEG/homebrew.mxcl.tokenlive.plist" "$OPT/homebrew.mxcl.tokenlive.plist"

# Wrapper helpers (optional; brew services is preferred)
cat >"$PREFIX/bin/tokenlive-start" <<EOF
#!/bin/bash
launchctl bootstrap "gui/\$(id -u)" "$USER_PLIST" 2>/dev/null || launchctl load -w "$USER_PLIST"
echo "tokenlive started — http://127.0.0.1:2525"
EOF
cat >"$PREFIX/bin/tokenlive-stop" <<EOF
#!/bin/bash
launchctl bootout "gui/\$(id -u)/${PLIST_LABEL}" 2>/dev/null || launchctl unload -w "$USER_PLIST" 2>/dev/null || true
echo "tokenlive stopped"
EOF
chmod +x "$PREFIX/bin/tokenlive-start" "$PREFIX/bin/tokenlive-stop"

echo
echo "==> installed"
echo "    binary: $($PREFIX/bin/tokenlive -version)"
echo "    config: $PREFIX/etc/tokenlive/config.yml"
echo "    data:   $PREFIX/var/tokenlive"
echo
echo "Start / stop:"
echo "  brew services start tokenlive   # if formula is linked"
echo "  tokenlive-start                 # LaunchAgent (always works)"
echo "  tokenlive                       # foreground, no args"
echo
echo "Stop:"
echo "  brew services stop tokenlive"
echo "  tokenlive-stop"
echo
echo "Open http://127.0.0.1:2525  — login admin / admin"

# Try brew services; fall back is already installed via LaunchAgent
if brew services start tokenlive 2>/dev/null; then
  sleep 2
  brew services list 2>/dev/null | grep tokenlive || true
else
  echo "(brew services name not registered — use tokenlive-start / LaunchAgent)"
  "$PREFIX/bin/tokenlive-start"
fi

sleep 2
if curl -sf http://127.0.0.1:2525/health >/dev/null; then
  echo "health: $(curl -s http://127.0.0.1:2525/health)"
else
  echo "health: not up yet — check $(brew --prefix)/var/log/tokenlive.err.log"
fi
