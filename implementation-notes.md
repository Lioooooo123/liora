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
