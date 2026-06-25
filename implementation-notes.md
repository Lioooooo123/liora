# implementation-notes

## 2026-06-25 Core Daemon

- 产品方向已收敛为本地 Mac 任务工坊，能力和性能优先，UI 作为吸引入口但不能牺牲执行稳定性。
- 本阶段只实现 v0.1 Core Daemon：SQLite 任务持久化、本地 HTTP API、SSE 事件流和 CLI daemon 入口。
- Docker sandbox、SwiftUI 客户端、白板闪念、审批 UI、MCP 长连接池均暂不实现，避免雏形阶段过早扩散。
- Daemon 不依赖 TUI。后续 TUI 或 SwiftUI 应消费任务 API，而不是反向让 daemon 依赖界面层。
- SQLite 继续使用现有 `liora.db`，这符合本地友好的产品定位，也便于后续记忆、任务和白板统一查询。
- 当前 daemon 默认不做鉴权，只适合本机开发使用。正式桌面端版本应补本地 token、Unix socket 或仅 localhost 绑定策略。

## 2026-06-25 Sandbox Executor

- 新增 `internal/sandbox`，把 Shell 执行抽象成 `Executor`，目前支持 `local` 和 `docker` 两种实现。
- 默认仍为 `local`，原因是本地开发和 CI 不一定安装 Docker；通过 `LIORA_SANDBOX=docker` 可切换 Docker。
- Docker executor 使用 `docker run --rm --network none --memory --cpus -v <workspace>:/workspace -w /workspace <image> sh -lc <command>`。
- 当前只把 `run` 工具切到 sandbox executor；`read/write/edit` 等文件工具仍在宿主进程执行，但已有 workspace 路径限制。
- task runner 会在含 `run` 步骤的任务中写入 `sandbox.run` 事件，保证用户能看到当前 shell executor 模式。
- 后续要把 Docker 从可配置能力升级为默认策略，还需要补产物 apply、危险命令审批、实时 SSE、容器清理观测和资源上限配置 UI。

## 2026-06-25 Patch Apply API

- 新增 `internal/apply`，支持生成和应用基础 unified patch，并校验 patch 路径不能越过 workspace。
- daemon 新增 `GET /v1/tasks/{id}/diff` 和 `POST /v1/tasks/{id}/apply`。
- `/apply` 会写入 `task.patch_applied` 事件，方便未来客户端在任务时间线中展示用户确认后的真实写入。
- 当前 apply API 是显式调用，不会自动应用 sandbox 产物；后续要把文件写入默认迁移为“sandbox 产出 patch，用户确认后 apply”。

## 2026-06-25 Task Patch Mode

- 新增 `LIORA_PATCH_MODE=1`，daemon task runner 会把 workspace 复制到临时目录，在副本中执行 agent 文件写入，然后把 diff 作为 `task.diff` 事件回传。
- patch mode 下真实 workspace 不会被任务执行阶段直接修改，必须通过 `/v1/tasks/{id}/apply` 显式应用 patch。
- 当前 patch mode 复制时跳过 `.git`、`node_modules` 和 `vendor`，这是为了控制本地性能；后续需要把跳过规则做成配置并在 UI 中解释。
- 这一步还不是完整 Docker sandbox apply：Docker 只覆盖 `run` 工具，文件工具是在临时副本中执行。下一步应把 Docker 临时 workspace 和 patch mode 合并成统一任务工作目录。
- workspace 准备逻辑已下沉到 `internal/sandbox.PrepareWorkspace`，task runner 只选择 `direct/copy` 模式，并写入 `sandbox.workspace` 事件给未来客户端展示。

## 2026-06-25 Live Event Stream

- `/v1/tasks/{id}/events/stream` 从一次性输出改成轮询 SQLite 事件表，按 event id 去重输出，直到看到 `task.completed`、`task.cancelled` 或 `task.error`。
- 当前实现是轻量轮询，优点是简单稳定、无需引入消息总线；后续任务量上来后可以替换为内存 pub/sub 或 SQLite update hook。

## 2026-06-25 Task Cancel API

- 新增 `POST /v1/tasks/{id}/cancel`，会把任务状态更新为 `cancelled` 并写入 `task.cancelled` 事件。
- 初始版本只做持久化状态和事件层取消；随后已补充当前 daemon 进程内运行任务的真实 context 取消，详见下一节。

## 2026-06-25 Running Task Cancellation

- daemon 新增运行中任务注册表，只保存当前进程内 `run_async` 任务的 cancel func；`/cancel` 先落库 `cancelled`，再触发运行中 context 取消。
- runner 在发现 context 已取消且任务状态已经是 `cancelled` 时，不再把终态覆盖为 `failed` 或 `completed`。
- 这个设计先解决本地 MVP 最重要的“能停住当前任务”；daemon 重启后无法恢复旧进程的 cancel func，因此重启前启动的任务只能做状态层取消。

## 2026-06-25 Process Group Cancellation

- 本地 sandbox 的 shell 执行从 `exec.CommandContext` 改为显式 `Start/Wait`，并在 Unix 平台为每条命令创建独立 process group。
- context 取消或超时时会 `SIGKILL` 整个 process group，避免 `sh -c` 下的后台子进程继续运行并污染 workspace。
- 非 Unix 平台暂时回退为杀主进程；当前产品优先面向 macOS，本阶段先保证 mac 本地体验可靠。

## 2026-06-25 Event Stream Notification

- `Repository.AppendEvent` 新增同进程内存通知，SSE 事件流优先等待通知，不再每 100ms 固定轮询 SQLite。
- 通知订阅是一次性的 channel，并提供 unsubscribe，避免事件流长连接在高频事件下堆积 goroutine。
- 保留 5 秒 fallback 轮询，原因是未来可能出现另一个进程直接写同一个 SQLite DB；这类跨进程写入不会触发当前进程内存通知。

## 2026-06-25 Live Script Tool Events

- script task 不再用 `MemoryRecorder` 等任务结束后批量写工具事件；新增 task 层实时 recorder，每条工具执行完成后立即写入 `tool.call` 和 `tool.result`。
- 实时 recorder 第一次收到工具事件时把任务状态更新为 `running`，这样客户端能更早展示任务已经进入执行阶段。
- 初始改动先覆盖无 LLM 的 script task；natural task 的实时 plan/tool 事件随后已补齐，详见下一节。

## 2026-06-25 Live Natural Task Events

- runtime 新增 `SubmitOptions`，允许调用方注入 recorder 和 plan hook；原 `Submit` / `SubmitWithRecorder` 保持兼容。
- natural task 现在会在 planner 产出步骤后立刻写 `task.plan_ready`，随后工具事件通过同一个实时 recorder 落库。
- 这样 daemon/SSE 客户端可以按顺序看到 planning、plan ready、tool call/result，而不是等自然语言任务全部结束后一次性刷新。

## 2026-06-25 Incremental Event Cursor

- task event 对外新增 `seq` 字段，当前实现使用 SQLite `rowid` 作为单库内递增游标，避免为旧数据库重建 `task_events` 表。
- 新增 `EventsAfter(taskID, afterSeq, limit)`，SSE 事件流用 `lastSeq` 增量读取，不再每次唤醒扫描该任务全部历史事件。
- 这个选择提升长任务事件流性能，但 `seq` 只保证当前 SQLite 数据库内稳定递增；如果未来做跨库同步，需要引入显式全局 event sequence。

## 2026-06-25 MVP Exit Benchmark

- 新增 `docs/mvp-exit-benchmark.md`，把当前长期目标收敛为 v0.1 能力底座验收标准。
- 结束标准强调任务能力、实时事件、SQLite 持久化、patch/apply、cancel、sandbox 基线和可验证 smoke；精致 Mac App、角色系统、白板完整形态和 Docker 默认化进入 v0.2+。
- 后续是否结束当前目标，应按该文档逐项验收，而不是继续做开放式优化。

## 2026-06-25 Minimal Action Guidance

- TUI 在展示 diff 后新增 `Next` 区块，提示用户先 review diff，再通过 daemon API apply 或 cancel。
- 这不是完整桌面确认 UI，只是 v0.1 的最低可用引导；真正的按钮式 apply/cancel 应由未来 Mac 客户端消费 daemon API 实现。
- README 中过期的“SSE 后续扩展实时订阅”描述已改为当前真实状态：同进程通知 + 增量游标 + 低频 fallback。
