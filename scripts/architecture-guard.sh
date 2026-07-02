#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "${1:-$(dirname "${BASH_SOURCE[0]}")/..}" && pwd)"

require_file() {
  local file="$1"
  if [[ ! -f "$ROOT/$file" ]]; then
    echo "architecture guard missing required file: $file" >&2
    exit 1
  fi
}

require_text() {
  local file="$1"
  local text="$2"
  if ! LC_ALL=C grep -Fq "$text" "$ROOT/$file"; then
    echo "architecture guard expected $file to contain: $text" >&2
    exit 1
  fi
}

require_absent_regex() {
  local regex="$1"
  shift
  local existing=()
  local path
  for path in "$@"; do
    if [[ -e "$ROOT/$path" ]]; then
      existing+=("$ROOT/$path")
    fi
  done
  if (( ${#existing[@]} == 0 )); then
    return
  fi
  if LC_ALL=C grep -RInE "$regex" -- "${existing[@]}"; then
    echo "architecture guard found forbidden main-runtime dependency or source reference" >&2
    exit 1
  fi
}

for file in \
  go.mod \
  apps/cli/main.go \
  internal/daemon/server.go \
  internal/daemonclient/client.go \
  internal/store/store.go \
  internal/task/task.go \
  internal/tui/program.go \
  internal/tui/tui.go \
  internal/tuisession/daemon_submitter.go \
  packages/protocol/src/client.ts
do
  require_file "$file"
done

require_text go.mod "modernc.org/sqlite"
require_text go.mod "charm.land/bubbletea"
require_text apps/cli/main.go "\"github.com/Lioooooo123/liora/internal/daemon\""
require_text apps/cli/main.go "\"github.com/Lioooooo123/liora/internal/daemonclient\""
require_text apps/cli/main.go "\"github.com/Lioooooo123/liora/internal/store\""
require_text apps/cli/main.go "taskpkg \"github.com/Lioooooo123/liora/internal/task\""
require_text apps/cli/main.go "\"github.com/Lioooooo123/liora/internal/tui\""
require_text apps/cli/main.go "http.ListenAndServe(*daemonAddr, server)"
require_text apps/cli/main.go "startEmbeddedDaemon(persistentStore, planner, llmRegistry, sandboxExecutor, patchMode)"
require_text apps/cli/main.go "daemonclient.New(baseURL, clientOptions...)"
require_text apps/cli/main.go "tui.RunProgram(context.Background(), tuiConfig, daemonSession)"

require_text internal/daemon/server.go "/v1/tasks"
require_text internal/daemon/server.go "handleTasksEventStream"
require_text internal/daemonclient/client.go "StreamEvents"
require_text internal/daemonclient/client.go "Apply"
require_text internal/store/store.go "_ \"modernc.org/sqlite\""
require_text internal/tui/program.go "charm.land/bubbletea"
require_text internal/tuisession/daemon_submitter.go "\"github.com/Lioooooo123/liora/internal/daemonclient\""
require_text internal/tuisession/daemon_submitter.go "s.client.CreateTask"
require_text internal/tuisession/daemon_submitter.go "s.client.StreamEvents"
require_text packages/protocol/src/client.ts "createDaemonProtocolClient"
require_text packages/protocol/src/client.ts "parseTaskEventStream"

require_absent_regex 'agent\.New\(|runtime\.FromWorkspace\(|store\.New\(' \
  internal/tui \
  packages/protocol

require_absent_regex 'github\.com/cloudwego/eino|cloudwego/eino|github\.com/tmc/langchaingo|tmc/langchaingo|github\.com/google/adk|google/adk|LangChainGo|langchaingo' \
  go.mod \
  go.sum \
  package.json \
  pnpm-lock.yaml \
  apps \
  internal \
  packages

echo "architecture guard ok: Go core + daemon + SQLite + HTTP/SSE + patch-first + Bubble Tea remain the main chain"
