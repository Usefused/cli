#!/usr/bin/env bash

set -euo pipefail

mod_file="${1:-go.mod}"

# A missing module file should fail before setup-go receives an empty or misleading version.
if [[ ! -f "$mod_file" ]]; then
  printf 'Go module file not found: %s\n' "$mod_file" >&2
  exit 1
fi

version="$(awk '$1 == "toolchain" && $2 ~ /^go[0-9]+\.[0-9]+\.[0-9]+$/ { sub(/^go/, "", $2); print $2; exit }' "$mod_file")"

# Requiring a strict toolchain directive keeps local, CI, and release builds on one explicit compiler.
if [[ -z "$version" ]]; then
  printf '%s must declare toolchain goMAJOR.MINOR.PATCH\n' "$mod_file" >&2
  exit 1
fi

printf '%s\n' "$version"
