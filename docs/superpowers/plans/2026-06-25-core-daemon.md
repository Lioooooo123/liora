# Core Daemon Implementation Plan

> 执行要求：实现本计划时使用 `superpowers:executing-plans`，新增行为先写测试，再实现。

## 目标

把 Liora v0.1 从单次 CLI/TUI 执行推进到本地 Core Daemon 雏形。未来 SwiftUI 客户端、任务工坊、白板和 sandbox 都通过同一个本地任务 API 接入。

## 成功标准

- 任务与事件持久化到现有 `liora.db`。
- 支持创建任务、查询任务、列出任务、读取任务事件。
- 支持本地 HTTP API 和 SSE 事件流。
- 支持 `coding-agent -daemon -daemon-addr :18080` 启动。
- 现有 CLI/TUI 行为不被破坏。
- `GOTOOLCHAIN=local go test ./...` 通过。
- 本地 smoke 能创建任务并读取事件。

## 范围

本计划只做 v0.1 Core Daemon。明确不做 SwiftUI、Docker sandbox、白板、审批 UI、MCP 连接池和复杂 replan。

## 设计

- `internal/task`：任务领域模型、SQLite 仓储、任务 runner。
- `internal/daemon`：HTTP API、SSE 输出。
- `internal/store`：开放共享 SQLite 连接，并初始化任务表。
- `cmd/coding-agent`：增加 daemon 模式。
- `scripts`：增加 daemon smoke 脚本和测试。

HTTP API:

- `GET /healthz`
- `POST /v1/tasks`
- `GET /v1/tasks`
- `GET /v1/tasks/{id}`
- `GET /v1/tasks/{id}/events`
- `GET /v1/tasks/{id}/events/stream`

事件模型:

- `task.created`
- `task.planning`
- `task.plan_ready`
- `tool.call`
- `tool.result`
- `task.summary`
- `task.error`
- `task.completed`
- `task.cancelled`

## 实施步骤

- [x] Task 1: 新增 `internal/task/task.go`，定义任务、事件、请求响应类型。
- [x] Task 2: 修改 `internal/store/store.go`，导出 `OpenDB()`，初始化 `tasks` 与 `task_events` 表。
- [x] Task 3: 新增 `internal/task/store.go` 和测试，完成任务仓储。
- [x] Task 4: 新增 `internal/task/runner.go` 和测试，桥接现有 runtime/agent 事件。
- [x] Task 5: 新增 `internal/daemon/server.go` 和测试，完成 HTTP/SSE API。
- [x] Task 6: 修改 `cmd/coding-agent/main.go` 和测试，接入 `-daemon`。
- [x] Task 7: 更新 README、增加 smoke 脚本和测试。
- [x] Task 8: 全量测试、smoke、git diff 审查、提交。

## 风险与取舍

- 当前 runner 先复用已有 runtime，不引入 Docker。sandbox 会作为 v0.2 单独实现。
- SSE 先按任务事件表轮询输出，避免引入复杂消息总线。任务量低时足够稳定，后续可替换为订阅式事件总线。
- API 先绑定本地地址，由使用者通过 `-daemon-addr` 控制监听地址。默认不做鉴权，后续桌面端接入前需要补本地 token 或 Unix socket。
- 任务 ID 采用随机 ID，避免基于时间戳的碰撞风险。
