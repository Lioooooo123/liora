# Packages

跨 app 复用的非 Go 包放在这里。

当前共享核心仍在根目录 Go module 的 `internal/` 下，先不做大规模迁移，避免破坏已经通过 smoke 的 daemon、task、runtime 和 store 边界。

未来可新增：

- `packages/protocol`: daemon API schema、SSE event 类型和客户端 SDK。
- `packages/ui`: TUI/desktop 共享的主题、快捷键和渲染约定。
- `packages/evals`: 面向 agent 体验的端到端评测用例。
