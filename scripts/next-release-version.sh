#!/usr/bin/env sh
set -eu

TAG_PATTERN='^v[0-9]+\.[0-9]+\.[0-9]+$'

head_tag="$(
  git tag --points-at HEAD --list 'v*' --sort=-v:refname 2>/dev/null |
    grep -E "$TAG_PATTERN" |
    head -n 1 || true
)"
if [ -n "$head_tag" ]; then
  printf '%s\n' "$head_tag"
  exit 0
fi

latest_tag="$(
  git tag --list 'v*' --sort=-v:refname 2>/dev/null |
    grep -E "$TAG_PATTERN" |
    head -n 1 || true
)"
if [ -z "$latest_tag" ]; then
  printf 'v0.1.0\n'
  exit 0
fi

version="${latest_tag#v}"
major="$(printf '%s' "$version" | cut -d . -f 1)"
minor="$(printf '%s' "$version" | cut -d . -f 2)"
patch="$(printf '%s' "$version" | cut -d . -f 3)"

case "$major$minor$patch" in
  *[!0-9]* | '')
    echo "invalid semver tag: $latest_tag" >&2
    exit 1
    ;;
esac

printf 'v%s.%s.%s\n' "$major" "$minor" "$((patch + 1))"
