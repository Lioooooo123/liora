#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
VERSION="${LIORA_VERSION:-$(git -C "$PROJECT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)}"
GOOS_TARGET="${GOOS:-$(go env GOOS)}"
GOARCH_TARGET="${GOARCH:-$(go env GOARCH)}"
DIST_DIR="${LIORA_DIST_DIR:-$PROJECT_DIR/dist}"
PACKAGE_NAME="liora_${VERSION}_${GOOS_TARGET}_${GOARCH_TARGET}"
STAGE_DIR="$DIST_DIR/$PACKAGE_NAME"
ARCHIVE="$DIST_DIR/$PACKAGE_NAME.tar.gz"

rm -rf "$STAGE_DIR" "$ARCHIVE" "$ARCHIVE.sha256"
mkdir -p "$STAGE_DIR/bin" "$STAGE_DIR/docs"

cd "$PROJECT_DIR"
GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" go build \
  -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$STAGE_DIR/bin/liora" \
  ./apps/cli

cp "$PROJECT_DIR/README.md" "$STAGE_DIR/README.md"
cp "$PROJECT_DIR/docs/release.md" "$STAGE_DIR/docs/release.md"
cp "$PROJECT_DIR/docs/mvp-exit-benchmark.md" "$STAGE_DIR/docs/mvp-exit-benchmark.md"
cp "$PROJECT_DIR/docs/v0.1-exit-audit.md" "$STAGE_DIR/docs/v0.1-exit-audit.md"

cat >"$STAGE_DIR/install.sh" <<'INSTALL'
#!/usr/bin/env sh
set -eu

PACKAGE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
INSTALL_DIR="${LIORA_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$INSTALL_DIR"
cp "$PACKAGE_DIR/bin/liora" "$INSTALL_DIR/liora"
chmod 755 "$INSTALL_DIR/liora"
printf 'Installed Liora to %s\n' "$INSTALL_DIR/liora"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    printf 'Add this to your shell profile if needed:\n'
    printf '  export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    ;;
esac
"$INSTALL_DIR/liora" -version
INSTALL
chmod 755 "$STAGE_DIR/install.sh"

(
  cd "$DIST_DIR"
  tar -czf "$ARCHIVE" "$PACKAGE_NAME"
  shasum -a 256 "$(basename "$ARCHIVE")" >"$(basename "$ARCHIVE").sha256"
)

printf '%s\n' "$ARCHIVE"
