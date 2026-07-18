#!/usr/bin/env bash
set -euo pipefail
PREFIX="$(brew --prefix 2>/dev/null || echo /opt/homebrew)"
PLIST_LABEL="homebrew.mxcl.tokenlive"
PLIST_PATH="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"

launchctl bootout "gui/$(id -u)/${PLIST_LABEL}" 2>/dev/null || true
launchctl unload -w "$PLIST_PATH" 2>/dev/null || true
rm -f "$PLIST_PATH"

rm -f "$PREFIX/bin/tokenlive" "$PREFIX/bin/tokenlive-start" "$PREFIX/bin/tokenlive-stop"
rm -rf "$PREFIX/share/tokenlive"
# keep etc/var (user data)
echo "uninstalled binaries; kept $PREFIX/etc/tokenlive and $PREFIX/var/tokenlive if present"
