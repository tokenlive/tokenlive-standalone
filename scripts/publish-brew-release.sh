#!/usr/bin/env bash
# Publish a Homebrew-ready release for tokenlive-standalone.
#
# Flow:
#   1. Build package (binary + admin/web + etc) with Homebrew default paths
#   2. Create darwin-arm64 tarball
#   3. Create/update GitHub Release and upload the asset
#   4. Bump tokenlive/homebrew-tokenlive Formula (version / url / sha256)
#
# Required tools: bash, go, node/npm, rsync, tar, shasum, gh, git
#
# Env:
#   VERSION              — e.g. 0.2.0 (or v0.2.0). Default: derived from git tag / GITHUB_REF_NAME
#   BREW_PREFIX          — baked into binary. Default: /opt/homebrew
#   FORCE_WEB_BUILD=1    — rebuild admin frontend (default in CI)
#   SKIP_RELEASE=1       — build tarball only, skip GitHub Release + tap
#   SKIP_TAP=1           — skip homebrew-tokenlive update
#   TAP_REPO             — default: tokenlive/homebrew-tokenlive
#   TAP_FORMULA_PATH     — default: Formula/tokenlive.rb
#   GITHUB_TOKEN / GH_TOKEN / HOMEBREW_TAP_TOKEN
#       HOMEBREW_TAP_TOKEN is preferred for pushing the tap (needs contents:write on TAP_REPO).
#       Release upload uses GH_TOKEN/GITHUB_TOKEN (repo-scoped is enough).
#   TOKENLIVE_GATEWAY_SRC / TOKENLIVE_ADMIN_SRC — local checkouts (CI sets these)
#   GATEWAY_REF / ADMIN_REF — optional pin; default: versions from go.mod
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
need shasum
need git
need gh

# --- version -----------------------------------------------------------------
raw_version="${VERSION:-${GITHUB_REF_NAME:-}}"
if [[ -z "$raw_version" ]]; then
  raw_version="$(git describe --tags --exact-match 2>/dev/null || true)"
fi
[[ -n "$raw_version" ]] || die "VERSION not set (pass VERSION=x.y.z or run on a v* tag)"

# strip leading v
VERSION="${raw_version#v}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-].*)?$ ]] || die "invalid version: $raw_version (expected semver, optional leading v)"

TAG="v${VERSION}"
ASSET="tokenlive-${VERSION}-darwin-arm64.tar.gz"
BREW_PREFIX="${BREW_PREFIX:-/opt/homebrew}"
FORCE_WEB_BUILD="${FORCE_WEB_BUILD:-1}"
SKIP_RELEASE="${SKIP_RELEASE:-0}"
SKIP_TAP="${SKIP_TAP:-0}"
TAP_REPO="${TAP_REPO:-tokenlive/homebrew-tokenlive}"
TAP_FORMULA_PATH="${TAP_FORMULA_PATH:-Formula/tokenlive.rb}"
STANDALONE_REPO="${STANDALONE_REPO:-tokenlive/tokenlive-standalone}"
OUT_DIR="${OUT_DIR:-$ROOT/dist/tokenlive-${VERSION}}"
DIST_DIR="$ROOT/dist"
TARBALL="$DIST_DIR/$ASSET"

export VERSION OUT_DIR BREW_PREFIX FORCE_WEB_BUILD

echo "==> publish brew release"
echo "    version:  $VERSION  (tag $TAG)"
echo "    brew:     $BREW_PREFIX"
echo "    asset:    $ASSET"
echo "    out:      $OUT_DIR"

# --- resolve gateway / admin sources ----------------------------------------
mod_ver() {
  # print module version from go.mod (e.g. v0.2.0)
  go list -m -f '{{.Version}}' "$1" 2>/dev/null || true
}

resolve_dep_src() {
  # $1=env name for path, $2=module path, $3=default sibling dir name, $4=optional ref env
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

  # CI / clean machine: shallow clone at go.mod version
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

# --- build package -----------------------------------------------------------
"$ROOT/scripts/package-release.sh"
[[ -x "$OUT_DIR/bin/tokenlive" ]] || die "missing binary after package-release"
built_ver="$("$OUT_DIR/bin/tokenlive" -version | tr -d '[:space:]')"
[[ "$built_ver" == "$VERSION" ]] || die "binary version mismatch: got '$built_ver', want '$VERSION'"

# --- tarball -----------------------------------------------------------------
mkdir -p "$DIST_DIR"
rm -f "$TARBALL"
# Match formula layout: bin/, share/, etc/ at archive root (no parent dir).
tar -czf "$TARBALL" -C "$OUT_DIR" .
SHA256="$(shasum -a 256 "$TARBALL" | awk '{print $1}')"
echo "$SHA256  $ASSET" | tee "$TARBALL.sha256"
ls -lh "$TARBALL"

if [[ "$SKIP_RELEASE" == "1" ]]; then
  echo "==> SKIP_RELEASE=1 — tarball ready at $TARBALL"
  exit 0
fi

need_token_for_release() {
  [[ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ]] || die "GH_TOKEN/GITHUB_TOKEN required to publish release"
}
need_token_for_release
# gh prefers GH_TOKEN
export GH_TOKEN="${GH_TOKEN:-$GITHUB_TOKEN}"

# --- GitHub Release ----------------------------------------------------------
echo "==> GitHub Release $TAG"
notes="$(cat <<EOF
## TokenLive ${VERSION}

All-in-one LLM API gateway + admin console.

### Install / Upgrade (Homebrew)

\`\`\`bash
brew tap tokenlive/tokenlive
brew update
brew upgrade tokenlive   # or: brew install tokenlive
brew services restart tokenlive
# http://127.0.0.1:2525  admin/admin
\`\`\`

Data (\`\$(brew --prefix)/var/tokenlive\`) and config (\`\$(brew --prefix)/etc/tokenlive\`) are preserved across upgrades.

### Assets

- \`${ASSET}\` — Apple Silicon (darwin-arm64) prebuilt binary with baked Homebrew default paths
- sha256: \`${SHA256}\`
EOF
)"

if gh release view "$TAG" --repo "$STANDALONE_REPO" >/dev/null 2>&1; then
  echo "    release exists — uploading/replacing asset"
  # clobber existing asset if re-running
  gh release upload "$TAG" "$TARBALL" "$TARBALL.sha256" \
    --repo "$STANDALONE_REPO" --clobber
  gh release edit "$TAG" --repo "$STANDALONE_REPO" --notes "$notes" >/dev/null
else
  gh release create "$TAG" "$TARBALL" "$TARBALL.sha256" \
    --repo "$STANDALONE_REPO" \
    --title "$TAG" \
    --notes "$notes"
fi

RELEASE_URL="$(gh release view "$TAG" --repo "$STANDALONE_REPO" --json url -q .url)"
ASSET_URL="https://github.com/${STANDALONE_REPO}/releases/download/${TAG}/${ASSET}"
echo "    release: $RELEASE_URL"
echo "    asset:   $ASSET_URL"

# --- update homebrew tap -----------------------------------------------------
if [[ "$SKIP_TAP" == "1" ]]; then
  echo "==> SKIP_TAP=1 — done"
  exit 0
fi

echo "==> update tap $TAP_REPO ($TAP_FORMULA_PATH)"
TAP_TOKEN="${HOMEBREW_TAP_TOKEN:-${GH_TOKEN:-${GITHUB_TOKEN:-}}}"
[[ -n "$TAP_TOKEN" ]] || die "HOMEBREW_TAP_TOKEN (or GH_TOKEN) required to push tap"

TAP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/homebrew-tokenlive.XXXXXX")"
cleanup_tap() { rm -rf "$TAP_DIR"; }
trap cleanup_tap EXIT

# Use x-access-token so the token is not echoed in remote -v on failure paths.
git clone --depth 1 "https://x-access-token:${TAP_TOKEN}@github.com/${TAP_REPO}.git" "$TAP_DIR"
FORMULA="$TAP_DIR/$TAP_FORMULA_PATH"
[[ -f "$FORMULA" ]] || die "formula not found: $TAP_FORMULA_PATH in $TAP_REPO"

# Rewrite version / url / sha256 in place (portable python — always on macOS runners).
VERSION="$VERSION" ASSET_URL="$ASSET_URL" SHA256="$SHA256" FORMULA="$FORMULA" python3 - <<'PY'
import os, re, pathlib
path = pathlib.Path(os.environ["FORMULA"])
text = path.read_text()
version = os.environ["VERSION"]
url = os.environ["ASSET_URL"]
sha = os.environ["SHA256"]

def sub_one(pattern, repl, s, label):
    out, n = re.subn(pattern, repl, s, count=1)
    if n != 1:
        raise SystemExit(f"failed to update {label}: expected 1 match, got {n}")
    return out

text = sub_one(r'version\s+"[^"]+"', f'version "{version}"', text, "version")
text = sub_one(r'url\s+"https://github\.com/tokenlive/tokenlive-standalone/releases/download/[^"]+"',
               f'url "{url}"', text, "url")
text = sub_one(r'sha256\s+"[0-9a-fA-F]{64}"', f'sha256 "{sha}"', text, "sha256")
path.write_text(text)
print(path.read_text())
PY

git -C "$TAP_DIR" config user.name "${GIT_AUTHOR_NAME:-tokenlive-release[bot]}"
git -C "$TAP_DIR" config user.email "${GIT_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}"

if git -C "$TAP_DIR" diff --quiet -- "$TAP_FORMULA_PATH"; then
  echo "    formula already up to date"
else
  git -C "$TAP_DIR" add "$TAP_FORMULA_PATH"
  git -C "$TAP_DIR" commit -m "tokenlive v${VERSION} formula (darwin-arm64)"
  git -C "$TAP_DIR" push origin HEAD
  echo "    pushed formula v${VERSION}"
fi

echo "==> done"
echo "    brew update && brew upgrade tokenlive"
