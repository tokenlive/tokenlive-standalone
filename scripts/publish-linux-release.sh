#!/usr/bin/env bash
# Publish a Linux release for tokenlive-standalone.
#
# Builds linux-amd64 + linux-arm64 tarballs (binary + admin/web + config),
# uploads them to the GitHub Release for tag vX.Y.Z.
#
# Flow:
#   1. Resolve VERSION (from VERSION env, GITHUB_REF_NAME, or git tag)
#   2. Clone gateway/admin at go.mod versions (or use siblings)
#   3. Build admin frontend (once, shared across arches)
#   4. Cross-compile linux/amd64 and linux/arm64, bake Linux FHS paths
#   5. Create tarballs + sha256
#   6. Upload to GitHub Release
#
# Required tools: bash, go, node/npm, rsync, tar, shasum (or sha256sum), git, gh
#
# Env:
#   VERSION              — e.g. 0.9.5 (or v0.9.5). Default: git tag / GITHUB_REF_NAME
#   FORCE_WEB_BUILD=1    — rebuild admin frontend (default in CI)
#   SKIP_RELEASE=1       — build tarballs only, skip GitHub Release upload
#   STANDALONE_REPO      — default: tokenlive/tokenlive-standalone
#   TOKENLIVE_GATEWAY_SRC / TOKENLIVE_ADMIN_SRC — local checkouts (CI sets these)
#   GATEWAY_REF / ADMIN_REF — optional pin; default: versions from go.mod
#   GH_TOKEN / GITHUB_TOKEN — for release upload
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

die() { echo "error: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }

need bash
need go
need npm
need rsync
need tar
need git
need gh

# sha256 helper that works on both macOS (shasum) and Linux (sha256sum)
sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    sha256sum "$1" | awk '{print $1}'
  fi
}

# --- version -----------------------------------------------------------------
raw_version="${VERSION:-${GITHUB_REF_NAME:-}}"
if [[ -z "$raw_version" ]]; then
  raw_version="$(git describe --tags --exact-match 2>/dev/null || true)"
fi
[[ -n "$raw_version" ]] || die "VERSION not set (pass VERSION=x.y.z or run on a v* tag)"

VERSION="${raw_version#v}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-].*)?$ ]] || die "invalid version: $raw_version (expected semver, optional leading v)"

TAG="v${VERSION}"
ASSET_AMD64="tokenlive-${VERSION}-linux-amd64.tar.gz"
ASSET_ARM64="tokenlive-${VERSION}-linux-arm64.tar.gz"
FORCE_WEB_BUILD="${FORCE_WEB_BUILD:-1}"
SKIP_RELEASE="${SKIP_RELEASE:-0}"
STANDALONE_REPO="${STANDALONE_REPO:-tokenlive/tokenlive-standalone}"
DIST_DIR="$ROOT/dist"

# Linux FHS paths baked into the binary
export DEFAULT_CONF="/etc/tokenlive/config.yml"
export DEFAULT_DATA="/var/lib/tokenlive"
export DEFAULT_ADMIN_DIR="/usr/share/tokenlive/admin"
export DEFAULT_WEB_DIR="/usr/share/tokenlive/web"
export CONFIG_FILE="config/linux.yml"
export VERSION FORCE_WEB_BUILD

echo "==> publish linux release"
echo "    version:  $VERSION  (tag $TAG)"
echo "    assets:   $ASSET_AMD64, $ASSET_ARM64"

# --- resolve gateway / admin sources ----------------------------------------
mod_ver() {
  go list -m -f '{{.Version}}' "$1" 2>/dev/null || true
}

resolve_dep_src() {
  local env_name="$1" module="$2" sibling="$3" ref_env="$4"
  local src="${!env_name:-}"
  local ref="${!ref_env:-}"

  if [[ -n "$src" && -f "$src/go.mod" ]]; then
    echo "$src"
    return
  fi

  if [[ -f "$ROOT/../$sibling/go.mod" ]]; then
    echo "$(cd "$ROOT/../$sibling" && pwd)"
    return
  fi

  ref="${ref:-$(mod_ver "$module")}"
  [[ -n "$ref" ]] || die "cannot resolve version for $module (go.mod / ${ref_env})"
  local dest="$ROOT/.deps/$sibling"
  echo "    cloning $module@$ref -> $dest" >&2
  rm -rf "$dest"
  git clone --depth 1 --branch "$ref" "https://github.com/${module#github.com/}.git" "$dest" >&2
  echo "$dest"
}

export TOKENLIVE_GATEWAY_SRC
export TOKENLIVE_ADMIN_SRC
TOKENLIVE_GATEWAY_SRC="$(resolve_dep_src TOKENLIVE_GATEWAY_SRC github.com/tokenlive/tokenlive-gateway tokenlive-gateway GATEWAY_REF)"
TOKENLIVE_ADMIN_SRC="$(resolve_dep_src TOKENLIVE_ADMIN_SRC github.com/tokenlive/tokenlive-admin tokenlive-admin ADMIN_REF)"

echo "    gateway:  $TOKENLIVE_GATEWAY_SRC"
echo "    admin:    $TOKENLIVE_ADMIN_SRC"

mkdir -p "$DIST_DIR"

# --- build amd64 package -----------------------------------------------------
echo "==> building linux/amd64 package"
OUT_DIR_AMD64="$DIST_DIR/tokenlive-${VERSION}-linux-amd64"
TARGET_GOOS=linux TARGET_GOARCH=amd64 OUT_DIR="$OUT_DIR_AMD64" "$ROOT/scripts/package-release.sh"
TARBALL_AMD64="$DIST_DIR/$ASSET_AMD64"
rm -f "$TARBALL_AMD64"
tar -czf "$TARBALL_AMD64" -C "$OUT_DIR_AMD64" .
SHA256_AMD64="$(sha256_file "$TARBALL_AMD64")"
echo "$SHA256_AMD64  $ASSET_AMD64" | tee "$TARBALL_AMD64.sha256"
ls -lh "$TARBALL_AMD64"

# --- build arm64 package -----------------------------------------------------
echo "==> building linux/arm64 package"
OUT_DIR_ARM64="$DIST_DIR/tokenlive-${VERSION}-linux-arm64"
SKIP_WEB=1 TARGET_GOOS=linux TARGET_GOARCH=arm64 OUT_DIR="$OUT_DIR_ARM64" "$ROOT/scripts/package-release.sh"
if [[ -d "$OUT_DIR_AMD64/share/tokenlive/web" ]]; then
  mkdir -p "$OUT_DIR_ARM64/share/tokenlive/web"
  rsync -a "$OUT_DIR_AMD64/share/tokenlive/web/" "$OUT_DIR_ARM64/share/tokenlive/web/"
fi
TARBALL_ARM64="$DIST_DIR/$ASSET_ARM64"
rm -f "$TARBALL_ARM64"
tar -czf "$TARBALL_ARM64" -C "$OUT_DIR_ARM64" .
SHA256_ARM64="$(sha256_file "$TARBALL_ARM64")"
echo "$SHA256_ARM64  $ASSET_ARM64" | tee "$TARBALL_ARM64.sha256"
ls -lh "$TARBALL_ARM64"

# Bundle systemd unit + install script into a separate assets tarball
ASSET_SVC="tokenlive-${VERSION}-linux-services.tar.gz"
SVC_DIR="$DIST_DIR/tokenlive-${VERSION}-linux-services"
rm -rf "$SVC_DIR"
mkdir -p "$SVC_DIR/systemd" "$SVC_DIR/bin"
cp "$ROOT/packaging/systemd/tokenlive.service" "$SVC_DIR/systemd/"
cp "$ROOT/scripts/install-linux.sh" "$SVC_DIR/bin/"
chmod +x "$SVC_DIR/bin/install-linux.sh"
TARBALL_SVC="$DIST_DIR/$ASSET_SVC"
rm -f "$TARBALL_SVC"
tar -czf "$TARBALL_SVC" -C "$SVC_DIR" .
SHA256_SVC="$(sha256_file "$TARBALL_SVC")"
echo "$SHA256_SVC  $ASSET_SVC" | tee "$TARBALL_SVC.sha256"
ls -lh "$TARBALL_SVC"

if [[ "$SKIP_RELEASE" == "1" ]]; then
  echo "==> SKIP_RELEASE=1 — tarballs ready at $DIST_DIR"
  exit 0
fi

# --- GitHub Release ----------------------------------------------------------
[[ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ]] || die "GH_TOKEN/GITHUB_TOKEN required to publish release"
export GH_TOKEN="${GH_TOKEN:-$GITHUB_TOKEN}"

echo "==> upload to GitHub Release $TAG"
# Create a draft release if the macOS job hasn't created it yet (avoids race).
if ! gh release view "$TAG" --repo "$STANDALONE_REPO" >/dev/null 2>&1; then
  gh release create "$TAG" --repo "$STANDALONE_REPO" --title "$TAG" \
    --notes "## TokenLive ${VERSION}" --draft 2>/dev/null || true
fi

for f in \
  "$TARBALL_AMD64" "$TARBALL_AMD64.sha256" \
  "$TARBALL_ARM64" "$TARBALL_ARM64.sha256" \
  "$TARBALL_SVC" "$TARBALL_SVC.sha256"; do
  gh release upload "$TAG" "$f" --repo "$STANDALONE_REPO" --clobber
done

echo "==> done"
echo "    release: https://github.com/${STANDALONE_REPO}/releases/tag/${TAG}"
echo "    install: curl -fsSL https://github.com/${STANDALONE_REPO}/releases/download/${TAG}/${ASSET_SVC} | tar -xz && bin/install-linux.sh ${VERSION}"
