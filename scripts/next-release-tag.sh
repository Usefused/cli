#!/usr/bin/env bash

set -euo pipefail

# select_highest_semver_tag chooses the first strict release tag from Git's version-sorted input without depending on commit ancestry.
select_highest_semver_tag() {
  local candidate selected=""
  while IFS= read -r candidate; do
    # Pre-release, build-metadata, and non-version tags cannot become the numeric bump baseline.
    if [[ -z "$selected" && "$candidate" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      selected="$candidate"
    fi
  done
  printf '%s' "$selected"
}

# conventional_bump returns the strongest release level present in commit subjects or bodies since the selected release.
conventional_bump() {
  local latest_tag="$1" has_release_tag="$2" bump=0 message
  local -a log_args=(--format=%s%n%b)
  # A real release tag defines the comparison set even when that tag lives on a divergent historical branch.
  if [[ "$has_release_tag" == "true" ]]; then
    log_args=("${latest_tag}..HEAD" "${log_args[@]}")
  fi
  while IFS= read -r message; do
    case "$message" in
      *"BREAKING CHANGE"*|*"BREAKING-CHANGE"*|*!:*)
        bump=2
        break
        ;;
      feat\(*\):*|feat:*)
        # A feature raises patch to minor but cannot weaken a previously detected major bump.
        if [[ "$bump" -lt 1 ]]; then
          bump=1
        fi
        ;;
    esac
  done < <(git log "${log_args[@]}")
  printf '%s' "$bump"
}

# bump_semver applies one validated conventional-commit level to a strict vMAJOR.MINOR.PATCH tag.
bump_semver() {
  local latest_tag="$1" bump="$2"
  local version="${latest_tag#v}" major minor patch
  IFS=. read -r major minor patch <<< "$version"
  case "$bump" in
    2) major=$((major + 1)); minor=0; patch=0 ;;
    1) minor=$((minor + 1)); patch=0 ;;
    0) patch=$((patch + 1)) ;;
    *) printf 'unsupported release bump %s\n' "$bump" >&2; return 1 ;;
  esac
  printf 'v%s.%s.%s' "$major" "$minor" "$patch"
}

latest_tag="$(git tag --list 'v*' --sort=-version:refname | select_highest_semver_tag)"
head_tag="$(git tag --points-at HEAD --list 'v*' --sort=-version:refname | select_highest_semver_tag)"

# A main-branch rerun may reuse its current release only when that release is still globally latest.
if [[ -n "$head_tag" ]]; then
  if [[ -n "$latest_tag" && "$head_tag" != "$latest_tag" ]]; then
    printf 'HEAD has stale release tag %s while latest is %s\n' "$head_tag" "$latest_tag" >&2
    exit 1
  fi
  printf 'latest_tag=%s\nnew_tag=%s\nbump=none\nalready_tagged=true\n' "$head_tag" "$head_tag"
  exit 0
fi

has_release_tag=true
# A repository without releases starts from the explicit zero version and includes all commits in bump detection.
if [[ -z "$latest_tag" ]]; then
  latest_tag="v0.0.0"
  has_release_tag=false
fi

bump="$(conventional_bump "$latest_tag" "$has_release_tag")"
new_tag="$(bump_semver "$latest_tag" "$bump")"
# An occupied result now means a concurrent or stale release view, so fail before Git emits an opaque tag-creation error.
if git show-ref --verify --quiet "refs/tags/$new_tag"; then
  printf 'calculated release tag %s already exists; fetch tags and rerun\n' "$new_tag" >&2
  exit 1
fi

printf 'latest_tag=%s\nnew_tag=%s\nbump=%s\nalready_tagged=false\n' "$latest_tag" "$new_tag" "$bump"
