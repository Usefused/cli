#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

printf 'module example.com/test\n\ngo 8.7.0\n\ntoolchain go9.8.7\n' > "$test_root/valid.mod"
actual="$("$script_dir/go-toolchain-version.sh" "$test_root/valid.mod")"
# A valid directive must lose only Go's required prefix when passed to setup-go.
if [[ "$actual" != "9.8.7" ]]; then
  printf 'expected parsed toolchain 9.8.7, got %s\n' "$actual" >&2
  exit 1
fi

printf 'module example.com/test\n\ngo 1.25.0\n' > "$test_root/missing.mod"
# Missing declarations must fail instead of silently falling back to the runner toolchain.
if "$script_dir/go-toolchain-version.sh" "$test_root/missing.mod" >/dev/null 2>&1; then
  printf 'expected a missing toolchain directive to fail\n' >&2
  exit 1
fi

printf 'module example.com/test\n\ngo 1.25.0\n\ntoolchain latest\n' > "$test_root/malformed.mod"
# Malformed declarations must fail before setup-go receives a non-reproducible selector.
if "$script_dir/go-toolchain-version.sh" "$test_root/malformed.mod" >/dev/null 2>&1; then
  printf 'expected a malformed toolchain directive to fail\n' >&2
  exit 1
fi

printf 'go-toolchain-version tests passed\n'
