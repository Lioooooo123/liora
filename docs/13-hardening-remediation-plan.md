# Liora Hardening & Remediation Plan

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax for tracking. Execute
> phase-by-phase; every code change is TDD (failing test first) and each task ends
> with `GOTOOLCHAIN=local go test ./...` green before commit.

**Goal:** Fix the concrete correctness, security, and release-engineering defects surfaced
by the 2026-07-11 four-dimension audit, restoring the guarantees the README already claims.

**Architecture:** Small, verifiable, independently-committable fixes. No large refactor lands
in this pass — god-file splits, a DTO protocol package, versioned migrations, cross-process DB
leasing, and the multi-platform release matrix are documented in "Deferred" with rationale so
they are tracked, not lost.

**Tech Stack:** Go 1.24 (core, daemon, task, store, tools, sandbox), TypeScript/vitest
(`packages/protocol`), GitHub Actions, SQLite (modernc.org/sqlite).

---

## Audit source

Findings come from four parallel audits on 2026-07-11 (security, architecture, robustness,
testing). Severity IDs below reference those reports. The build was **red** at audit time
(`internal/daemon` test failing) with **no CI** to catch it.

## Priority phases

| Phase | Theme | Findings addressed | Land this pass? |
|-------|-------|--------------------|-----------------|
| P0 | Green build + CI | test-fail, no-CI, release-runs-subset | ✅ yes |
| P1 | Correctness / concurrency | C1 cancel-race, C2 event-cap | ✅ yes |
| P2 | Security (restore README guarantees) | M1 symlink, H2 workspace, M3 MCP env, L4 trace perms | ✅ yes |
| P3 | Daemon robustness | H1 no-timeout / no-graceful-shutdown | ✅ yes |
| P4 | Docs & hygiene | stale README, root `pnpm test`, `internal/trust` 0% | ✅ yes |
| D | Deferred (large/risky) | C3 two-daemon lease, H5 DB-handle, god-files, DTO pkg, versioned migrations, release matrix | ❌ tracked only |

---

## Phase 0 — Green build + CI

### Task 0.1: Fix `/v1/schedules//trigger` empty-id routing (405 → 404)

Root cause: `handleSchedule` (`internal/daemon/server_schedule.go:69-70`) does
`strings.Trim(path, "/")` which drops the *leading* empty segment, so `//trigger` parses as
resource id `"trigger"` and a POST falls through to `handleScheduleResource` → 405. The test
`TestServerScheduleTriggerRejectsDisabledUnknownAndMalformed/missing_id`
(`server_automation_test.go:268`) wants 404. It currently only passes under go1.24 mux path
cleaning; the `GOTOOLCHAIN=local` pin in every audit script hides the failure on newer Go.

**Files:**
- Modify: `internal/daemon/server_schedule.go:69-70`
- Test: `internal/daemon/server_automation_test.go:268` (already exists — it is the failing test)

- [x] **Step 1: Confirm the failure** — `go test ./internal/daemon/ -run TestServerScheduleTriggerRejectsDisabledUnknownAndMalformed` → FAIL `missing_id` (405, want 404).
- [x] **Step 2: Fix parsing** — preserve leading empty segment, tolerate one trailing slash:

```go
path := strings.TrimPrefix(r.URL.Path, "/v1/schedules/")
path = strings.TrimSuffix(path, "/")
parts := strings.Split(path, "/")
```

`//trigger` → `["", "trigger"]` → `parts[0] == ""` → the existing `len(parts) != 2 || parts[0] == ""` branch returns 404. `abc/` still resolves as resource `abc`.
- [x] **Step 3: Re-run** the test → PASS, then `go test ./internal/daemon/` → PASS.
- [x] **Step 4: Commit** `fix(daemon): return 404 for empty schedule id path segment`.

### Task 0.2: Add PR/push CI workflow

No workflow runs tests on PRs; `release.yml` tests only 5 of 22 packages.

**Files:**
- Create: `.github/workflows/ci.yml`

- [x] **Step 1:** Add `ci.yml` triggering on `pull_request` and `push` (all branches), one `macos-14` job:
  - `actions/setup-go@v6` with `go-version-file: go.mod`
  - `go build ./...`
  - `go vet ./...`
  - `gofmt -l` gate (fail if any file unformatted)
  - `go test -count=1 ./...`
  - `actions/setup-node` + `corepack` + `pnpm install --frozen-lockfile` + `pnpm test:protocol`
- [x] **Step 2:** Validate YAML locally (`python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))"`).
- [x] **Step 3: Commit** `ci: run full go + protocol test suite on push and PR`.

### Task 0.3: Make release run the full suite

`release.yml:45` tests a subset — the failing package (`internal/daemon`) is excluded.

**Files:** Modify `.github/workflows/release.yml:44-45`

- [x] **Step 1:** Replace the subset line with `go test -count=1 ./...` and add `go vet ./...`.
- [x] **Step 2: Commit** `ci(release): run full test suite before publishing`.

---

## Phase 1 — Correctness / concurrency

### Task 1.1: Make terminal status transitions race-safe (C1)

`UpdateStatus` (`internal/task/store.go:1905`) and `Cancel` (`store.go:1986`) are unconditional
`UPDATE`s, so a cancelled task can be flipped back to completed/failed and vice-versa. Guard so
that once a task reaches a truly-terminal state (`completed`, `failed`, `cancelled`, `stale`) no
later write overwrites it. `lost`/`recovered` stay mutable (legitimate recovery path via
`RecoverLostTask`).

**Files:**
- Modify: `internal/task/store.go` (`UpdateStatus`, `Cancel`)
- Test: `internal/task/store_test.go` (new cases)

- [x] **Step 1: Failing tests** in `store_test.go`:
  - `TestUpdateStatusDoesNotOverwriteTerminal`: create task, `Cancel(...)`, then `UpdateStatus(StatusCompleted)`; assert `Get(...).Status == cancelled`.
  - `TestCancelDoesNotOverwriteCompleted`: create task, `UpdateStatus(StatusCompleted)`, then `Cancel(...)`; assert status stays `completed`.
- [x] **Step 2: Run** → FAIL (status flips).
- [x] **Step 3: Implement** — add the guard to both writes:

```go
// UpdateStatus
UPDATE tasks
SET status = ?, updated_at = ?, completed_at = COALESCE(?, completed_at)
WHERE id = ? AND status NOT IN ('completed','failed','cancelled','stale')
```

Same `AND status NOT IN ('completed','failed','cancelled','stale')` clause on the `UPDATE`
inside `Cancel`'s transaction. In `Cancel`, if `RowsAffected() == 0` the task is already final:
roll back (do not append a spurious `task.cancelled` event) and return nil so cancel is
idempotent. Keep the terminal set as a shared `var terminalStatuses` / helper to stay DRY.
- [x] **Step 4: Run** the new tests + `go test ./internal/task/ ./internal/daemon/` → PASS. If any existing test relied on overwriting a terminal state, inspect — it is asserting the bug.
- [x] **Step 5: Commit** `fix(task): guard terminal status writes against races`.

### Task 1.2: Read the newest events, not the oldest 1000 (C2)

`Events` (`store.go:2253`) caps at 1000 ascending from seq 0; consumers scan backward over that
*oldest* window, so on tasks with >1000 events (every long streamed answer) the newest
approval/input/diff/expiry events are invisible → `/input` & `/approval` 409 forever, `/diff`
404, expiry missed.

**Files:**
- Modify: `internal/task/store.go` (add `LatestEvents`)
- Modify consumers: `internal/daemon/server_queue.go:433` (`latestWaitingRequest`),
  `internal/daemon/server.go:1368` (`handleTaskDiff`), `internal/task/queue.go:107`
  (`LatestUserInput`), `internal/task/queue.go:524` (`latestExpiry`),
  `internal/daemon/server.go:682` (`backgroundTaskOutput`)
- Test: `internal/task/store_test.go`, `internal/daemon/server_test.go`

- [x] **Step 1: Failing test** `TestLatestEventsReturnsNewest`: append 1200 events, then a
  final `user_input.requested`; assert a helper that finds the latest waiting request sees it.
  Also `TestTaskDiffAfter1000Events`: append >1000 deltas then a diff event; `GET /diff` → 200.
- [x] **Step 2: Run** → FAIL.
- [x] **Step 3: Implement** `LatestEvents` (newest-N, returned ascending so existing
  backward-scan logic is unchanged):

```go
func (r *Repository) LatestEvents(ctx context.Context, taskID string, limit int) ([]Event, error) {
    if limit <= 0 || limit > 1000 {
        limit = 1000
    }
    rows, err := r.db.QueryContext(ctx, `
        SELECT rowid, id, task_id, type, payload_json, created_at
        FROM task_events
        WHERE task_id = ?
        ORDER BY rowid DESC
        LIMIT ?
    `, taskID, limit)
    // scan, then reverse the slice to ascending order before returning
}
```

  Point each consumer at `LatestEvents` instead of `Events`.
- [x] **Step 4: Run** new + `go test ./internal/task/ ./internal/daemon/` → PASS.
- [x] **Step 5: Commit** `fix(task): scan newest events for approval/diff/expiry lookups`.

---

## Phase 2 — Security (restore README guarantees)

### Task 2.1: Resolve symlinks so workspace confinement actually holds (M1)

`Workspace.resolve` (`internal/tools/workspace.go:610`) and `apply.resolve`
(`internal/apply/patch.go:165`) are purely lexical — a symlink *inside* the workspace pointing
to `/` lets reads/writes/patches escape, contradicting README "防止路径穿越".

**Files:**
- Modify: `internal/tools/workspace.go:610-629`, `internal/apply/patch.go:165-182`
- Test: `internal/tools/workspace_test.go`, `internal/apply/patch_test.go`

- [x] **Step 1: Failing tests** — create workspace with `evil -> <outside dir>` symlink; assert
  `read evil/secret`, `write evil/x`, and a patch targeting `evil/x` all error with "outside
  workspace" (skip on `runtime.GOOS == "windows"`).
- [x] **Step 2: Run** → FAIL (escape succeeds).
- [x] **Step 3: Implement** — after the lexical check, resolve the parent directory's real path
  (`filepath.EvalSymlinks` on the deepest existing ancestor, since the leaf may not exist yet)
  and re-verify it is still within `EvalSymlinks(root)`. Reject if the real path escapes.
  Share the logic via one helper used by both packages (keep in each package if cross-import is
  undesirable — duplicate the ~15-line helper rather than create a cycle).
- [x] **Step 4: Run** new + `go test ./internal/tools/ ./internal/apply/` → PASS.
- [x] **Step 5: Commit** `fix(tools,apply): reject symlink escapes from workspace`.

### Task 2.2: Validate the task workspace path (H2)

`Repository.Create` (`internal/task/store.go:73`) only rejects an empty workspace. Require an
absolute, cleaned, existing directory. `os.Stat` must follow and validate a workspace-root
symlink, while the caller's cleaned spelling remains the shared workspace identity used by task,
session, memory, and rule records. (A configurable allowlist root is deferred.)

**Files:** Modify `internal/task/store.go:73-76`; Test `internal/task/store_test.go`

- [x] **Step 1: Failing test** `TestCreateRejectsNonAbsoluteWorkspace` (relative path → error)
  and `TestCreateRejectsMissingWorkspace` (nonexistent dir → error).
- [x] **Step 2: Run** → FAIL.
- [x] **Step 3: Implement** — require `filepath.IsAbs(workspace)`; `filepath.Clean`; `os.Stat`
  must succeed and report a directory. Return a clear error otherwise. Preserve the cleaned
  spelling so all persistence layers continue to use the same workspace identity. (Tests that
  pass `t.TempDir()` already satisfy this; fix fake or missing fixture paths to use real dirs.)
- [x] **Step 4: Run** `go test ./internal/task/ ./internal/daemon/` → PASS.
- [x] **Step 5: Commit** `fix(task): require absolute existing workspace directory`.

### Task 2.3: Stop leaking secrets into MCP subprocesses (M3)

`internal/mcp/session.go:41` passes `os.Environ()` — every MCP server gets `LIORA_LLM_API_KEY`,
`LIORA_DAEMON_TOKEN`, etc. Pass a filtered environment: inherit non-secret vars, drop known
secret keys, then apply the server's explicit config env.

**Files:** Modify `internal/mcp/session.go:40-44`; Test `internal/mcp/*_test.go`

- [x] **Step 1: Failing test** — start a fake MCP server that echoes its env; assert
  `LIORA_LLM_API_KEY`/`OPENAI_API_KEY`/`LIORA_DAEMON_TOKEN` are absent but `PATH` present and
  configured env applied.
- [x] **Step 2: Run** → FAIL.
- [x] **Step 3: Implement** — build env by filtering `os.Environ()`, excluding keys matching
  `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `LIORA_LLM_*`, `ANTHROPIC_*`, `OPENAI_*`, `GEMINI_*`,
  `DEEPSEEK_*` (case-insensitive), then append config env.
- [x] **Step 4: Run** `go test ./internal/mcp/` → PASS.
- [x] **Step 5: Commit** `fix(mcp): filter secrets from MCP subprocess environment`.

### Task 2.4: Write trace JSONL with owner-only perms (L4)

`internal/trace/jsonl.go:10,13` creates dirs 0755 / files 0644; traces can contain secrets.

**Files:** Modify `internal/trace/jsonl.go:10-13`; Test `internal/trace/jsonl_test.go`

- [x] **Step 1: Failing test** — write a trace, assert `os.Stat(...).Mode().Perm() == 0o600`.
- [x] **Step 2: Run** → FAIL.
- [x] **Step 3: Implement** — `MkdirAll(..., 0o700)`; replace `os.Create` with
  `os.OpenFile(path, O_WRONLY|O_CREATE|O_TRUNC, 0o600)`.
- [x] **Step 4: Run** → PASS.
- [x] **Step 5: Commit** `fix(trace): restrict trace file permissions to owner`.

---

## Phase 3 — Daemon robustness

### Task 3.1: HTTP timeouts + graceful shutdown for `-daemon` (H1)

`apps/cli/main.go:141` uses `http.ListenAndServe` with a zero-value server (no timeouts, no
signal handling). Standalone daemon can't drain and has slowloris exposure.

**Files:** Modify `apps/cli/main.go` (daemon-mode block ~126-145); Test `apps/cli/main_test.go`

- [x] **Step 1: Failing/behavioral test** — assert the daemon-mode server is constructed with a
  non-zero `ReadHeaderTimeout` (extract server construction into a small testable helper
  returning `*http.Server`).
- [x] **Step 2: Run** → FAIL.
- [x] **Step 3: Implement** — build `&http.Server{Addr, Handler, ReadHeaderTimeout: 10s,
  IdleTimeout: 120s}`; run `ListenAndServe` in a goroutine; `signal.NotifyContext` on
  SIGINT/SIGTERM; on signal call `server.Shutdown(ctx)` with a 5s deadline. Mirror the
  `ReadHeaderTimeout` onto the embedded daemon server (`main.go:305`).
- [x] **Step 4: Run** `go test ./apps/cli/` → PASS; manual: `liora -daemon` + Ctrl-C exits cleanly.
- [x] **Step 5: Commit** `fix(daemon): add HTTP timeouts and graceful shutdown`.

---

## Phase 4 — Docs & hygiene

### Task 4.1: Fix stale README claim + root pnpm test + trust coverage

- [x] **Step 1:** README:584 — replace "尚未实现本地 token 鉴权" with the real state
  (`-daemon-token` + `requireCapability` exist; default is no token). Note the embedded-daemon
  default has no token.
- [x] **Step 2:** `package.json` — add `"test": "pnpm --filter @liora/protocol test"` so root
  `pnpm test` stops failing with `ERR_PNPM_NO_SCRIPT`.
- [x] **Step 3:** Add `internal/trust/trust_test.go` covering `NormalizeSource`,
  `IsTrustedSource`, `IsUntrustedSource`, `LevelForSource` (0% → covered).
- [x] **Step 4: Run** `go test ./internal/trust/` + `pnpm test` → PASS.
- [x] **Step 5: Commit** `docs,test: correct README auth note, wire root pnpm test, cover trust`.

---

## Deferred (tracked, not done this pass) — with rationale

- **C3 — two daemons over one SQLite (lease/ownership).** Correct fix needs a cross-process
  lock/lease and rework of `recoverRestartState` to not reap another live daemon's tasks. High
  blast radius; needs its own design. *Mitigation now:* documented; embedded-daemon-by-default
  already the common path. Follow-up: single-writer lock file or advisory lease in `store`.
- **H5 — `Store.OpenDB()` re-opens + replays DDL per call.** Caching a single handle changes
  connection lifecycle across ~26 call sites and interacts with the task `Repository`'s own
  connection; risky without a dedicated migration/handle-ownership refactor. Real perf/lock
  cost, but not a correctness regression in single-daemon use.
- **Versioned migrations (M9).** Depends on H5's handle work; convert the idempotent DDL replay
  into `user_version`-gated steps.
- **God-file splits** (`tuisession/daemon_submitter.go` 2586L, `task/store.go` 2422L),
  **DTO protocol package** (stop serving `store`/`task` structs as the wire API), **table-driven
  command registry**, **real architecture-guard via import graph.** Pure structure; large diffs,
  no behavior change; schedule as focused follow-ups.
- **Multi-platform release matrix (darwin/amd64, linux, windows).** `package-release.sh` already
  cross-compiles; only the workflow matrix + per-platform smoke are missing. Additive; do after
  CI is trusted.
- **coding-eval labeling (deterministic, not a model eval), grep-style script tests.** Doc/test
  hygiene; low risk, batch later.
- **LLM: 60s stream cap (H4), unbounded tool-loop context (M5), 429 jitter/Retry-After (M6),
  multi-hunk patch apply (M1-apply).** Real robustness gaps; each needs its own test harness.

---

## Definition of done for this pass

- `GOTOOLCHAIN=local go test ./...` **and** plain `go test ./...` (current Go) both green.
- `go vet ./...` clean, `gofmt -l` empty.
- `pnpm test:protocol` and root `pnpm test` green.
- CI workflow present and green on the branch.
- Each task committed separately with a passing suite at every commit.
