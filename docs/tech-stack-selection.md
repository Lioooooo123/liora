# Liora 技术选型

## 结论

Liora 采用“Go core + TypeScript UI monorepo”的路线。

| 层级 | 选型 | 状态 | 结论 |
| --- | --- | --- | --- |
| Agent Core | Go | 已在用 | 保留。负责 daemon、task、sandbox、LLM provider、SQLite、MCP、patch/apply。 |
| 下一代 TUI | TypeScript + React + Ink | 下一阶段 | 采用。复杂终端 UI 不继续堆 Go line-based TUI。 |
| 当前 CLI | Go `apps/cli` | 已在用 | 保留。继续提供 `liora` 二进制、daemon、脚本模式和轻量 TUI。 |
| 桌面端 | 先不启动；候选 Tauri 2 或 SwiftUI | v0.2+ | 等 TUI/daemon 合同稳定后再选。产品验证阶段优先 Tauri，macOS 深集成阶段再考虑 SwiftUI。 |
| Monorepo 包管理 | pnpm workspace | 已建骨架 | 采用。管理未来 `apps/tui`、`packages/protocol`、`packages/ui`。 |
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

Go 不适合作为下一阶段复杂 TUI 的主要表达层：

- 复杂布局、局部重绘、输入栏、快捷键、弹窗、diff viewport、虚拟列表会把 Go TUI 代码推向高复杂度。
- 产品视觉和交互动效迭代速度不如 React 生态。
- 未来桌面端如果也使用 Web/React 技术，TUI 的状态管理和组件模型可以复用更多经验。

## 下一代 TUI 选 Ink/React

选择 `Ink + React + TypeScript` 放在 `apps/tui`。

理由：

- Ink 是 React renderer，适合用组件模型构建交互式命令行 UI。
- React 组件、hooks、状态管理对复杂 TUI 更自然，适合 transcript、tool stream、approval modal、diff viewer。
- TypeScript 能把 daemon event、task、session、memory、approval 类型沉淀到 `packages/protocol`。
- 与未来 Tauri/Web desktop 的 UI 心智接近。

第一版 `apps/tui` 范围：

- 连接本地 daemon；必要时拉起 embedded daemon 或提示用户启动。
- 显示 session timeline、当前 task、tool stream、diff、approval。
- 底部固定输入栏。
- 支持 `/help`、`/cancel`、`/apply`、`/approve`、`/deny`、`/timeline`、`/history`。
- 长输出折叠，按需展开。

不在第一版 TUI 做：

- 不重新实现 planner、tool executor、sandbox。
- 不直接读写 SQLite。
- 不实现完整桌面窗口。
- 不把所有 Claude Code 功能一次性搬完。

## 为什么不是 Bubble Tea

Bubble Tea 是成熟的 Go TUI 框架，适合 Go-only 工具和小中型 TUI。

Liora 不选它作为下一代主 TUI，原因是：

- 我们已经决定 monorepo，为未来 React/Tauri UI 铺路。
- Agent UI 的复杂度更接近“终端里的应用”，组件组合和状态管理比纯 Go 结构体模式更重要。
- Go 继续负责 core，TUI 再用 Go 会让 UI 和 core 更容易重新耦合。

保留 Bubble Tea 作为备选：

- 如果后续希望继续保持单二进制、极低 Node 依赖，可以用 Bubble Tea 重写 `apps/cli` 内的全屏模式。
- 但这会牺牲未来 desktop/web UI 的复用心智。

## 桌面端选择延后

桌面端暂不立刻开工。等 `apps/tui` 和 daemon API 跑顺后再选：

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

- v0.1/v0.2：先不做桌面端，集中打磨 agent core 和 Ink TUI。
- v0.3：如果需要快速产品化，优先 Tauri 2。
- v1.0：如果只押 macOS 且追求原生质感，再评估 SwiftUI。

## Monorepo 组织

当前结构：

```text
apps/
  cli/        Go CLI/TUI/daemon 入口，产物名 liora
  tui/        下一代 Ink/React TUI
internal/     Go core
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

### Phase 2：Ink TUI MVP

目标：

- 新增 `apps/tui` 可运行入口。
- 首屏 workbench、底部输入栏、timeline、tool stream、diff preview。
- 支持 cancel/apply/approve/deny。

完成标准：

- 能替代当前 Go line TUI 完成 daemon smoke 的核心场景。
- 保留 `apps/cli` 作为稳定 fallback。

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
| 继续纯 Go TUI | 能做，但复杂 UI 迭代成本会越来越高。 |
| Python core | 发布和本地系统集成不如 Go 单二进制稳定。 |
| Web-only app | 失去本地 agent 的产品定位。 |
| 直接 SwiftUI | 过早，会拖慢 agent core 和 TUI 验证。 |
| 直接 Rust core | 当前 Go core 已可运行，重写收益不足。 |

## 参考

- Ink: https://github.com/vadimdemedes/ink
- Bubble Tea: https://github.com/charmbracelet/bubbletea
- Tauri 2: https://v2.tauri.app/
- pnpm Workspaces: https://pnpm.io/workspaces
