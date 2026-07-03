#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
WORKSPACE="${1:-$TMP_DIR/workspace}"
GO_TOOLCHAIN="${GOTOOLCHAIN:-local}"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

PROFILE_CATALOG='{"cheap":{"provider":"deepseek","model":"deepseek-chat","profile":"budget","base_url":"https://profiles.example.test/v1","api_key":"cheap-secret"},"strong":{"provider":"anthropic","model":"claude-sonnet-4","profile":"strong","api_key":"strong-secret"}}'

mkdir -p "$TMP_DIR/home" "$WORKSPACE"
(
  cd "$ROOT"
  GOTOOLCHAIN="$GO_TOOLCHAIN" go build -o "$TMP_DIR/liora" ./apps/cli
)

DOCTOR_OUT="$TMP_DIR/doctor.out"
LIORA_HOME="$TMP_DIR/home" \
LIORA_LLM_PROVIDER=openai-chat \
LIORA_LLM_API_KEY=diagnostic-secret \
LIORA_LLM_MODEL=gpt-5 \
LIORA_LLM_PROFILES="$PROFILE_CATALOG" \
"$TMP_DIR/liora" -doctor >"$DOCTOR_OUT"

grep -q 'profile_catalog: configured' "$DOCTOR_OUT"
grep -q 'profile_catalog.cheap:' "$DOCTOR_OUT"
grep -q 'profile_catalog.strong:' "$DOCTOR_OUT"
grep -q 'capability.native_tool_use: true' "$DOCTOR_OUT"
grep -q 'capability.streaming: true' "$DOCTOR_OUT"
grep -q 'credential.api_key: configured redacted=true' "$DOCTOR_OUT"
if grep -qE 'diagnostic-secret|cheap-secret|strong-secret' "$DOCTOR_OUT"; then
  echo "doctor diagnostics leaked a raw API key" >&2
  exit 1
fi

TUI_OUT="$TMP_DIR/tui.out"
printf '/tools\n/config\n/exit\n' | \
LIORA_HOME="$TMP_DIR/home" \
LIORA_LLM_PROVIDER=openai-chat \
LIORA_LLM_API_KEY=diagnostic-secret \
LIORA_LLM_MODEL=gpt-5 \
LIORA_LLM_PROFILES="$PROFILE_CATALOG" \
"$TMP_DIR/liora" -workspace "$WORKSPACE" -interactive >"$TUI_OUT"

grep -q 'Built-in tools' "$TUI_OUT"
grep -q 'access=read:path(path)' "$TUI_OUT"
grep -q 'access=write:path(path)' "$TUI_OUT"
grep -q 'access=exclusive:workspace' "$TUI_OUT"
grep -q 'profile_catalog: configured' "$TUI_OUT"
grep -q 'capability.native_tool_use: true' "$TUI_OUT"
grep -q 'capability.streaming: true' "$TUI_OUT"
grep -q 'credential.api_key: configured redacted=true' "$TUI_OUT"
if grep -qE 'diagnostic-secret|cheap-secret|strong-secret' "$TUI_OUT"; then
  echo "TUI diagnostics leaked a raw API key" >&2
  exit 1
fi

cat "$DOCTOR_OUT"
cat "$TUI_OUT"
echo "diagnostics smoke ok: $WORKSPACE"
