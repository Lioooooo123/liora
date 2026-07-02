# Liora Coding Agent 架构与分层规划

> 目标读者：Liora 后端、TUI 前端、未来桌面客户端的开发者与评审者。
> 本文聚焦**整体架构与分层**：三层如何切分、各层职责边界、协议契约、数据流和演进路线。
> 技术选型决策见 [tech-stack-selection.md](./tech-stack-selection.md)，逐项实现记录见 [implementation-notes.md](../implementation-notes.md)。

## 1. 背景与目标

Liora 是一个本地优先的 Coding Agent，定位为「Go 后端 + 多前端入口」。当前已有可运行的 Go core、本地 daemon、line-based TUI 和 monorepo 骨架。本规划要解决的核心问题是：

- **一套 agent core，多个入口复用**：TUI 和未来桌面客户端不得各自重写 planner、tool executor、sandbox。
- **清晰的层与契约**：UI 只消费 daemon API，core 不反向依赖任何 UI。
- **可演进**：从当前 line-based TUI 平滑过渡到 Go 原生全屏 TUI（Bubble Tea），再到桌面客户端，每一步都不破坏已通过的 `scripts/v0.1-exit-audit.sh`。

### 设计原则

1. **Core / UI 分离**：UI 不直接执行工具、不直接读写 SQLite，只通过 daemon HTTP + SSE API。
2. **单一事实来源**：任务、会话、事件、记忆、能力清单都由 daemon 持有，所有入口投影同一份状态。
3. **协议先行**：跨层交互通过显式的 HTTP/SSE + JSON 契约，沉淀到 `packages/protocol`，Go 与 TS 两侧按契约对齐而非互相 import。
4. **本地优先**：单二进制、SQLite、localhost 绑定，先服务本机单用户，再考虑多端同步与鉴权。

## 2. 总体架构

```text
┌──────────────────────────────────────────────────────────────────┐
│                          前端入口层 (UI)                             │
│                                                                    │
│  apps/cli (Go)                                    desktop (Tauri/   │
│  TTY → Bubble Tea 全屏 TUI                        SwiftUI, 未来)     │
│  非 TTY → line-based TUI (smoke/CI)                                 │
│  + 脚本/daemon 入口                                                 │
└───────────────┬──────────────────────────────────┬────────────────┘
                │                                  │
                │  HTTP + SSE (JSON 契约, 仅本地 localhost)            │
                │                                  │
┌───────────────▼──────────────────────────────────▼────────────────┐
│                       协议层 (Contract)                             │
│  packages/protocol (TS 类型 + client + SSE parser + event reducer)  │
│  internal/daemonclient (Go client SDK)                              │
│  —— 两侧不互相 import，按同一份 API contract / fixture 对齐 ——       │
└───────────────────────────────┬────────────────────────────────────┘
                                │
┌───────────────────────────────▼────────────────────────────────────┐
│                     Go Core 后端 (internal/)                         │
│                                                                      │
│  daemon  ── HTTP API + SSE 事件流 + workbench 投影                    │
│  task    ── 任务模型 / SQLite 仓储 / runner / session / timeline      │
│  runtime ── 单轮执行编排 (planner ↔ agent ↔ replan)                   │
│  agent   ── 工具步骤解析与调度                                        │
│  tools   ── workspace 内文件 / 搜索 / 目录 / shell 能力               │
│  sandbox ── shell executor 抽象 (local / docker) + workspace 准备     │
│  apply   ── unified patch 生成与应用                                  │
│  llm     ── 多供应商 client + 自然语言 planner                        │
│  mcp     ── stdio MCP client / server-tool manager                   │
│  store   ── goal / memory / skill / mcp.json 本地持久化               │
│  permission ── 审批策略 (auto / prompt)                              │
│  trace   ── 工具调用轨迹 + JSONL 落盘                                 │
│  capabilities ── 工具能力注册表 (planner / API / UI 共享)            │
└───────────────────────────────┬────────────────────────────────────┘
                                │
                  ┌─────────────▼─────────────┐
                  │   本地资源 (Local Host)     │
                  │  SQLite (liora.db)         │
                  │  workspace 文件系统         │
                  │  Docker / 子进程 / MCP server│
                  │  LLM Provider API (出网)    │
                  └────────────────────────────┘
```

三层职责一句话总结：

| 层 | 职责 | 不做 |
| --- | --- | --- |
| 前端入口层 | 渲染、输入、交互、状态投影 | 执行工具、读写 DB、调 LLM |
| 协议层 | 类型契约、client、SSE 解析、event → view model | 业务决策 |
| Go Core 后端 | 任务编排、工具执行、持久化、沙箱、权限 | 关心具体 UI 形态 |

## 3. Go Core 后端分层

后端是整套系统的稳定底座，已在 `internal/` 下成形。各模块边界与依赖方向：

### 3.1 模块职责

- **daemon** ([internal/daemon](../internal/daemon))：唯一对外暴露面。提供 HTTP API、SSE 事件流、workbench 投影、approval/cancel/apply 入口。不包含业务执行逻辑，只编排 repository + runner + store。
- **task** ([internal/task](../internal/task))：任务领域模型、SQLite 仓储（tasks / task_events / sessions / session_messages / memories）、任务 runner、session 与 timeline 投影。是状态的单一事实来源。
- **runtime** ([internal/runtime](../internal/runtime))：单轮执行编排，连接 planner 与 agent，处理 bounded replan。被 task runner 调用。
- **agent** ([internal/agent](../internal/agent))：解析受控工具步骤并调度到 tools / sandbox / mcp。
- **tools** ([internal/tools](../internal/tools))：workspace 内的文件、搜索、目录查看、shell 能力，带路径穿越防护。
- **sandbox** ([internal/sandbox](../internal/sandbox))：shell executor 抽象（local / docker），以及 patch mode 的临时 workspace 准备。
- **apply** ([internal/apply](../internal/apply))：unified patch 生成与应用，校验 patch 路径不越界。
- **llm** ([internal/llm](../internal/llm))：多供应商 provider registry + per-request resolved config + 自然语言 planner。不同 conversation thread 可以绑定不同 provider/model/profile，运行时请求必须显式携带 resolved model config，而不是依赖进程级全局可变 client。
- **mcp** ([internal/mcp](../internal/mcp))：stdio MCP client 与 server/tool manager。
- **store** ([internal/store](../internal/store))：goal / memory / skill / mcp.json 的本地持久化与 SQLite 打开。
- **permission** ([internal/permission](../internal/permission))：审批策略，控制危险 shell、非 patch 写、MCP 外部调用是否需要用户确认。
- **capabilities** ([internal/capabilities](../internal/capabilities))：工具能力注册表，planner 的 allowed tools、`/v1/capabilities`、UI 工具面板共享同一来源。
- **trace** ([internal/trace](../internal/trace))：工具调用轨迹记录与 JSONL 落盘。

### 3.2 依赖方向规则

```text
daemon → task → runtime → agent → {tools, sandbox, mcp}
daemon → store / capabilities / permission
runtime → llm
所有写入 → apply / sandbox workspace
```

强约束：

- **禁止反向依赖 UI**：`internal/*` 不得 import `apps/*`。
- **避免 import cycle**：`internal/tui` 不直接依赖 daemonclient 或 task，daemon 适配逻辑放在 `internal/tuisession`（现状已如此）。
- **模型路由显式化**：task/thread 层保存 model binding，runner 在每次 LLM 调用前解析 global default、workspace default、thread override、task override，传入 `internal/llm`；provider client 只按 resolved config 执行请求并上报 provider/model 维度的 retry、latency、token、capability。
- **入口仅做装配**：`apps/cli/main.go` 只负责参数解析、配置加载、模式选择和依赖装配，不写业务逻辑。

### 3.3 后端演进重点

- **Agent loop 升级**：从「LLM 输出文本计划 → 一次性执行」升级为多轮 tool-use loop（结构化 tool schema、观察-执行循环、bounded 自修复）。这是对标 Claude Code 的核心后端工作。
- **Sandbox 默认化**：把 Docker 从可配置能力升级为默认执行策略，统一 Docker 临时 workspace 与 patch mode 的工作目录。
- **鉴权**：daemon 当前无鉴权仅本机用；常驻/桌面场景需补本地 token、Unix socket 或 localhost-only 绑定。
- **事件结构化**：task event 与 tool call 事件进一步结构化，便于多端 UI 一致渲染。

## 4. 协议层（跨层契约）

协议层是「一套 core 多个入口」能否成立的关键。它把后端能力沉淀为语言无关的契约。

### 4.1 当前 API 面（v0.1）

```text
GET  /healthz
GET  /v1/capabilities
GET  /v1/memories            POST /v1/memories
GET  /v1/workbench
GET  /v1/timeline/search
POST /v1/tasks               GET  /v1/tasks
GET  /v1/tasks/events/stream                 # 多任务 SSE
GET  /v1/tasks/{id}          GET  /v1/tasks/{id}/events
GET  /v1/tasks/{id}/events/stream            # 单任务 SSE
GET  /v1/tasks/{id}/diff     POST /v1/tasks/{id}/apply
POST /v1/tasks/{id}/cancel   POST /v1/tasks/{id}/approval
GET  /v1/sessions            POST /v1/sessions
GET  /v1/sessions/{id}       GET  /v1/sessions/{id}/messages
GET  /v1/sessions/{id}/tasks GET  /v1/sessions/{id}/timeline
```

事件流约定：SSE frame 的 `event:` 为 task event type，`data:` 为 payload JSON；多任务流 `data` 为 `{"task_id": "...", "payload": {...}}` envelope，并用 `seq` 做增量游标。

### 4.2 packages/protocol（待建，TS）

为未来 Web/桌面 UI 提供统一入口：

- **类型定义**：task / session / event / memory / capability / approval 的 TypeScript 类型。
- **daemon client**：封装 HTTP 状态码、JSON decode、错误包装，UI 层不手拼 `/v1/...` 路径。
- **SSE parser**：把 `event:` / `id:` / `data:` 解析成 `StreamEvent`。
- **event reducer**：把 task events 规约成 UI 可直接渲染的 view model（status / plan / tools / summary / diff / approval）。

### 4.3 internal/daemonclient（已有，Go）

供 `apps/cli`、CLI 自动化和 Go 侧复用，覆盖 health、capabilities、task CRUD、单/多任务 SSE、diff、apply、cancel、approval、sessions、timeline、memories，以及后续 thread model binding / provider profile 查询与切换。

### 4.4 契约对齐策略

Go 与 TS 两侧**不互相 import**，通过共享 fixture 对齐：Go daemon 产出标准输出 fixture，TS protocol 用同一份 fixture 做 parser/reducer 测试。任一侧改契约必须同步 fixture。

## 5. 前端入口层

### 5.1 apps/cli（Go，现状）

- 提供 `liora` 二进制、脚本模式、`-daemon` 模式、embedded daemon 自动拉起。
- 交互入口按 stdin/stdout 是否为 TTY 自动选择渲染器：**TTY → Bubble Tea 全屏 TUI**（[internal/tui/program.go](../internal/tui/program.go)）；**非 TTY（管道 / CI / smoke）→ line-based TUI**（[internal/tui/tui.go](../internal/tui/tui.go)）。`LIORA_FORCE_GO_TUI` 可强制 line 渲染器用于调试。
- 装配逻辑见 [apps/cli/main.go](../apps/cli/main.go)：解析参数 → 构造 llm/store/sandbox → 启 daemon 或交互入口。

### 5.2 internal/tui（Go 原生全屏 TUI，Bubble Tea）

主力终端 UI 采用 Go 原生实现，与 core 同进程、同语言、同二进制，无额外运行时依赖。这是相对早期「Ink + React + TypeScript」设想的有意调整（决策见 [tech-stack-selection.md](./tech-stack-selection.md) 与 [implementation-notes.md](../implementation-notes.md)）。

- 基于 charmbracelet **Bubble Tea**（Elm 架构 Model/Update/View）+ **bubbles**（textinput / viewport / spinner）+ **lipgloss** 样式。
- 首屏 workbench（workspace / model / core / safety）+ 可滚动事件区 + 底部固定输入栏。
- 流式桥接：`startTurn` 起 goroutine 调 `submitter.SubmitStream`，经 buffered channel + `waitForUpdate` cmd 逐条产 `streamUpdateMsg`，保证 Bubble Tea 单 goroutine 安全。
- 命令复用 `internal/tuisession` 的 `HandleCommand`：`/help` `/cancel` `/apply` `/approve` `/deny` `/timeline` `/history` `/watch` `/spawn` 等。
- 与 line-based TUI 共享同包渲染助手与样式（`RenderWelcome` / `RenderStreamUpdate` / `helpText` 等），两套渲染器消费同一份 daemon 事件。

不做：重写 planner/executor/sandbox、直接读写 SQLite、实现桌面窗口。

### 5.3 desktop（未来客户端）

待 daemon API 与 protocol 稳定后启动，复用同一套 daemon + protocol：

- **候选 A — Tauri 2**：产品验证期优先，复用 React/Vite 栈与 protocol 类型，跨平台，本地 HTTP/Unix socket 调 Go daemon。
- **候选 B — SwiftUI**：macOS 深集成期再评估，原生窗口/菜单栏/通知体验更好，但与 TS UI 复用少。

桌面端只是「另一个入口」，承载任务工坊、记忆、白板等产品形态，绝不复制 agent core。

## 6. 数据流：一次任务的生命周期

```text
用户输入 (TUI/CLI/桌面)
   │  POST /v1/tasks {workspace, prompt, natural, run_async}
   ▼
daemon 创建 task → 事务内创建/复用 session + 写 user message + last_task_id
   │
   ▼
task runner 启动 (async 时立即返回 task_id)
   │  patch mode: sandbox.PrepareWorkspace 复制 workspace 到临时副本 → 写 sandbox.workspace
   ▼
runtime 编排:
   planner.Plan → 写 task.plan_ready
   agent 逐步执行工具 → 实时写 tool.call / tool.result (status)
   shell 步骤 → sandbox executor (local/docker) → 写 sandbox.run
   权限拦截 (prompt 模式) → waiting_user + permission.requested
   工具失败 → 最多一次 task.replanning → 第二个 plan_ready
   │
   ▼
完成 → 写 task.diff (patch mode 下真实 workspace 未改) → task.completed
   │
   ▼
客户端通过 SSE 增量消费 (按 seq 去重) → reducer → 渲染 plan/tools/summary/diff
   │
   ▼
用户 review diff → POST /v1/tasks/{id}/apply → 写 task.patch_applied → 落盘真实 workspace
   或 POST /v1/tasks/{id}/cancel → 进程内 context 取消 + 状态置 cancelled
```

关键不变量：

- **patch-first**：执行阶段不直接改真实 workspace，`/apply` 才落盘。
- **事件单调**：每个 task 的事件按 `seq` 单调递增，支持断线后增量续读。
- **状态终态唯一**：`completed` / `failed` / `cancelled` 互斥；取消优先，不被覆盖回 completed/failed。

## 7. Monorepo 组织与边界

```text
apps/
  cli/        Go CLI/TUI/daemon 入口，产物名 liora
internal/     Go core（保持当前模块边界，不下沉到 packages）
  tui/        Go 原生 TUI：Bubble Tea 全屏 + line-based fallback
packages/
  protocol/   TS 类型 + daemon client + SSE parser + event reducer (待建)
  ui/         主题 / 快捷键 / 终端 UI 约定 (待建)
  evals/      agent 回归评测 case (待建)
scripts/      smoke / install / package / exit-audit
docs/         产品与架构文档
```

工具链：`pnpm-workspace.yaml` 管 `apps/*` + `packages/*`；Go module 仍在根目录以保护 `internal/` 可见性与已通过的 smoke。

## 8. 演进路线（按阶段）

每阶段都以 `GOTOOLCHAIN=local ./scripts/v0.1-exit-audit.sh "$PWD"` 通过为底线。

| 阶段 | 目标 | 完成标准 |
| --- | --- | --- |
| **Phase 0（已达成）** | Go core 可运行、cli 可安装打包、line TUI 可 smoke、monorepo 骨架 | exit-audit 通过 |
| **Phase 1 — Protocol 包** | 新增 `packages/protocol`，抽 TS task/session/event 类型 + client + SSE parser | `pnpm test --filter @liora/protocol` 通过；与 Go daemon fixture 对齐 |
| **Phase 2 — Go 原生全屏 TUI（已达成）** | `internal/tui` 新增 Bubble Tea 全屏渲染器，TTY 自动启用，workbench + 输入栏 + 事件流 + 流式桥接 + cancel/apply/approve/deny；非 TTY 保留 line-based TUI | Bubble Tea 与 line TUI 共享 daemon 事件；exit-audit 全绿，smoke 走 line 渲染器零改动 |
| **Phase 3 — Agent 能力升级** | 文本计划 → 多轮 tool-use loop；结构化 tool schema；强化权限/sandbox/eval | 固定 coding eval case 通过率稳定；失败可解释、可恢复、可回放 |
| **Phase 4 — 桌面客户端** | 基于 Tauri 2 或 SwiftUI 做桌面入口，复用 daemon + protocol | 不复制 agent core；桌面端只是另一个入口 |

横切任务（贯穿各阶段）：daemon 鉴权、Docker 默认化、事件结构化、eval case 扩充。

## 9. 风险与约束

- **无 Node 依赖**：TUI 改为 Go 原生（Bubble Tea），与 core 同二进制，打包链路无需携带任何 Node 脚本或运行时。
- **daemon 无鉴权**：当前仅适合本机；常驻进程或桌面端前必须补 token/socket/绑定策略。
- **MCP 短连接**：当前每次 list/call 启动一次 stdio server；高频场景需长连接 session pool。
- **patch mode 并发**：默认 patch mode 下运行中 `/cancel` 与事件写入并发，已用单连接池 + `busy_timeout` 缓解；高并发客户端需更细写队列。
- **TUI 双渲染器一致性**：Bubble Tea（TTY）与 line-based（非 TTY/smoke）两套渲染器必须消费同一份 daemon 事件并保持断言串一致，smoke 始终走 line 渲染器作为基线。
- **范围收敛**：对标 Claude Code / Kimi Code 容易无限扩范围；精致桌面 App、白板、角色系统归到 v0.2+，不阻塞 v0.1 收口。

## 10. 参考

- [README.md](./README.md) — docs 入口与维护规则
- [liora-1.0-plan.md](./liora-1.0-plan.md) — 1.0 主路线
- [tech-stack-selection.md](./tech-stack-selection.md) — 技术选型决策
- [v0.1-exit-audit.md](./v0.1-exit-audit.md) — v0.1 验收矩阵
- [implementation-notes.md](../implementation-notes.md) — 逐项实现记录
