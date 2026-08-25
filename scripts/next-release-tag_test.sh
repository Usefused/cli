#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

# new_test_repository creates an isolated deterministic Git history for one release-calculation scenario.
new_test_repository() {
  local name="$1"
  test_repository="$test_root/$name"
  git init -q -b main "$test_repository"
  git -C "$test_repository" config user.name "Release Test"
  git -C "$test_repository" config user.email "release-test@example.com"
  git -C "$test_repository" commit -q --allow-empty -m "chore: initialize"
}

# assert_output requires one exact key-value line so failures identify the incorrect release decision directly.
assert_output() {
  local output="$1" expected="$2"
  # Partial substring matches could let one malformed output line satisfy another expected key.
  if ! grep -Fxq "$expected" <<< "$output"; then
    printf 'missing %q in output:\n%s\n' "$expected" "$output" >&2
    exit 1
  fi
}

# calculate runs the production helper inside the current isolated repository.
calculate() {
  (cd "$test_repository" && "$script_dir/next-release-tag.sh")
}

# A higher tag on another branch must beat the nearest reachable tag that caused the original collision.
new_test_repository divergent-tag
git -C "$test_repository" tag v0.15.0
git -C "$test_repository" switch -q -c released-line
git -C "$test_repository" commit -q --allow-empty -m "fix: released elsewhere"
git -C "$test_repository" tag v0.16.0
git -C "$test_repository" switch -q main
git -C "$test_repository" commit -q --allow-empty -m "feat: next capability"
output="$(calculate)"
assert_output "$output" "latest_tag=v0.16.0"
assert_output "$output" "new_tag=v0.17.0"
assert_output "$output" "bump=1"

# A repository without tags must establish a deterministic patch baseline.
new_test_repository no-tags
output="$(calculate)"
assert_output "$output" "latest_tag=v0.0.0"
assert_output "$output" "new_tag=v0.0.1"

# A conventional breaking footer must override a patch-looking subject.
new_test_repository breaking-footer
git -C "$test_repository" tag v2.3.4
git -C "$test_repository" commit -q --allow-empty -m "fix: adjust response" -m "BREAKING CHANGE: response shape changed"
output="$(calculate)"
assert_output "$output" "new_tag=v3.0.0"
assert_output "$output" "bump=2"

# A rerun on the globally latest tagged commit must be idempotent.
new_test_repository tagged-head
git -C "$test_repository" tag v1.2.3
output="$(calculate)"
assert_output "$output" "new_tag=v1.2.3"
assert_output "$output" "already_tagged=true"

printf 'next-release-tag tests passed\n'
