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
VERSION="${VERSION:-0.9.5}"

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

# Generate INSTALL_RECEIPT.json so Homebrew recognises tokenlive as an installed formula
cat >"$KEG/INSTALL_RECEIPT.json" <<EOF
{
  "homebrew_version": "6.0.0",
  "used_options": [],
  "unused_options": [],
  "built_as_bottle": false,
  "poured_from_bottle": false,
  "installed_on_request": true,
  "changed_files": null,
  "time": $(date +%s),
  "source_modified_time": $(date +%s),
  "compiler": "clang",
  "aliases": [],
  "runtime_dependencies": [],
  "source": {
    "path": "$ROOT/packaging/homebrew/tokenlive.rb",
    "tap": "tokenlive/tokenlive",
    "tap_git_head": null,
    "spec": "stable",
    "versions": {
      "stable": "${VERSION}",
      "head": null,
      "version_scheme": 0,
      "compatibility_version": null
    }
  },
  "arch": "$(uname -m)",
  "built_on": {
    "os": "Macintosh",
    "os_version": "macOS",
    "cpu_family": "$(uname -m)"
  }
}
EOF

mkdir -p "$PREFIX/etc/tokenlive" "$PREFIX/var/tokenlive" "$PREFIX/var/log" "$PREFIX/share"
"$ROOT/scripts/install-brew-config.sh" \
  "$STAGE/etc/tokenlive/config.yml" \
  "$PREFIX/etc/tokenlive"
install -m 644 "$STAGE/etc/tokenlive/config.example.yml" "$PREFIX/etc/tokenlive/config.example.yml"

# Links
brew link --overwrite tokenlive 2>/dev/null || {
  rm -rf "$OPT"
  ln -sfn "$KEG" "$OPT"
  ln -sfn "$OPT/bin/tokenlive" "$PREFIX/bin/tokenlive"
  ln -sfn "$OPT/share/tokenlive" "$PREFIX/share/tokenlive"
}

# Service definition files for Homebrew
cat >"$KEG/homebrew.mxcl.tokenlive.plist" <<EOF
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

cat >"$KEG/homebrew.tokenlive.service" <<EOF
[Unit]
Description=Homebrew generated unit for tokenlive

[Install]
WantedBy=default.target

[Service]
Type=simple
ExecStart="${OPT}/bin/tokenlive"
Restart=always
WorkingDirectory=${PREFIX}/var/tokenlive
StandardOutput=append:${PREFIX}/var/log/tokenlive.log
StandardError=append:${PREFIX}/var/log/tokenlive.err.log
EOF

# Sync to User LaunchAgents
mkdir -p "$HOME/Library/LaunchAgents"
cp "$KEG/homebrew.mxcl.tokenlive.plist" "$USER_PLIST"

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
echo "Logs & Status:"
echo "  tokenlive logs                  # view running logs"
echo "  tokenlive logs -f               # follow logs in real time"
echo "  tokenlive logs -e               # view error logs only"
echo "  tokenlive logs --check          # check if errors exist"
echo "  tokenlive status                # view service status"
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
  echo "health: not up yet — check logs with: tokenlive logs -e"
fi
