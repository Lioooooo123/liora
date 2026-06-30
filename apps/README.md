# Apps

Liora 的用户入口放在这里。

- `cli`: 当前可用的 Go CLI/TUI/daemon 入口，产物叫 `liora`。交互 TUI 由 Go 原生实现（见 `internal/tui`，TTY 下 Bubble Tea 全屏、非 TTY 下 line-based renderer），与 core 同二进制、无 Node 依赖。

未来的 Web/桌面入口应通过 daemon API 复用 Go core，而不是重写 agent 执行逻辑。
