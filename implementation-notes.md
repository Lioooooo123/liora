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

## 2026-06-26 Async Streaming TUI Commands

- `internal/tui.App` 在 submitter 支持 `StreamingSubmitter` 时会把 stdin scanner 与 task stream 拆开：任务运行时继续读取输入，只即时处理 `/cancel`，其它输入按顺序排队到当前任务结束后再执行。
- 这个改动解决了 line-based TUI 运行中无法取消的问题，同时避免管道输入里的 `/apply`、`/last`、`/exit` 抢在当前任务完成前执行。
- `DaemonSubmitter.cancelCurrent` 会短暂等待 current task id 出现，规避管道输入极快时 `/cancel` 先于 daemon task id 设置的竞态。
- 这仍不是完整 Bubble Tea 全屏 TUI；当前只是让最关键的运行中控制命令可用。后续全屏 TUI 还需要状态栏、可滚动 transcript、diff 面板和审批队列。

## 2026-06-26 Session Timeline Projection

- 新增 `Repository.Timeline` 和 `GET /v1/sessions/{id}/timeline`，把 `session_messages` 与 task events 按时间合成为客户端可直接渲染的 timeline。
- timeline 当前包含 user message、assistant summary、tool call/result、diff、approval、status。它是按需投影，不新增 materialized transcript 表，因此兼容现有 SQLite 数据并避免迁移复杂度。
- `internal/daemonclient` 新增 `SessionTimeline`，daemon-backed TUI 新增 `/timeline`/`/transcript`。未来 Mac 客户端应优先消费 timeline，而不是自己合并 messages/tasks/events。
- 当前 timeline 适合渲染和恢复，不适合全文搜索、长期摘要和 compaction；这些能力后续应落到独立 transcript/search 表。

## 2026-06-26 Daemon TUI Smoke

- 新增 `scripts/tui-smoke.sh`，用 Python 标准库启动临时 fake Chat Completions server，再启动 Core Daemon 和 `-tui-daemon` 入口。
- smoke 覆盖两条用户入口：自然语言请求的 streaming 输出 + `/timeline`，以及 script task 运行中 `/cancel`。
- 这个 smoke 不依赖真实 LLM API key，适合发布前和本地回归；它补足了 `daemon-smoke.sh` 只验证 HTTP API、不验证 TUI 输入/输出链路的问题。

## 2026-06-26 Embedded Daemon TUI Default

- 默认 `liora` / `liora -interactive` 不再走旧的进程内 runtime submitter，而是在本进程内监听临时 localhost 端口启动 embedded Core Daemon，然后 TUI 通过 `daemonclient` 和 SSE 消费任务事件。
- 显式 `-tui-daemon` 仍表示连接用户已启动的外部 daemon；默认 embedded daemon 避免普通用户还要先手动运行 `liora -daemon`，也让 CLI 入口和未来 Mac 客户端更接近同一套 agent core。
- embedded daemon 退出时会 shutdown HTTP server 并关闭 SQLite DB。当前没有暴露 daemon 地址，也不做跨进程复用；如果后续要做常驻后台进程，需要补本地 token、端口发现和生命周期管理。

## 2026-06-26 Default Patch Mode

- daemon 和默认 TUI 的 `LIORA_PATCH_MODE` 默认值从关闭改为开启，写文件先发生在临时 workspace 副本中，用户通过 `/apply` 或 apply API 后才落到真实 workspace。
- 保留 `LIORA_PATCH_MODE=0` 作为显式关闭开关，便于调试或需要旧式直接写入的本地自动化；非交互脚本模式仍保持直接执行，不受该默认值影响。
- 默认 patch mode 后，运行中 `/cancel` 更容易与 task runner 的事件写入并发，曾触发 SQLite `database is locked`。`Store.OpenDB` 现在设置单连接池和 `PRAGMA busy_timeout=5000`，优先保证本地 agent 的写入稳定性；未来高并发客户端再考虑更细的写队列。

## 2026-06-26 TUI Workbench Baseline

- TUI 首屏新增 `Core` 和 `Safety`，让用户能看到当前是 embedded/external daemon，以及写入策略是 patch-first 还是 direct-write。
- streaming 事件里的 planning、workspace、tool.call、completed/cancelled 改为轻量日志行，避免每个小状态都渲染成卡片；Plan、Tools、Summary、Diff 仍保留区块，保证主要内容可读。
- 本阶段没有引入 Bubble Tea 全屏 TUI，原因是当前 line-based TUI 已经承担 smoke/e2e 基线；先降低噪音和卡顿风险，再做可滚动 transcript、状态栏、diff 面板和快捷键。

## 2026-06-26 TUI Action Feedback

- `daemonclient.Apply` 从弱类型 `map[string]any` 改为结构化 `ApplyResult`，TUI `/apply` 现在会展示 applied task、落盘文件列表和下一步 `/timeline` 提示。
- `/approve` 和 `/deny` 也补充 task status 与后续查看提示，避免确认动作只返回一句不可追踪的文本。
- 这仍是 line-based action feedback，不是完整按钮式确认 UI；全屏 TUI 或 Mac 客户端应继续使用相同 apply/approval API，把这些动作升级成明确控件。

## 2026-06-26 Coding Eval Baseline

- 新增 `scripts/coding-eval.sh`，用临时 fake LLM、临时 daemon 和临时 workspace 跑真实 coding task eval，不依赖用户 API key，也不污染当前项目。
- eval 覆盖自然语言规划、工具执行、patch-first 不直接写真实 workspace、apply 后文件落盘、`task.patch_applied` 事件、session timeline 和运行中 cancel。
- 这不是完整 benchmark suite，只是 v0.1 防退化基线；后续可以继续增加多文件编辑、失败后 replan、MCP 调用和大输出截断 case。

## 2026-06-26 Permission Eval Coverage

- `scripts/coding-eval.sh` 现在启用 `LIORA_PERMISSION=prompt`，并新增危险 shell 的 approve/deny 两条分支，验证 `permission.requested`、`permission.approved`、`permission.denied` 和对应任务终态。
- patch-first 写文件仍不触发审批，危险 shell 仍触发审批；这符合当前产品取舍：默认安全写入减少打扰，但高风险外部动作需要用户确认。

## 2026-06-26 Multi-file Eval Coverage

- `scripts/coding-eval.sh` 新增 multi-file natural task，fake planner 输出两个 `write` 和一个 `diff`，验证 patch mode 下真实 workspace 不提前出现文件。
- eval 会确认 diff 和 apply result 同时包含 `config/settings.txt` 与 `docs/guide.txt`，并在 apply 后校验两个文件内容。这补齐了单文件替换之外的基础 coding task 形态。

## 2026-06-26 Large Output Eval Coverage

- `scripts/coding-eval.sh` 新增大输出 shell task，输出超过当前 `maxShellOutputBytes`，验证 daemon SSE 的 `tool.result` payload 中已经包含 `truncated` 标记。
- 脚本断言使用 shell 字符串匹配而不是 `printf | grep -q`，避免 `set -o pipefail` 下 grep 提前退出导致 broken pipe。这个 case 用来防止大 stdout/stderr 再次拖垮事件流或 TUI。

## 2026-06-26 Sandbox Cancel Cleanup

- 全量测试暴露 `TestLocalExecutorCancelStopsChildProcesses` 偶发失败：只按 process group 发送一次 `SIGKILL` 时，shell 后台子进程仍可能存活并继续写 workspace。
- Unix 本地 executor 取消时现在先递归收集并杀掉当前命令的 descendant pids，再杀 process group；`cmd.Wait()` 返回后再补一次清理。这样覆盖 shell 子进程不完全留在同一 process group 的情况。
- 该实现依赖 Unix `pgrep -P` 做 descendant discovery；macOS 本地优先满足，非 Unix 仍保留原主进程 kill fallback。

## 2026-06-26 Bounded Replan Baseline

- natural task 在工具步骤失败后会最多触发一次 replan：runtime 把用户原始请求、上一次计划、错误信息和当前 diff 交给 planner，要求 LLM 生成修正版步骤并在同一 workspace 副本中继续执行。
- 新增 `task.replanning` 事件，daemon/TUI/未来客户端都能看到“正在修正计划”，随后会出现第二个 `task.plan_ready`。工具级失败现在仍使用 `tool.result` 且 `status=error`，`task.error` 只表示任务最终失败，避免 TUI 在可恢复错误上提前断流。
- 当前不做无限循环或复杂 self-healing tree search，原因是本地 MVP 需要可预测的执行时间和清晰事件线。后续如要对齐更强的 Claude Code/Kimi Code 体验，可以把 replan 次数、错误分类和观察步骤预算做成策略配置。
- `scripts/coding-eval.sh` 新增 `replan-case`，用 fake LLM 先规划错误文件，再根据 replan prompt 中的 failure context 返回正确读取步骤，验证 `task.replanning` 和最终 `task.completed`。

## 2026-06-26 Document Read Tool

- 新增 read-only `document <path> [start line] [line count]` 工具，专门处理 `.pdf` 和 `.docx`，解决用户在真实工作区里让 agent 看 assignment PDF/DOCX 时，模型只能误用 `stat/read/run` 的问题。
- DOCX 使用 Go 标准库 `archive/zip` + `encoding/xml` 直接解析 `word/document.xml`，不增加发布包依赖；当前只提取正文段落，页眉、批注、表格复杂结构会被简化为纯文本。
- PDF 使用系统 `pdftotext -layout -enc UTF-8`，原因是 Go 标准库没有 PDF 文本提取能力，引入完整 PDF 解析库会明显扩大 MVP 依赖面。没有 `pdftotext` 时会返回明确错误，后续 macOS 安装包可考虑内置或检测提示。
- `document` 复用 `read` 的分页和截断策略，避免大 PDF/DOCX 一次性冲垮事件流或 TUI。`scripts/coding-eval.sh` 用最小 DOCX 覆盖 daemon/SSE 的文档读取路径，PDF 路径先由单独环境能力承担。

## 2026-06-26 MCP Eval Coverage

- `scripts/coding-eval.sh` 新增临时 stdio fake MCP server，并在 `LIORA_HOME/mcp.json` 中配置 `fake/echo`，用 natural task 触发 `mcp fake echo {"text":"hello from eval"}`。
- 因为当前权限策略把 MCP 归类为 external，eval 会先验证 `permission.requested` 和 `external` 风险，再调用 approval API，最终确认事件历史里出现 `permission.approved` 与 `mcp echo: hello from eval`。
- 这条 case 证明 MCP 已进入 daemon/session/task/event 主链路，TUI 和未来 Mac 客户端可以复用同一套审批与事件机制。当前 MCP client 仍是每次 list/call 启动一次 stdio server；长连接池、server 生命周期管理和工具 schema UI 仍留到后续阶段。

## 2026-06-26 Daemon Capabilities MCP Tools

- `GET /v1/capabilities` 现在返回内建 `tools` 和可选 `mcp_tools`，后者来自 daemon 持有的同一个 `store.Store` 读取 `mcp.json` 并执行 MCP `tools/list`。TUI 和未来 Mac 客户端可以把这个 API 作为统一工具能力视图。
- daemon-backed TUI 的 `/tools` 现在优先调用 daemon capabilities，而不是回退到 runtime 的本地 builtin 清单；输出中会分组展示 Built-in tools 和 MCP tools。
- 如果 MCP server 启动或 list 失败，capabilities 仍返回内建工具，并附带 `mcp_error`。这是为了避免某个外部 server 坏掉时拖垮整个 TUI 首屏或客户端工具面板。

## 2026-06-26 Failure Diagnostics

- daemonclient 不再把所有 `task.error` SSE 帧都当作传输错误；只有非 JSON payload 的 `task.error` 才表示 stream 级错误。正常任务失败事件会进入 TUI 和未来客户端的事件模型。
- daemon-backed TUI 的 `/last`/`/resume` 回放现在会显示 `tool.result` 的 `status`，并在 `task.error` 中同时展示终态和失败原因，便于用户看到失败工具、输入和下一步恢复线索。
- `scripts/coding-eval.sh` 新增 `read missing-eval.txt` 失败任务，验证 SSE 里同时出现 `tool.result status=error`、`task.error` 和 failed 终态，防止失败路径退化成不可解释的 generic error。

## 2026-06-26 TUI Tail History View

- daemon-backed TUI 新增 `/tail [lines|task_id lines]` 与别名 `/log`，通过 daemonclient.Events 读取 task event history 后显示尾部行。
- 这是 line-based TUI 的长输出回看 MVP，避免为了长输出立即重写 Bubble Tea；默认 40 行，最多 200 行。
- `/tail` 使用 daemon event history，不依赖终端滚屏；未来全屏 TUI 或 Mac 客户端可以基于同一套 event/timeline 数据做可滚动 transcript。
- `scripts/tui-smoke.sh` 覆盖 `/tail 8`，防止长输出回看入口从 daemon-backed TUI 退化。

## 2026-06-26 TUI Diff Preview Command

- daemon-backed TUI 新增 `/diff [task_id]`，复用 daemon diff API 展示最近或指定任务的 patch 预览，并把该 diff 缓存给后续 `/apply`。
- 这是 patch-first 工作流的最低确认体验：用户可以先 review diff，再显式 apply 到真实 workspace。完整 split-pane diff、逐文件勾选和按钮式确认留给全屏 TUI 或 Mac 客户端。
- diff 预览默认最多展示 180 行，避免超大 patch 再次拖慢 line-based TUI；完整 patch 仍保存在 daemon task event/diff API 中。

## 2026-06-26 TUI Approval Queue

- daemon-backed TUI 新增 `/approvals` 与别名 `/pending`，通过 task list + permission.requested event 展示等待审批任务、工具输入、风险类型和原因。
- `/approve` 和 `/deny` 保留无参处理最近 task 的兼容行为，同时新增 `/approve <task_id>`、`/deny <task_id>`，避免多任务等待审批时误操作。
- 这仍是 task 级授权队列，不是逐步 tool approval。它先补齐 line-based TUI 的可见性；后续全屏 TUI 或 Mac 客户端应基于相同事件数据做逐步审批弹窗。

## 2026-06-26 TUI Session Auto Resume

- daemon-backed TUI 在首次提交任务、`/session` 或 `/timeline` 时，如果当前进程还未绑定 session，会自动接回同 workspace 最近更新的 session。
- 新增 `/resume-latest` 用于显式接回最近 session，新增 `/new-session` 用于让下一条任务强制创建新 session，避免自动恢复让用户无法从干净上下文开始。
- 自动恢复只按 workspace 精确匹配，不跨目录复用 session；这保持本地项目隔离，也让未来 Mac 客户端可以复用同一规则做“继续上次工作”入口。

## 2026-06-26 Expanded Transcript View

- 参考本地 Kimi Code 的“持久化 session 可直接继续 prompt”方向，以及 Claude Code 在进入 query 前先写 transcript、assistant 写入可异步 fire-and-forget 的做法；Liora 继续把 session/timeline 放在 daemon + SQLite，TUI 只做投影。
- `/timeline [limit]` 保持紧凑事件线，`/transcript [limit]` 展开 user、assistant、tool、diff、approval 和 status 内容，默认最多取 100 个 timeline item，最多 300 个。
- 后续做真正全屏 TUI 时，应继续使用 Go 的 context/goroutine/channel：每个 session/task 独立流式消费 daemon 事件，UI 层按 session id 聚合，避免单个长输出或慢工具阻塞其它 session。

## 2026-06-26 Workspace Workbench

- 参考 Kimi Code session store 的 workDir bucket 行为，daemon 的 `GET /v1/tasks` 和 `GET /v1/sessions` 新增 `workspace` query filter；TUI 的 `/tasks`、`/sessions` 默认只展示当前 workspace。
- daemon-backed TUI 新增 `/workbench` 与别名 `/status`，展示当前 workspace 的 sessions、active tasks 和 recent tasks。实现上用两个 goroutine 并发拉取 sessions/tasks，并通过 context cancellation 传递错误。
- 这一步让多 session 可见性留在 daemon/client 合同上，而不是写死在 TUI 私有状态；未来 Mac 客户端可以直接复用 workspace-scoped API 构建多项目工作台。

## 2026-06-26 Multi Task Event Stream

- daemonclient 新增 `StreamTaskEvents(ctx, taskIDs)`，最初用每个 task 一个 goroutine 消费现有 SSE，再聚合成带 `TaskID` 的 channel；后续已经升级为消费 daemon 原生多任务 SSE，保留同一个 Go API 给 TUI、CLI 自动化和未来 Mac 客户端复用。
- 设计参考了 Claude Code 的 per-conversation engine 隔离思路，以及 Kimi Code 按 workspace/session 管理历史的方向：核心状态仍在 daemon/session/task/event，入口层只负责投影和交互。
- 多任务流现在由 daemon 端统一按 task seq 增量读取并 fan-in 通知；任一 HTTP 传输错误会通过 buffered error channel 返回。调用方停止消费时仍必须取消 context，否则 channel send 会等待，这是 Go channel API 的显式背压语义。
- daemon 原生 `/v1/tasks/events/stream?task_id=...` 已落地，目的是降低多任务观察时的连接数，并让未来 Mac 客户端无需重新实现多连接聚合。

## 2026-06-26 TUI Multi Task Watch

- daemon-backed TUI 新增 `/watch [active|task_id...]`。无参或 `active` 时只订阅当前 workspace 的 active tasks；显式传 task id 时可以跨 workspace 观察指定任务。
- `/watch` 复用 `daemonclient.StreamTaskEvents`，输出按 `task_id event_type` 前缀聚合。line-based TUI 仍是阻塞式命令，不做全屏持续刷新；这是为了保持现有交互模型稳定，同时把多任务观察能力下沉到可复用 agent core。
- 单次 `/watch` 最多展示 120 个事件，避免大输出任务拖慢终端；完整历史仍通过 daemon event store 和 `/tail <task_id>` 回看。后续全屏 TUI 或 Mac 客户端可以基于同一 fan-in API 做实时列表、状态栏和可滚动 transcript。

## 2026-06-26 TUI Background Spawn

- daemon-backed TUI 新增 `/spawn <request>`，复用同一个 `CreateTask(... RunAsync: true)` 路径创建后台任务并立即返回 task id。前台普通输入仍会继续 stream 到完成，避免改变现有用户习惯。
- `SubmitStream` 和 `/spawn` 共享 `createTask` helper，确保 session auto-resume、workspace、natural mode 和 last task 记录一致。这个设计让 TUI 与未来 Mac 客户端都围绕 daemon task API 组织并发任务，而不是各自维护一套任务状态。
- `/spawn` 不直接输出事件；用户可以用 `/watch <task_id>` 观察实时事件，或任务结束后用 `/tail <task_id>` / `/timeline` 回看。这是 line-based TUI 下的可控并发 MVP，完整任务队列和非阻塞状态栏留给全屏 TUI 或桌面端。
- 为了让后台任务有闭环控制，`/cancel` 同时支持 `/cancel <task_id>`。无参仍保留取消当前前台任务的语义，避免破坏原交互。

## 2026-06-26 Daemon Workbench Snapshot

- daemon 新增 `GET /v1/workbench?workspace=...&limit=N`，一次返回当前 workspace 的 sessions、active tasks、recent tasks 和 pending approvals。这个 projection 属于 agent core API，避免 TUI 和未来 Mac 客户端各自用多次 HTTP 调用拼工作台。
- daemonclient 新增 `Workbench(ctx, workspace, limit)`；daemon-backed TUI 的 `/workbench` 和 `/approvals` 都改为消费这个 API。此前 `/approvals` 用全局 task list，可能把其它 workspace 的等待审批任务混进当前目录，现在已经按 workspace 隔离。
- pending approval 里直接包含最新 `permission.requested` payload。当前仍是 task 级审批；如果后续升级逐步授权 UI，可以在同一个 workbench snapshot 里扩展 approval item，而不用改变 TUI/Mac 客户端的入口模型。

## 2026-06-26 Timeline Search

- repository 新增 `SearchTimeline(workspace, query, limit)`，daemon 暴露 `GET /v1/timeline/search?q=...&workspace=...`，daemonclient 暴露 `SearchTimeline`。搜索覆盖 user message、assistant summary、tool input/output、diff、approval/status 等 timeline 投影文本。
- daemon-backed TUI 新增 `/history <query>` 和 `/search-history <query>`。这让用户重启后可以按关键词找回历史任务，而不是只能知道 session id 后手动 `/timeline` 翻页。
- 当前搜索是基于现有 session/timeline 投影的轻量实现，不新增 materialized transcript 表；结果按时间倒序返回。后续如果要做长期全文搜索、embedding 或 compaction，再单独落 transcript/search 表。

## 2026-06-26 Daemon Multi Task SSE

- daemon 新增 `GET /v1/tasks/events/stream?task_id=...&task_id=...`，单条 SSE 连接可订阅多个 task。每个 SSE frame 的 `data` 是 envelope：`{"task_id": "...", "payload": {...}}`，`event` 仍保持原 task event type。
- `daemonclient.StreamTaskEvents` 从 client-side goroutine fan-in 改为消费 daemon 原生多任务 SSE，减少未来 TUI/Mac 客户端重复实现多连接聚合的负担。
- repository 新增 `SubscribeEventsAny`，内部仍复用 per-task subscriber，并用 `sync.Once` 做 fan-in，避免同一 channel 注册到多个 task 后被重复 close。多任务 stream 仍按每个 task 的 `seq` 增量读取，保留低频 fallback。

## 2026-06-26 Daemon Memory API

- 本轮没有重做 memory schema，而是把已有 SQLite `memories` 表提升到 daemon/client/TUI 共享合同：`GET /v1/memories?q=...&limit=...` 和 `POST /v1/memories`。这样未来 Mac 客户端不需要直接读 `LIORA_HOME` 文件，也不需要复刻 runtime 命令逻辑。
- `store.AddMemory` 保持兼容旧调用，新加 `CreateMemory` 返回结构化 `Memory`，方便 API 创建后展示 id/text/kind/source/importance 等字段。
- daemon-backed TUI 的 `/memory list|add|search` 现在优先走 daemonclient，而不是落回本地 runtime store。当前仍是手动记忆，尚未做自动抽取、embedding 或 last_used_at 更新；这些属于 v0.2 的记忆质量优化。
