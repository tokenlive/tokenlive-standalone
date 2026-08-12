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
