# Liora 技术选型

## 结论

Liora 采用“Go core + Go 原生 TUI，TypeScript 协议/桌面预留”的路线。

| 层级 | 选型 | 状态 | 结论 |
| --- | --- | --- | --- |
| Agent Core | Go | 已在用 | 保留。负责 daemon、task、sandbox、LLM provider、SQLite、MCP、patch/apply。 |
| 全屏 TUI | Go + Bubble Tea (charmbracelet) | 已在用 | 采用。与 core 同二进制、零运行时依赖，TTY 自动启用；非 TTY 回退 line-based。 |
| 当前 CLI | Go `apps/cli` | 已在用 | 保留。继续提供 `liora` 二进制、daemon、脚本模式和 TUI 入口装配。 |
| 桌面端 | 先不启动；候选 Tauri 2 或 SwiftUI | v0.2+ | 等 daemon 合同稳定后再选。产品验证阶段优先 Tauri，macOS 深集成阶段再考虑 SwiftUI。 |
| Monorepo 包管理 | pnpm workspace | 已建骨架 | 保留。为未来 `packages/protocol`、`packages/ui`、桌面端预留。 |
| API 协议 | HTTP + SSE + JSON schema | 已在用 | 保留。TUI/desktop 都通过 daemon API 复用 core。 |
| 本地数据库 | SQLite | 已在用 | 保留。适合本地优先、单用户、可迁移。 |
| Sandbox | Docker + local fallback | 已在用 | 逐步把 Docker 提升为默认执行策略。 |

## 决策原则

- Core 和 UI 分离：UI 不直接执行工具，只消费 daemon API。
- 先保证 coding agent 可用，再追求可爱、二次元和桌面产品感。
- TUI 要能承载长 transcript、实时工具流、diff、approval、任务列表和快捷键。
- 技术栈要利于未来 Mac 客户端复用协议，而不是把业务逻辑写进某个 UI 入口。
- 所有重构必须保留 `./scripts/v0.1-exit-audit.sh "$PWD"` 可通过。

## 为什么 Go 仍然适合 Core

Go 适合 Liora 的执行底座：

- 并发模型简单，适合多 session、多 task、SSE fan-in、cancel context。
- 单二进制发布简单，适合本地 agent。
- 文件、进程、Docker、SQLite、HTTP server 这些系统能力成熟。
- 当前 `internal/daemon`、`internal/task`、`internal/runtime`、`internal/store` 已有可运行基础。

Go 同样能胜任全屏 TUI：

- charmbracelet 生态（Bubble Tea / bubbles / lipgloss）已经把布局、局部重绘、输入栏、viewport、spinner、样式做成成熟组件。
- TUI 与 core 同语言、同进程、同二进制，省去跨语言协议桥接和额外运行时。
- Elm 架构（Model/Update/View）的单 goroutine 消息循环天然契合 SSE 流式事件的串行消费。

## 下一代 TUI 选 Bubble Tea

选择 charmbracelet 的 **Bubble Tea + bubbles + lipgloss**，实现放在 `internal/tui`（与 line-based renderer 同包复用渲染助手与样式）。

理由：

- Bubble Tea 是成熟的 Go TUI 框架，Elm 架构清晰，适合“终端里的应用”级别交互。
- 与 Go core 同二进制，**零 Node / 零额外运行时依赖**，打包链路最简单，最贴合“本地优先、单二进制”定位。
- bubbles 提供 textinput / viewport / spinner 等现成组件，覆盖输入栏、可滚动 transcript、运行态指示。
- 单 goroutine 消息循环 + buffered channel 桥接，能安全消费 daemon SSE 流式事件。
- TTY 自动启用 Bubble Tea 全屏；非 TTY（管道 / CI / smoke）回退 line-based renderer，smoke 基线零改动。

第一版 Bubble Tea TUI 范围：

- 连接本地 daemon；必要时拉起 embedded daemon 或提示用户启动。
- 首屏 workbench（workspace / model / core / safety）+ 可滚动事件区 + 底部固定输入栏。
- 实时消费 plan / tool stream / summary / diff，流式逐条渲染。
- 支持 `/help`、`/cancel`、`/apply`、`/approve`、`/deny`、`/timeline`、`/history` 等命令（复用 `internal/tuisession`）。

不在第一版 TUI 做：

- 不重新实现 planner、tool executor、sandbox。
- 不直接读写 SQLite。
- 不实现完整桌面窗口。
- 不把所有 Claude Code 功能一次性搬完。

## 为什么不是 Ink / React

早期曾设想用 `Ink + React + TypeScript` 做下一代 TUI，最终放弃，改用 Go 原生 Bubble Tea。原因：

- Ink 引入 Node 运行时与 npm 依赖树，破坏“单二进制、本地优先、零额外运行时”的核心定位。
- CLI 入口与 TUI 跨语言（Go ↔ Node）需要额外进程拉起、脚本分发与协议桥接，打包/安装链路显著复杂化。
- Bubble Tea 已能满足当前 TUI 复杂度（输入栏、viewport、流式事件、命令），不需要 React 组件模型的额外抽象。
- TUI 用 Go 与 core 同包，可直接复用渲染助手与样式，反而降低耦合维护成本。

保留 TypeScript 的使用场景：

- 未来 `packages/protocol`（daemon API 类型 / client / SSE parser）与桌面端（Tauri/Web）仍可用 TS。
- TUI 不再是 TS 的落点，TS 聚焦协议层与潜在 Web/桌面 UI。

## 桌面端选择延后

桌面端暂不立刻开工。等 Bubble Tea TUI 和 daemon API 跑顺后再选：

### 候选 A：Tauri 2

适合产品验证期：

- 能使用 React/Vite 等 Web 前端栈。
- 跨平台能力强，未来不只 macOS。
- 体积和资源占用通常优于 Electron。
- 可以通过本地 HTTP/Unix socket 调 Go daemon。

风险：

- 引入 Rust/Tauri 打包链路。
- macOS 原生体验、权限、菜单栏、全局快捷键仍需要细调。

### 候选 B：SwiftUI

适合 macOS 深集成期：

- 原生窗口、菜单栏、通知、权限、快捷键体验更好。
- 更符合“精致小巧 Mac 本地陪伴”的长期产品感。

风险：

- 与 TypeScript TUI/Web 组件复用少。
- 开发成本更高，跨平台弱。
- 需要维护 Swift 与 Go daemon 的协议边界。

当前建议：

- v0.1/v0.2：先不做桌面端，集中打磨 agent core 和 Bubble Tea TUI。
- v0.3：如果需要快速产品化，优先 Tauri 2。
- v1.0：如果只押 macOS 且追求原生质感，再评估 SwiftUI。

## Monorepo 组织

当前结构：

```text
apps/
  cli/        Go CLI/TUI/daemon 入口，产物名 liora
internal/     Go core
  tui/        Go 原生 TUI：Bubble Tea 全屏 + line-based fallback
packages/
  protocol/   未来：daemon API 类型、SSE event 类型、client SDK
  ui/         未来：主题、快捷键、终端 UI 约定
  evals/      未来：agent 回归评测 case
scripts/      smoke、install、package、audit
docs/         产品和架构文档
```

根工具链：

- `pnpm-workspace.yaml` 管理 `apps/*` 和 `packages/*`。
- 根 `package.json` 提供跨栈脚本。
- Go module 仍在根目录，避免破坏 `internal/` 可见性和已通过的 smoke。

## 协议边界

所有 UI 都通过 daemon API：

- `GET /v1/capabilities`
- `POST /v1/tasks`
- `GET /v1/tasks/{id}`
- `GET /v1/tasks/{id}/events/stream`
- `GET /v1/tasks/{id}/diff`
- `POST /v1/tasks/{id}/apply`
- `POST /v1/tasks/{id}/cancel`
- `POST /v1/tasks/{id}/approval`
- `GET /v1/sessions/{id}/timeline`
- `GET /v1/memories`
- `POST /v1/memories`

后续应新增 `packages/protocol`：

- TypeScript 类型定义。
- daemon client。
- SSE parser。
- event reducer，把 task events 规约成 UI view model。

Go 侧可以保留 `internal/daemonclient`，两边通过 API contract 对齐，不互相 import。

## 阶段计划

### Phase 0：当前状态

- Go core 可运行。
- `apps/cli` 可安装、打包。
- line-based TUI 可 smoke。
- monorepo 骨架已建立。

完成标准：

- `GOTOOLCHAIN=local ./scripts/v0.1-exit-audit.sh "$PWD"` 通过。

### Phase 1：Protocol 包

目标：

- 新增 `packages/protocol`。
- 抽出 TypeScript task/session/event 类型。
- 实现 daemon client 和 SSE parser。
- 用 contract fixture 覆盖 Go daemon 输出。

完成标准：

- `pnpm test --filter @liora/protocol` 通过。
- Go daemon fixture 与 TS parser 对齐。

### Phase 2：Bubble Tea TUI（已达成）

目标：

- 在 `internal/tui` 新增 Bubble Tea 全屏 renderer，TTY 自动启用。
- 首屏 workbench、底部输入栏、可滚动事件区、流式 tool stream、diff preview。
- 支持 cancel/apply/approve/deny（复用 `internal/tuisession`）。

完成标准：

- Bubble Tea（TTY）与 line-based（非 TTY）共享 daemon 事件，渲染一致。
- 非 TTY smoke 走 line renderer，断言串零改动；exit-audit 全绿。

### Phase 3：Agent 能力升级

目标：

- 从“文本计划”升级到多轮 tool-use loop。
- 引入结构化 tool schema。
- 加强权限、sandbox 和 eval。

完成标准：

- 固定 coding eval case 通过率稳定。
- 失败可解释、可恢复、可回放。

### Phase 4：桌面端

目标：

- 基于 Tauri 2 或 SwiftUI 做桌面入口。
- 复用 daemon 和 protocol。
- 加入任务工坊、白板、记忆、角色房间。

完成标准：

- 不复制 agent core。
- 桌面端只是另一个入口。

## 当前不选

| 方案 | 不选原因 |
| --- | --- |
| Electron | 太重，和“精致小巧本地陪伴”不匹配。 |
| Ink / React TUI | 引入 Node 运行时与跨语言桥接，破坏单二进制、本地优先定位。 |
| Python core | 发布和本地系统集成不如 Go 单二进制稳定。 |
| Web-only app | 失去本地 agent 的产品定位。 |
| 直接 SwiftUI | 过早，会拖慢 agent core 和 TUI 验证。 |
| 直接 Rust core | 当前 Go core 已可运行，重写收益不足。 |

## 参考

- Bubble Tea: https://github.com/charmbracelet/bubbletea
- Bubbles: https://github.com/charmbracelet/bubbles
- Lip Gloss: https://github.com/charmbracelet/lipgloss
- Tauri 2: https://v2.tauri.app/
- pnpm Workspaces: https://pnpm.io/workspaces
