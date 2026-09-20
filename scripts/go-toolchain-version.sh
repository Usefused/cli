#!/usr/bin/env bash

set -euo pipefail

mod_file="${1:-go.mod}"

# A missing module file should fail before setup-go receives an empty or misleading version.
if [[ ! -f "$mod_file" ]]; then
  printf 'Go module file not found: %s\n' "$mod_file" >&2
  exit 1
fi

selector="$(awk '$1 == "toolchain" { print $2; exit }' "$mod_file")"

# Go removes a redundant toolchain directive when the go directive already names that exact patched compiler.
if [[ -z "$selector" ]]; then
  selector="go$(awk '$1 == "go" { print $2; exit }' "$mod_file")"
fi

# Both supported declarations must pin a complete compiler version; malformed explicit toolchains never fall back.
if [[ ! "$selector" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  printf '%s must declare an exact toolchain or go version (MAJOR.MINOR.PATCH)\n' "$mod_file" >&2
  exit 1
fi

printf '%s\n' "${selector#go}"
