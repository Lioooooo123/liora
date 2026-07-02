#!/usr/bin/env sh
set -eu

ARCHIVE="${1:-}"
if [ -z "$ARCHIVE" ]; then
  echo "usage: scripts/release-supply-chain-audit.sh dist/liora_<version>_<goos>_<goarch>.tar.gz" >&2
  exit 2
fi
if [ ! -f "$ARCHIVE" ]; then
  echo "archive not found: $ARCHIVE" >&2
  exit 2
fi

ARCHIVE_DIR=$(CDPATH= cd -- "$(dirname -- "$ARCHIVE")" && pwd)
ARCHIVE_BASENAME="$(basename "$ARCHIVE")"
ARCHIVE="$ARCHIVE_DIR/$ARCHIVE_BASENAME"
CHECKSUM="$ARCHIVE.sha256"
PROVENANCE="$ARCHIVE.provenance.json"
SBOM="$ARCHIVE.sbom.json"
MANIFEST_REVIEW="$ARCHIVE.manifest-review.json"

if [ ! -f "$CHECKSUM" ]; then
  echo "checksum not found: $CHECKSUM" >&2
  exit 1
fi

(
  cd "$ARCHIVE_DIR"
  shasum -a 256 -c "$ARCHIVE_BASENAME.sha256" >/dev/null
)

python3 - "$ARCHIVE" "$CHECKSUM" "$PROVENANCE" "$SBOM" "$MANIFEST_REVIEW" <<'PY'
import json
import os
import re
import sys

archive, checksum_path, provenance_path, sbom_path, manifest_path = sys.argv[1:]
archive_name = os.path.basename(archive)


def fail(message):
    print(message, file=sys.stderr)
    sys.exit(1)


def load_json(path, label):
    if not os.path.isfile(path):
        fail(f"{label} not found: {path}")
    try:
        with open(path, encoding="utf-8") as handle:
            return json.load(handle)
    except Exception as exc:
        fail(f"malformed {label}: {exc}")


provenance = load_json(provenance_path, "provenance")
sbom = load_json(sbom_path, "dependency inventory")
manifest = load_json(manifest_path, "manifest review")

for field in ("package_name", "version", "git_commit", "archive", "sha256"):
    if not isinstance(provenance.get(field), str) or not provenance[field]:
        fail(f"provenance is missing {field}")

if provenance["archive"] != archive_name:
    fail("provenance archive mismatch")

with open(checksum_path, encoding="utf-8") as handle:
    checksum_fields = handle.read().strip().split()
if len(checksum_fields) < 2:
    fail("checksum file is malformed")
if checksum_fields[0] != provenance["sha256"]:
    fail("provenance checksum mismatch")
if checksum_fields[1] != archive_name and os.path.basename(checksum_fields[1]) != archive_name:
    fail("checksum archive mismatch")
if not re.fullmatch(r"[0-9a-fA-F]{64}", provenance["sha256"]):
    fail("provenance checksum is not sha256")

common_fields = ("package_name", "version", "git_commit")
for label, doc in (("dependency inventory", sbom), ("manifest review", manifest)):
    for field in common_fields:
        if doc.get(field) != provenance[field]:
            fail(f"{label} {field} mismatch")

go_modules = sbom.get("go_modules")
if not isinstance(go_modules, list) or not go_modules:
    fail("dependency inventory is empty")
for module in go_modules:
    if not isinstance(module, dict) or not module.get("path"):
        fail("dependency inventory contains invalid module")

if manifest.get("verdict") != "pass":
    fail("manifest review verdict is not pass")

for server in manifest.get("mcp_servers", []):
    if not isinstance(server, dict):
        fail("manifest review contains invalid MCP server")
    name = server.get("name") or "<unknown>"
    if server.get("network_enabled") is True and server.get("approved") is not True:
        fail(f"unsafe MCP server requires approval: {name}")

writable_absolute_prefixes = ("/tmp/", "/var/tmp/", "/private/tmp/")
for hook in manifest.get("hooks", []):
    if not isinstance(hook, dict):
        fail("manifest review contains invalid hook")
    command = str(hook.get("command", "")).strip()
    if command.startswith(writable_absolute_prefixes):
        fail(f"unsafe hook command: {command}")

print(f"release supply-chain audit ok: {archive_name}")
PY
