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

## 2026-06-25 Release Packaging

- 新增 `scripts/package-release.sh`，产物为 `dist/liora_<version>_<goos>_<goarch>.tar.gz`，包含 `bin/liora`、`install.sh`、README 和 MVP benchmark。
- CLI 新增 `-version`，打包时通过 `-ldflags "-X main.version=<version>"` 注入版本，方便发布包 smoke 和用户快速确认安装结果。
- 新增 `scripts/release-smoke.sh`，会解包发布包、安装到临时目录并运行 `liora -version`。这个 smoke 只验证包可安装可启动，不验证用户自己的 LLM API key。

## 2026-06-25 Step Argument Parsing

- 修复 LLM 生成 `assignment\ question.pdf` 或 `"course notes.txt"` 时路径被按空格拆散的问题，文件类工具现在支持反斜杠转义和单/双引号参数。
- `run` 和 `mcp` 不能使用同一套去引号逻辑：`run` 需要保留 shell 命令原文，`mcp` 需要保留 JSON 参数中的双引号，因此 parser 按工具类型处理后续参数。

## 2026-06-25 TUI / Agent Capability Parity

- 用户要求参考 `kimi-code` 和 `claude-code-main` 复刻 TUI 与 coding agent 主能力。当前没有直接全量重写 TUI，而是先抽出 `internal/capabilities` 作为工具能力注册表，避免 planner、TUI、未来客户端各自维护一套工具说明。
- 第一阶段将 `/tools` 接入 runtime，planner 的 allowed tools 也改为从注册表生成，并新增 `GET /v1/capabilities` 给未来客户端读取。这样用户问“你能干嘛”、模型规划工具步骤、后续客户端展示能力清单时可以共享同一份来源。
- 参考结论已写入 `docs/tui-agent-parity-plan.md`。后续真正重写 TUI 时应优先接 daemon/SSE 事件流和取消能力，而不是只做视觉美化；否则仍会出现同步阻塞、长输出卡顿和客户端无法复用的问题。

## 2026-06-25 Daemon Client SDK

- 新增 `internal/daemonclient`，作为 TUI、CLI 扩展和未来 Mac 客户端复用 daemon API 的统一入口。它负责 HTTP 状态码、JSON decode、SSE 解析和 API error 包装，避免 UI 层各自拼接 `/v1/tasks/...` 路径。
- client 当前覆盖 health、capabilities、create/list/get task、events、stream events、diff、apply、cancel。下一步做流式 TUI 时应依赖这个包，而不是继续直接调用 in-process runtime。
- SSE parser 会把 `event:` 与 `data:` 转成 `StreamEvent`。daemon 目前的错误帧只携带字符串，因此 client 将 `task.error` 帧包装成错误返回；如果后续 daemon 将错误也标准化成 `task.Event`，这里需要同步更新解析逻辑。

## 2026-06-26 Daemon-backed TUI

- 新增 `tui.StreamingSubmitter` 和 `internal/tuisession.DaemonSubmitter`，TUI 可以通过 daemon 创建 async task，并随着 SSE 事件即时渲染 `Status / Plan / Tool / Tools / Summary / Diff`。这样后续 Mac 客户端和 TUI 共用 daemon/task/event 主链路。
- `internal/tui` 不再直接依赖 daemonclient 或 task 包，避免 `runtime -> tui` 与 `tui -> task -> runtime` 的 import cycle；daemon 适配逻辑放在 `internal/tuisession`。
- CLI 新增 `-tui-daemon`，连接 `-daemon-addr` 指向的已运行 daemon。当前不会自动拉起 daemon，也还不能在任务运行中通过输入 `/cancel` 取消；这两个能力留给后续阶段。
- 修正 `daemonclient` SSE 解析：daemon 的 SSE `data:` 是事件 payload JSON，不是完整 task event。client 现在用 `event:` 填 type、`id:` 填 id、`data:` 填 payload，避免 UI 层拿不到 payload。

## 2026-06-26 TUI Cancel and Apply Commands

- `internal/tuisession.DaemonSubmitter` 现在记录 current task、last task 和 last diff，并实现 `HandleCommand` 支持 `/cancel` 与 `/apply`。CLI 在 `-tui-daemon` 模式下使用 `tui.CommandChain{daemonSession, turnRuntime}`，先处理 daemon session 命令，再回退到 runtime 命令。
- `/cancel` 会调用 daemon cancel API 并停止当前 running task；主动取消不再作为 TUI 错误返回，避免用户取消后额外显示 Error。
- `/apply` 会优先使用最近一次 stream 中保存的 diff，必要时回查 daemon diff API，再调用 apply API。patch-mode 下任务完成不会直接改真实 workspace，用户输入 `/apply` 后才落盘。
- 当前 line-based TUI 在 task 运行期间仍阻塞在 `SubmitStream`，所以“运行中输入 `/cancel`”还不是完整交互体验；全屏 Bubble Tea/异步输入层需要继续把这个 command 能力绑定到快捷键或并发输入。

## 2026-06-26 Daemon Task History Commands

- `internal/tuisession.DaemonSubmitter` 新增 `/tasks`、`/last`、`/resume <task_id>`，直接通过 daemonclient 查询 task 列表和 event 历史，并把 task/event 回放成文本时间线。
- `/last` 与 `/resume` 会把回放任务记录为 last task，并记住最近 diff，因此用户重启 TUI 后可以先 `/last` 或 `/resume <task_id>`，再 `/apply` 最近 diff。
- 这一步只是 task/event 级恢复，不是完整多轮 session transcript。后续如果要对齐 Claude Code/Kimi Code 的 session resume，需要补 session/message 数据模型和更好的 transcript 渲染。

## 2026-06-26 Session Transcript Baseline

- SQLite 新增 `sessions` 与 `session_messages`，`tasks` 增加 `session_id`。迁移采用 `ALTER TABLE ... ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`，兼容用户已有本地 `liora.db`。
- `Repository.Create` 现在会在一个事务内创建或复用 session、插入 task、写入用户 message，并更新 session 的 `last_task_id`。这样 daemon API、TUI 和未来 Mac 客户端都能围绕同一条会话主线恢复上下文。
- daemon 新增 `GET/POST /v1/sessions`、`GET /v1/sessions/{id}`、`GET /v1/sessions/{id}/messages`、`GET /v1/sessions/{id}/tasks`，并同步扩展 `internal/daemonclient` SDK。
- daemon-backed TUI 新增 `/sessions`、`/session`、`/resume-session <session_id>`。两轮以上输入会自动复用同一个 session；新 TUI 进程可通过 `/resume-session` 重新绑定旧 session。
- 当前 transcript 只持久化用户消息，assistant summary/tool event 仍在 task_events 中。这个拆分能满足 v0.1 恢复和客户端复用，但后续若要做完整聊天历史搜索，应把 assistant answer、tool timeline projection 或 transcript snapshot 也写入 session 层。

## 2026-06-26 Permission Prompt Baseline

- 新增 `internal/permission`，通过 `LIORA_PERMISSION=prompt` 启用审批策略；默认仍是 `auto`，避免破坏脚本模式和本地快速开发。
- prompt 模式下，危险 shell、非 patch mode 写操作、MCP 外部调用会在执行前返回 permission required，task runner 将任务置为 `waiting_user` 并写入 `permission.requested` 事件。
- daemon 新增 `POST /v1/tasks/{id}/approval`，`approve` 会给 task 写入 `approval_granted` 并重新异步运行该 task，`deny` 会把 task 取消并写入 `permission.denied` 事件。
- daemon-backed TUI 新增 `/approve`、`/deny` 命令，处理最近 task 的审批。当前 line-based TUI 只能显示批准/拒绝结果，批准后的继续执行在 daemon 中进行；更自然的“批准后继续流式展示”需要全屏异步 TUI 的命令通道。
- 当前授权粒度是 task 级，一旦批准，该 task 后续需要审批的步骤都会继续执行。这是为了用最小实现先打通 daemon/session/task/event/API 结构；后续 Mac 客户端或 Bubble Tea TUI 应升级为逐步授权队列。
