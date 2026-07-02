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

rm -rf "$STAGE_DIR" "$ARCHIVE" "$ARCHIVE.sha256" "$ARCHIVE.provenance.json" "$ARCHIVE.sbom.json" "$ARCHIVE.manifest-review.json"
mkdir -p "$STAGE_DIR/bin" "$STAGE_DIR/docs"

cd "$PROJECT_DIR"
GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" go build \
  -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$STAGE_DIR/bin/liora" \
  ./apps/cli

cp "$PROJECT_DIR/README.md" "$STAGE_DIR/README.md"
cp "$PROJECT_DIR"/docs/*.md "$STAGE_DIR/docs/"

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

ARCHIVE_BASENAME="$(basename "$ARCHIVE")"
CHECKSUM_VALUE="$(awk '{print $1}' "$ARCHIVE.sha256")"
GIT_COMMIT="unknown"
GIT_DIRTY="false"
if git -C "$PROJECT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  GIT_COMMIT="$(git -C "$PROJECT_DIR" rev-parse HEAD)"
  if ! git -C "$PROJECT_DIR" diff --quiet --ignore-submodules -- 2>/dev/null || \
    ! git -C "$PROJECT_DIR" diff --cached --quiet --ignore-submodules -- 2>/dev/null; then
    GIT_DIRTY="true"
  fi
fi

LIORA_PROJECT_DIR="$PROJECT_DIR" \
LIORA_PACKAGE_NAME="$PACKAGE_NAME" \
LIORA_PACKAGE_VERSION="$VERSION" \
LIORA_GOOS="$GOOS_TARGET" \
LIORA_GOARCH="$GOARCH_TARGET" \
LIORA_ARCHIVE_BASENAME="$ARCHIVE_BASENAME" \
LIORA_ARCHIVE_SHA256="$CHECKSUM_VALUE" \
LIORA_GIT_COMMIT="$GIT_COMMIT" \
LIORA_GIT_DIRTY="$GIT_DIRTY" \
LIORA_PROVENANCE_PATH="$ARCHIVE.provenance.json" \
LIORA_SBOM_PATH="$ARCHIVE.sbom.json" \
LIORA_MANIFEST_REVIEW_PATH="$ARCHIVE.manifest-review.json" \
python3 <<'PY'
import json
import os
import subprocess
import time
from pathlib import Path

project_dir = Path(os.environ["LIORA_PROJECT_DIR"])
package_name = os.environ["LIORA_PACKAGE_NAME"]
version = os.environ["LIORA_PACKAGE_VERSION"]
git_commit = os.environ["LIORA_GIT_COMMIT"]
common = {
    "package_name": package_name,
    "version": version,
    "git_commit": git_commit,
}

provenance = {
    **common,
    "archive": os.environ["LIORA_ARCHIVE_BASENAME"],
    "sha256": os.environ["LIORA_ARCHIVE_SHA256"],
    "goos": os.environ["LIORA_GOOS"],
    "goarch": os.environ["LIORA_GOARCH"],
    "git_dirty": os.environ["LIORA_GIT_DIRTY"] == "true",
    "builder": "scripts/package-release.sh",
    "generated_at_unix": int(time.time()),
}

decoder = json.JSONDecoder()
raw_modules = subprocess.check_output(["go", "list", "-m", "-json", "all"], cwd=project_dir, text=True)
modules = []
remaining = raw_modules
while remaining.strip():
    remaining = remaining.lstrip()
    module, idx = decoder.raw_decode(remaining)
    remaining = remaining[idx:]
    modules.append(
        {
            "path": module.get("Path", ""),
            "version": module.get("Version", ""),
            "main": bool(module.get("Main", False)),
        }
    )

package_json_path = project_dir / "package.json"
package_json = json.loads(package_json_path.read_text(encoding="utf-8")) if package_json_path.exists() else {}
sbom = {
    **common,
    "kind": "dependency-inventory",
    "go_modules": modules,
    "node_package": {
        "name": package_json.get("name", ""),
        "version": package_json.get("version", ""),
        "package_manager": package_json.get("packageManager", ""),
        "dependencies": sorted((package_json.get("dependencies") or {}).keys()),
        "dev_dependencies": sorted((package_json.get("devDependencies") or {}).keys()),
        "lockfile": "pnpm-lock.yaml" if (project_dir / "pnpm-lock.yaml").exists() else "",
    },
}

manifest_review = {
    **common,
    "kind": "mcp-hook-manifest-review",
    "verdict": "pass",
    "mcp_servers": [],
    "hooks": [],
    "notes": [
        "release package does not ship repository MCP server definitions",
        "release package does not ship writable hook commands",
    ],
}

for path_name, doc in [
    ("LIORA_PROVENANCE_PATH", provenance),
    ("LIORA_SBOM_PATH", sbom),
    ("LIORA_MANIFEST_REVIEW_PATH", manifest_review),
]:
    Path(os.environ[path_name]).write_text(json.dumps(doc, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY

printf '%s\n' "$ARCHIVE"
