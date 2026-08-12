# Homebrew Config Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Automatically replace an unmodified Homebrew `config.yml` during upgrades while preserving any user-modified configuration.

**Architecture:** Store the last installed official default beside the active configuration as `config.yml.default`. A shared shell helper compares the two files byte-for-byte before installing a new default; both the local installer and Homebrew Formula call this helper. Release packaging ships the helper, and release publishing synchronizes the canonical Formula into the tap so users receive the behavior.

**Tech Stack:** Bash, Homebrew Ruby Formula DSL, Python 3 standard library, Make, existing release shell scripts.

## Global Constraints

- `$(brew --prefix)/etc/tokenlive/config.yml` remains the only runtime configuration file.
- Any byte-level difference, including comments or whitespace, counts as a user modification.
- A modified `config.yml` must never be overwritten.
- A missing prior baseline must be treated as a legacy modified installation and preserved.
- Installed configuration files must use mode `0644`.
- Formula and local Homebrew-style installation must use the same comparison implementation.
- Do not edit or stage the user's existing uncommitted `config/brew.yml` change.

## File Map

- Create `scripts/install-brew-config.sh`: shared comparison and atomic installation logic.
- Create `scripts/install-brew-config_test.sh`: isolated shell behavior tests.
- Modify `Makefile`: include the shell behavior tests in `make test`.
- Modify `scripts/package-release.sh`: place the shared helper in release tarballs.
- Modify `scripts/brew-install-local.sh`: replace the `events:` heuristic with the shared helper.
- Modify `packaging/homebrew/tokenlive.rb`: make the canonical Formula consume the prebuilt release layout and shared helper.
- Create `scripts/update_homebrew_formula.py`: update Formula version, URLs, and checksums deterministically.
- Create `scripts/update_homebrew_formula_test.py`: verify both architecture blocks and preserve Formula behavior.
- Modify `scripts/publish-brew-release.sh`: copy the canonical Formula into the tap and invoke the updater.
- Modify `docs/homebrew.md`: document active/default config behavior and upgrade messages.

---

### Task 1: Shared Configuration Installer

**Files:**
- Create: `scripts/install-brew-config_test.sh`
- Create: `scripts/install-brew-config.sh`
- Modify: `Makefile:15-17`

**Interfaces:**
- Consumes: `scripts/install-brew-config.sh <new-default-file> <target-config-directory>`
- Produces: active `<target>/config.yml`, baseline `<target>/config.yml.default`, one primary `config:` status line, and an optional comparison hint.

- [ ] **Step 1: Write the failing shell behavior test**

Create `scripts/install-brew-config_test.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HELPER="$ROOT/scripts/install-brew-config.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tokenlive-config-test.XXXXXX")"
trap 'chmod -R u+w "$TEST_ROOT" 2>/dev/null || true; rm -rf "$TEST_ROOT"' EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_same() {
  cmp -s "$1" "$2" || fail "$1 and $2 differ"
}

assert_contains() {
  [[ "$1" == *"$2"* ]] || fail "expected output to contain: $2"
}

write_default() {
  local path="$1"
  local marker="$2"
  printf 'env: prod\nmarker: %s\n' "$marker" >"$path"
}

run_helper() {
  bash "$HELPER" "$1" "$2"
}

[[ -x "$HELPER" ]] || fail "missing executable helper: $HELPER"

new_v1="$TEST_ROOT/new-v1.yml"
new_v2="$TEST_ROOT/new-v2.yml"
new_v3="$TEST_ROOT/new-v3.yml"
write_default "$new_v1" v1
write_default "$new_v2" v2
write_default "$new_v3" v3

fresh="$TEST_ROOT/fresh"
output="$(run_helper "$new_v1" "$fresh")"
assert_contains "$output" "config: installed new default"
assert_same "$new_v1" "$fresh/config.yml"
assert_same "$new_v1" "$fresh/config.yml.default"
[[ "$(stat -f '%Lp' "$fresh/config.yml")" == "644" ]] || fail "active mode is not 644"
[[ "$(stat -f '%Lp' "$fresh/config.yml.default")" == "644" ]] || fail "baseline mode is not 644"

output="$(run_helper "$new_v2" "$fresh")"
assert_contains "$output" "config: upgraded unmodified config"
assert_same "$new_v2" "$fresh/config.yml"
assert_same "$new_v2" "$fresh/config.yml.default"

output="$(run_helper "$new_v3" "$fresh")"
assert_contains "$output" "config: upgraded unmodified config"
assert_same "$new_v3" "$fresh/config.yml"
assert_same "$new_v3" "$fresh/config.yml.default"

modified="$TEST_ROOT/modified"
run_helper "$new_v1" "$modified" >/dev/null
printf 'custom: true\n' >>"$modified/config.yml"
cp "$modified/config.yml" "$TEST_ROOT/expected-modified.yml"
output="$(run_helper "$new_v2" "$modified")"
assert_contains "$output" "config: preserved user-modified config"
assert_same "$TEST_ROOT/expected-modified.yml" "$modified/config.yml"
assert_same "$new_v2" "$modified/config.yml.default"

comment_only="$TEST_ROOT/comment-only"
run_helper "$new_v1" "$comment_only" >/dev/null
printf '# user comment\n' >>"$comment_only/config.yml"
cp "$comment_only/config.yml" "$TEST_ROOT/expected-comment.yml"
output="$(run_helper "$new_v2" "$comment_only")"
assert_contains "$output" "config: preserved user-modified config"
assert_same "$TEST_ROOT/expected-comment.yml" "$comment_only/config.yml"
assert_same "$new_v2" "$comment_only/config.yml.default"

legacy="$TEST_ROOT/legacy"
mkdir -p "$legacy"
printf 'legacy: customized\n' >"$legacy/config.yml"
cp "$legacy/config.yml" "$TEST_ROOT/expected-legacy.yml"
output="$(run_helper "$new_v2" "$legacy")"
assert_contains "$output" "config: preserved legacy config"
assert_same "$TEST_ROOT/expected-legacy.yml" "$legacy/config.yml"
assert_same "$new_v2" "$legacy/config.yml.default"

missing_active="$TEST_ROOT/missing-active"
mkdir -p "$missing_active"
cp "$new_v1" "$missing_active/config.yml.default"
output="$(run_helper "$new_v2" "$missing_active")"
assert_contains "$output" "config: installed new default"
assert_same "$new_v2" "$missing_active/config.yml"
assert_same "$new_v2" "$missing_active/config.yml.default"

failure="$TEST_ROOT/failure"
run_helper "$new_v1" "$failure" >/dev/null
cp "$failure/config.yml" "$TEST_ROOT/expected-failure.yml"
if run_helper "$TEST_ROOT/does-not-exist.yml" "$failure" >/dev/null 2>&1; then
  fail "missing source unexpectedly succeeded"
fi
assert_same "$TEST_ROOT/expected-failure.yml" "$failure/config.yml"

read_only="$TEST_ROOT/read-only"
run_helper "$new_v1" "$read_only" >/dev/null
cp "$read_only/config.yml" "$TEST_ROOT/expected-read-only.yml"
chmod 500 "$read_only"
if run_helper "$new_v2" "$read_only" >/dev/null 2>&1; then
  fail "read-only target unexpectedly succeeded"
fi
chmod 700 "$read_only"
assert_same "$TEST_ROOT/expected-read-only.yml" "$read_only/config.yml"

echo "PASS: install-brew-config"
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
bash scripts/install-brew-config_test.sh
```

Expected: `FAIL: missing executable helper`.

- [ ] **Step 3: Implement the minimal shared installer**

Create `scripts/install-brew-config.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

die() {
  echo "error: $*" >&2
  exit 1
}

[[ "$#" -eq 2 ]] || die "usage: $0 <new-default-file> <target-config-directory>"

NEW_DEFAULT="$1"
CONFIG_DIR="$2"
ACTIVE="$CONFIG_DIR/config.yml"
BASELINE="$CONFIG_DIR/config.yml.default"
TEMP_FILE=""

cleanup() {
  if [[ -n "$TEMP_FILE" ]]; then
    rm -f "$TEMP_FILE"
  fi
}
trap cleanup EXIT

[[ -f "$NEW_DEFAULT" ]] || die "new default config not found: $NEW_DEFAULT"
mkdir -p "$CONFIG_DIR"

atomic_install() {
  local source="$1"
  local target="$2"
  TEMP_FILE="$(mktemp "$CONFIG_DIR/.$(basename "$target").tmp.XXXXXX")"
  install -m 644 "$source" "$TEMP_FILE"
  mv -f "$TEMP_FILE" "$target"
  TEMP_FILE=""
}

if [[ ! -f "$ACTIVE" ]]; then
  atomic_install "$NEW_DEFAULT" "$BASELINE"
  atomic_install "$NEW_DEFAULT" "$ACTIVE"
  echo "config: installed new default"
elif [[ ! -f "$BASELINE" ]]; then
  atomic_install "$NEW_DEFAULT" "$BASELINE"
  echo "config: preserved legacy config (no previous default baseline)"
elif cmp -s "$ACTIVE" "$BASELINE"; then
  atomic_install "$NEW_DEFAULT" "$BASELINE"
  atomic_install "$NEW_DEFAULT" "$ACTIVE"
  echo "config: upgraded unmodified config"
else
  atomic_install "$NEW_DEFAULT" "$BASELINE"
  echo "config: preserved user-modified config"
  echo "config: compare with: diff \"$ACTIVE\" \"$BASELINE\""
fi
```

Make both scripts executable:

```bash
chmod +x scripts/install-brew-config.sh scripts/install-brew-config_test.sh
```

- [ ] **Step 4: Add the behavior test to the project test target**

Change `Makefile`:

```make
test:
	go test ./...
	bash scripts/install-brew-config_test.sh
```

- [ ] **Step 5: Run tests and shell syntax checks**

Run:

```bash
bash -n scripts/install-brew-config.sh
bash -n scripts/install-brew-config_test.sh
bash scripts/install-brew-config_test.sh
make test
```

Expected: syntax checks exit `0`; both test commands print `PASS: install-brew-config`; Go tests pass.

- [ ] **Step 6: Commit the shared installer**

```bash
git add Makefile scripts/install-brew-config.sh scripts/install-brew-config_test.sh
git commit -m "feat: safely update Homebrew configuration"
```

---

### Task 2: Release Package and Local Installer Integration

**Files:**
- Modify: `scripts/package-release.sh:32-54`
- Modify: `scripts/brew-install-local.sh:51-55`

**Interfaces:**
- Consumes: `scripts/install-brew-config.sh` from Task 1.
- Produces: release artifact `libexec/install-brew-config.sh`; local installation delegates config decisions to the shared helper.

- [ ] **Step 1: Demonstrate that the current release layout omits the helper**

Run:

```bash
package_dir="$(mktemp -d "${TMPDIR:-/tmp}/tokenlive-package-plan.XXXXXX")"
SKIP_WEB=1 VERSION=plan-test OUT_DIR="$package_dir" ./scripts/package-release.sh
test -x "$package_dir/libexec/install-brew-config.sh"
```

Expected: the final `test` command fails because `libexec/install-brew-config.sh` is absent.

- [ ] **Step 2: Package the helper**

Update the directory creation and config copy block in `scripts/package-release.sh`:

```bash
rm -rf "$OUT_DIR"
mkdir -p \
  "$OUT_DIR/bin" \
  "$OUT_DIR/libexec" \
  "$OUT_DIR/share/tokenlive/admin" \
  "$OUT_DIR/share/tokenlive/web" \
  "$OUT_DIR/etc/tokenlive"
```

Use this config and helper copy block:

```bash
rsync -a "$ROOT/configs/admin/" "$OUT_DIR/share/tokenlive/admin/"
cp "$ROOT/config/brew.yml" "$OUT_DIR/etc/tokenlive/config.yml"
cp "$ROOT/config/all-in-one.example.yml" "$OUT_DIR/etc/tokenlive/config.example.yml"
install -m 755 "$ROOT/scripts/install-brew-config.sh" "$OUT_DIR/libexec/install-brew-config.sh"
```

- [ ] **Step 3: Replace the local install heuristic**

Replace lines 51-55 of `scripts/brew-install-local.sh` with:

```bash
mkdir -p "$PREFIX/etc/tokenlive" "$PREFIX/var/tokenlive" "$PREFIX/var/log" "$PREFIX/share"
"$ROOT/scripts/install-brew-config.sh" \
  "$STAGE/etc/tokenlive/config.yml" \
  "$PREFIX/etc/tokenlive"
install -m 644 "$STAGE/etc/tokenlive/config.example.yml" "$PREFIX/etc/tokenlive/config.example.yml"
```

- [ ] **Step 4: Verify package and local installer syntax**

Run:

```bash
bash -n scripts/package-release.sh
bash -n scripts/brew-install-local.sh
package_dir="$(mktemp -d "${TMPDIR:-/tmp}/tokenlive-package-verify.XXXXXX")"
SKIP_WEB=1 VERSION=plan-test OUT_DIR="$package_dir" ./scripts/package-release.sh
test -x "$package_dir/libexec/install-brew-config.sh"
cmp scripts/install-brew-config.sh "$package_dir/libexec/install-brew-config.sh"
```

Expected: all commands exit `0`; the package contains an executable byte-identical helper.

- [ ] **Step 5: Commit packaging integration**

```bash
git add scripts/package-release.sh scripts/brew-install-local.sh
git commit -m "feat: use safe config updates in Homebrew packaging"
```

---

### Task 3: Canonical Prebuilt Homebrew Formula

**Files:**
- Modify: `packaging/homebrew/tokenlive.rb:14-88`

**Interfaces:**
- Consumes: release tarball directories `bin`, `share`, `etc`, and `libexec`.
- Produces: installed binary/assets plus helper-managed `etc/tokenlive/config.yml` and `config.yml.default`.

- [ ] **Step 1: Verify the current canonical Formula does not match the release tarball**

Run:

```bash
rg -n 'scripts/package-release.sh|stage/etc/tokenlive/config.yml' packaging/homebrew/tokenlive.rb
```

Expected: matches show that the Formula tries to rebuild and read a `stage` directory that is not present in published prebuilt tarballs.

- [ ] **Step 2: Convert the Formula install block to the prebuilt layout**

Keep the metadata and dual-architecture URL blocks, remove the incompatible
source-only `head` declaration and build dependencies, and replace `def install`
with:

```ruby
def install
  bin.install "bin/tokenlive"
  (pkgshare/"admin").install Dir["share/tokenlive/admin/*"]
  (pkgshare/"web").mkpath
  (pkgshare/"web").install Dir["share/tokenlive/web/*"] if Dir["share/tokenlive/web/*"].any?
  libexec.install "libexec/install-brew-config.sh"

  (etc/"tokenlive").mkpath
  system "bash",
         libexec/"install-brew-config.sh",
         buildpath/"etc/tokenlive/config.yml",
         etc/"tokenlive"

  rm_f etc/"tokenlive/config.example.yml"
  (etc/"tokenlive").install "etc/tokenlive/config.example.yml"
  (var/"tokenlive").mkpath
end
```

Remove:

```ruby
head "https://github.com/tokenlive/tokenlive-standalone.git", branch: "master"

depends_on "go" => :build
depends_on "node" => :build
depends_on "rsync" => :build
```

- [ ] **Step 3: Document the two installed config files in Formula caveats**

Use:

```ruby
def caveats
  <<~EOS
    Start:
      brew services start tokenlive
      # or: tokenlive

    Logs & Status:
      tokenlive logs
      tokenlive logs -f
      tokenlive logs -e
      tokenlive logs --check
      tokenlive status

    Open http://127.0.0.1:2525 — login admin / admin
    Active config: #{etc}/tokenlive/config.yml
    Latest default: #{etc}/tokenlive/config.yml.default

    Modified active configs are preserved during upgrades. Compare with:
      diff #{etc}/tokenlive/config.yml #{etc}/tokenlive/config.yml.default
  EOS
end
```

- [ ] **Step 4: Validate Formula syntax and style**

Run:

```bash
ruby -c packaging/homebrew/tokenlive.rb
brew style packaging/homebrew/tokenlive.rb
```

Expected: `Syntax OK`; Homebrew style exits `0`.

- [ ] **Step 5: Commit the canonical Formula**

```bash
git add packaging/homebrew/tokenlive.rb
git commit -m "feat: preserve modified config in Homebrew formula"
```

---

### Task 4: Publish the Canonical Formula to the Tap

**Files:**
- Create: `scripts/update_homebrew_formula_test.py`
- Create: `scripts/update_homebrew_formula.py`
- Modify: `scripts/publish-brew-release.sh:203-254`

**Interfaces:**
- Consumes: canonical `packaging/homebrew/tokenlive.rb` plus version, arm64/amd64 URLs, and SHA-256 values.
- Produces: a tap Formula with release metadata changed and all config-upgrade behavior preserved.

- [ ] **Step 1: Write the failing Formula updater test**

Create `scripts/update_homebrew_formula_test.py`:

```python
import pathlib
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
UPDATER = ROOT / "scripts" / "update_homebrew_formula.py"
CANONICAL = ROOT / "packaging" / "homebrew" / "tokenlive.rb"


class UpdateHomebrewFormulaTest(unittest.TestCase):
    def test_updates_release_metadata_and_preserves_install_logic(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            formula = pathlib.Path(temp_dir) / "tokenlive.rb"
            formula.write_text(CANONICAL.read_text())

            subprocess.run(
                [
                    sys.executable,
                    str(UPDATER),
                    "--formula",
                    str(formula),
                    "--version",
                    "9.8.7",
                    "--arm64-url",
                    "https://example.test/tokenlive-arm64.tar.gz",
                    "--arm64-sha256",
                    "a" * 64,
                    "--amd64-url",
                    "https://example.test/tokenlive-amd64.tar.gz",
                    "--amd64-sha256",
                    "b" * 64,
                ],
                check=True,
            )

            text = formula.read_text()
            self.assertIn('version "9.8.7"', text)
            self.assertIn('url "https://example.test/tokenlive-arm64.tar.gz"', text)
            self.assertIn('sha256 "' + ("a" * 64) + '"', text)
            self.assertIn('url "https://example.test/tokenlive-amd64.tar.gz"', text)
            self.assertIn('sha256 "' + ("b" * 64) + '"', text)
            self.assertIn('libexec/"install-brew-config.sh"', text)
            self.assertIn("config.yml.default", text)


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
python3 -m unittest scripts/update_homebrew_formula_test.py
```

Expected: error because `scripts/update_homebrew_formula.py` does not exist.

- [ ] **Step 3: Implement the deterministic Formula updater**

Create `scripts/update_homebrew_formula.py`:

```python
#!/usr/bin/env python3
import argparse
import pathlib
import re


def replace_one(text: str, pattern: str, replacement: str, label: str) -> str:
    updated, count = re.subn(pattern, replacement, text, count=1)
    if count != 1:
        raise SystemExit(f"failed to update {label}: expected 1 match, got {count}")
    return updated


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--formula", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--arm64-url", required=True)
    parser.add_argument("--arm64-sha256", required=True)
    parser.add_argument("--amd64-url", required=True)
    parser.add_argument("--amd64-sha256", required=True)
    args = parser.parse_args()

    path = pathlib.Path(args.formula)
    text = path.read_text()
    text = replace_one(
        text,
        r'version\s+"[^"]+"',
        f'version "{args.version}"',
        "version",
    )
    text = replace_one(
        text,
        r'(if\s+Hardware::CPU\.intel\?\s*\n\s*url\s+)"[^"]+"(\s*\n\s*sha256\s+)"[^"]+"',
        rf'\1"{args.amd64_url}"\2"{args.amd64_sha256}"',
        "amd64 release",
    )
    text = replace_one(
        text,
        r'(else\s*\n\s*url\s+)"[^"]+"(\s*\n\s*sha256\s+)"[^"]+"',
        rf'\1"{args.arm64_url}"\2"{args.arm64_sha256}"',
        "arm64 release",
    )
    path.write_text(text)


if __name__ == "__main__":
    main()
```

Make it executable:

```bash
chmod +x scripts/update_homebrew_formula.py
```

- [ ] **Step 4: Replace inline tap patching with canonical Formula synchronization**

In `scripts/publish-brew-release.sh`, after confirming `FORMULA` exists, replace the inline Python heredoc with:

```bash
cp "$ROOT/packaging/homebrew/tokenlive.rb" "$FORMULA"

python3 "$ROOT/scripts/update_homebrew_formula.py" \
  --formula "$FORMULA" \
  --version "$VERSION" \
  --arm64-url "$ASSET_URL_ARM64" \
  --arm64-sha256 "$SHA256_ARM64" \
  --amd64-url "$ASSET_URL_AMD64" \
  --amd64-sha256 "$SHA256_AMD64"

cat "$FORMULA"
```

This copy must occur before release metadata replacement so structural Formula changes reach the tap.

- [ ] **Step 5: Run updater, shell, and Formula validation**

Run:

```bash
python3 -m unittest scripts/update_homebrew_formula_test.py
python3 -m py_compile scripts/update_homebrew_formula.py
bash -n scripts/publish-brew-release.sh
ruby -c packaging/homebrew/tokenlive.rb
```

Expected: unittest passes; the remaining commands exit `0`; Ruby prints `Syntax OK`.

- [ ] **Step 6: Commit release publishing support**

```bash
git add \
  scripts/update_homebrew_formula.py \
  scripts/update_homebrew_formula_test.py \
  scripts/publish-brew-release.sh
git commit -m "build: publish canonical Homebrew formula"
```

---

### Task 5: Documentation and End-to-End Verification

**Files:**
- Modify: `docs/homebrew.md:61-70`

**Interfaces:**
- Consumes: completed installer, package, Formula, and publisher behavior.
- Produces: user-facing upgrade and recovery instructions.

- [ ] **Step 1: Document the active and baseline files**

Extend the paths table in `docs/homebrew.md`:

```markdown
| 用途 | 路径 |
|------|------|
| 二进制 | `$(brew --prefix)/bin/tokenlive` |
| 活动配置 | `$(brew --prefix)/etc/tokenlive/config.yml` |
| 最新默认配置 | `$(brew --prefix)/etc/tokenlive/config.yml.default` |
| 数据 | `$(brew --prefix)/var/tokenlive` |
| Admin | `$(brew --prefix)/share/tokenlive/admin` |
| SPA | `$(brew --prefix)/share/tokenlive/web` |
| 服务日志 | `$(brew --prefix)/var/log/tokenlive.log` |
```

- [ ] **Step 2: Add upgrade behavior and comparison instructions**

Add:

````markdown
### 配置升级规则

- 如果活动配置与上一版默认配置完全一致，升级会自动安装新版默认配置。
- 如果活动配置有任何修改（包括注释和空白），升级会保留活动配置。
- 新版默认配置始终写入 `config.yml.default`，可手动比较：

```bash
diff "$(brew --prefix)/etc/tokenlive/config.yml" \
     "$(brew --prefix)/etc/tokenlive/config.yml.default"
```

首次使用该升级机制且缺少 `config.yml.default` 时，安装程序会保留现有活动配置，
只建立新的默认配置基准。
````

- [ ] **Step 3: Run the complete verification suite**

Run:

```bash
make test
python3 -m unittest scripts/update_homebrew_formula_test.py
bash -n \
  scripts/install-brew-config.sh \
  scripts/install-brew-config_test.sh \
  scripts/package-release.sh \
  scripts/brew-install-local.sh \
  scripts/publish-brew-release.sh
ruby -c packaging/homebrew/tokenlive.rb
brew style packaging/homebrew/tokenlive.rb
git diff --check
```

Expected: all tests pass; Ruby prints `Syntax OK`; no shell syntax, style, or whitespace errors.

- [ ] **Step 4: Verify the release artifact layout**

Run:

```bash
package_dir="$(mktemp -d "${TMPDIR:-/tmp}/tokenlive-package-final.XXXXXX")"
SKIP_WEB=1 VERSION=plan-final OUT_DIR="$package_dir" ./scripts/package-release.sh
test -x "$package_dir/bin/tokenlive"
test -x "$package_dir/libexec/install-brew-config.sh"
test -f "$package_dir/etc/tokenlive/config.yml"
test -f "$package_dir/etc/tokenlive/config.example.yml"
```

Expected: all four artifact checks exit `0`.

- [ ] **Step 5: Review the diff without staging the user's config change**

Run:

```bash
git status --short
git diff -- \
  Makefile \
  scripts/install-brew-config.sh \
  scripts/install-brew-config_test.sh \
  scripts/package-release.sh \
  scripts/brew-install-local.sh \
  packaging/homebrew/tokenlive.rb \
  scripts/update_homebrew_formula.py \
  scripts/update_homebrew_formula_test.py \
  scripts/publish-brew-release.sh \
  docs/homebrew.md
```

Expected: `config/brew.yml` remains modified but unstaged and absent from the scoped diff.

- [ ] **Step 6: Commit documentation**

```bash
git add docs/homebrew.md
git commit -m "docs: explain Homebrew config upgrades"
```
