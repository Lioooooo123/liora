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
