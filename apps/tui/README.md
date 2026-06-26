# Liora TUI

这个目录预留给下一阶段的独立 TUI app。

当前 Go 入口 `apps/cli` 仍保留轻量 line-based TUI，保证 `liora` 可安装、可 smoke、可打包。复杂 TUI 不应重新实现 agent 逻辑，而应通过 Core Daemon API 复用：

- `GET /v1/capabilities`
- `POST /v1/tasks`
- `GET /v1/tasks/{id}/events/stream`
- `GET /v1/tasks/{id}/diff`
- `POST /v1/tasks/{id}/apply`
- session、timeline、memory 和 approval API

设计边界：

- TUI 只做输入、渲染、快捷键、审批和 diff 交互。
- 任务执行、sandbox、LLM provider、SQLite store、MCP 和 patch/apply 继续留在 Go core。
- 后续如果选择 Ink/React，放置自己的 `package.json`，并纳入根 `pnpm-workspace.yaml`。
