#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
INSTALL_DIR="${LIORA_INSTALL_DIR:-$HOME/.local/bin}"
BIN_PATH="$INSTALL_DIR/liora"
CONFIG_DIR="$HOME/.config/liora"

mkdir -p "$INSTALL_DIR"
cd "$PROJECT_DIR"
go build -o "$BIN_PATH" ./cmd/coding-agent

if [ -f "$PROJECT_DIR/.env.local" ] && [ ! -f "$CONFIG_DIR/.env" ]; then
  mkdir -p "$CONFIG_DIR"
  cp "$PROJECT_DIR/.env.local" "$CONFIG_DIR/.env"
  chmod 600 "$CONFIG_DIR/.env"
  printf 'Copied local API config to %s\n' "$CONFIG_DIR/.env"
fi

printf 'Installed Liora to %s\n' "$BIN_PATH"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    printf 'Add this to your shell profile if needed:\n'
    printf '  export PATH="%s:$PATH"\n' "$INSTALL_DIR"
    ;;
esac
