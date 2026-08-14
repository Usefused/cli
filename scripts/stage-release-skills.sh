#!/bin/sh
set -eu

version="${1#v}"
case "$version" in
    ""|*[!0-9A-Za-z.-]*)
        echo "invalid release version: $version" >&2
        exit 1
        ;;
esac

target="skills/$version"
if [ -d "$target" ]; then
    echo "Using existing $target."
    exit 0
fi

# A release snapshot must be immutable even when skills/dev changes later.
# Staging before the build also makes this exact copy available to go:embed.
cp -R skills/dev "$target"
echo "Staged $target from skills/dev."
