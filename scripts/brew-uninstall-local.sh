#!/usr/bin/env bash
set -euo pipefail
PREFIX="$(brew --prefix 2>/dev/null || echo /opt/homebrew)"
PLIST_LABEL="homebrew.mxcl.tokenlive"
USER_PLIST="$HOME/Library/LaunchAgents/${PLIST_LABEL}.plist"

brew services stop tokenlive 2>/dev/null || true
launchctl bootout "gui/$(id -u)/${PLIST_LABEL}" 2>/dev/null || true
launchctl unload -w "$USER_PLIST" 2>/dev/null || true
rm -f "$USER_PLIST"

rm -f "$PREFIX/bin/tokenlive" "$PREFIX/bin/tokenlive-start" "$PREFIX/bin/tokenlive-stop"
rm -rf "$PREFIX/opt/tokenlive" "$PREFIX/Cellar/tokenlive" "$PREFIX/share/tokenlive"

echo "uninstalled tokenlive binary/cellar"
echo "kept: $PREFIX/etc/tokenlive  $PREFIX/var/tokenlive"
if [[ "${1:-}" == "--purge" ]]; then
  rm -rf "$PREFIX/etc/tokenlive" "$PREFIX/var/tokenlive"
  rm -f "$PREFIX/var/log/tokenlive.log" "$PREFIX/var/log/tokenlive.err.log"
  echo "purged etc/var/logs"
fi
