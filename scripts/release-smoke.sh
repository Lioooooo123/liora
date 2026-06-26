#!/usr/bin/env sh
set -eu

ARCHIVE="${1:-}"
if [ -z "$ARCHIVE" ]; then
  echo "usage: scripts/release-smoke.sh dist/liora_<version>_<goos>_<goarch>.tar.gz" >&2
  exit 2
fi
if [ ! -f "$ARCHIVE" ]; then
  echo "archive not found: $ARCHIVE" >&2
  exit 2
fi

WORK_DIR=$(mktemp -d)
INSTALL_DIR="$WORK_DIR/bin"
trap 'rm -rf "$WORK_DIR"' EXIT

tar -xzf "$ARCHIVE" -C "$WORK_DIR"
PACKAGE_DIR="$(find "$WORK_DIR" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
if [ ! -x "$PACKAGE_DIR/install.sh" ]; then
  echo "package is missing executable install.sh" >&2
  exit 1
fi
for path in \
  "$PACKAGE_DIR/README.md" \
  "$PACKAGE_DIR/docs/release.md" \
  "$PACKAGE_DIR/docs/mvp-exit-benchmark.md" \
  "$PACKAGE_DIR/docs/v0.1-exit-audit.md" \
  "$PACKAGE_DIR/bin/liora"
do
  if [ ! -e "$path" ]; then
    echo "package is missing $path" >&2
    exit 1
  fi
done

LIORA_INSTALL_DIR="$INSTALL_DIR" "$PACKAGE_DIR/install.sh" >/tmp/liora-release-install.log
"$INSTALL_DIR/liora" -version | grep -q 'liora '

printf 'release smoke ok: %s\n' "$ARCHIVE"
