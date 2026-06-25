# Liora TUI 与 Coding Agent 能力复刻计划

## 背景

这轮目标是先把 TUI 和 coding agent 的主体验做好，后续 Mac 客户端只作为另一个入口复用同一套 core 能力。参考对象是本地的 `kimi-code` 与 `claude-code-main`，但 Liora 不直接复制它们的实现语言和全部产品复杂度，而是吸收对当前 MVP 最关键的工程实践。

## 参考结论

### Kimi Code 可借鉴点

- 会话优先：任务、事件、取消、恢复、approval/question 都围绕 session/task API 组织。
- 事件流优先：工具调用、计划、命令输出、diff 和终态都通过结构化事件传给 UI。
- e2e 先行：server e2e 覆盖 create/send/cancel/approval/tool-call/terminal/session-resume，比只测单个函数更能保证真实体验。
- ACP/MCP 边界清晰：编辑器、TUI、客户端都不应该直接绑死内部 agent，实现上要有协议层或适配层。

### Claude Code 可借鉴点

- 首屏性能优先：慢初始化、权限/配置读取、后台扫描不能挡住 REPL 首屏。
- REPL 状态分层：输入、消息时间线、工具流、任务状态、权限弹窗、transcript 搜索各自独立，避免一次事件导致整棵 UI 高频重绘。
- 每个任务有取消句柄：任务运行必须拿到 `AbortController`/context，用户中断时能停止 shell 和 agent loop。
- 权限与沙箱是产品能力：危险 shell、写文件、MCP 外部调用应该有清晰可见的策略，而不是藏在实现里。

## Liora 架构原则

TUI 和未来 Mac 客户端都只做入口，不能各自实现 agent 逻辑。

```text
TUI / Mac Client / CLI
        |
        v
Session API / Daemon API
        |
        v
Agent Core
  - Planner
  - Tool Registry
  - Permission Policy
  - Sandbox Executor
  - Event Bus
  - Store
        |
        v
Workspace / Docker / MCP / LLM Providers
```

## 能力基准

### P0：当前 TUI 体验修复

- 输入只出现一次，不重复渲染 `You` 区块。
- `/tools` 能展示内建工具、参数格式、风险类型。
- 用户问“你能干嘛”时，模型和命令都能基于同一份工具能力清单回答。
- 文件名支持空格、引号和反斜杠转义。
- 任务结束后清楚展示 Plan、Tools、Summary、Diff、Next。

### P1：事件流 TUI

- TUI 通过 daemon task API 创建任务，而不是同步阻塞 `Submit`。
- 通过 SSE 增量展示 planning、plan_ready、tool.call、tool.result、summary、diff、completed/error/cancelled。
- 正在运行时支持 `/cancel` 或 Ctrl+C 取消当前任务。
- TUI 只重绘变化区域，长输出默认折叠，避免卡顿。
- 同一套事件模型可被未来 Mac 客户端复用。

### P2：权限与确认

- 写文件默认走 patch mode，真实 workspace 只在用户确认 apply 后变化。
- Shell 工具标记风险等级，危险命令进入 waiting_user。
- MCP 外部调用展示 server/tool/args，必要时要求确认。
- TUI 展示 apply/cancel/continue 的明确动作。

### P3：会话与恢复

- SQLite 保存 session/task/message/event/transcript。
- 支持 `/sessions`、`/resume`、`/last`。
- TUI 重新启动后可以恢复最近 workspace 的任务历史。
- 客户端直接复用 daemon API 获取历史和实时事件。

### P4：产品化 TUI

- 使用 Bubble Tea 或等价模型实现全屏 TUI。
- 视图区分：消息时间线、当前任务状态、工具事件、diff 预览、输入栏、状态栏。
- 首屏不阻塞慢初始化，MCP/skills/git 扫描后台完成后更新状态。
- 支持 transcript 搜索、长输出分页、模型/权限模式状态显示。

## 第一阶段落地

本阶段先做共享能力基础，不急着全量重写 TUI：

- 新增 `internal/capabilities`，集中维护内建工具清单。
- planner allowed tools、`/tools` 命令、TUI welcome 使用同一份清单。
- daemon API 暴露 `GET /v1/capabilities`，Mac 客户端直接读取工具能力和风险类型。
- 新增 `internal/daemonclient`，封装 health、capabilities、create/list/get task、events、SSE stream、diff、apply、cancel。后续 TUI 和 Mac 客户端都应使用这个 client，而不是各自拼 HTTP 路径和 SSE 解析。

## 第二阶段入口

下一阶段优先把 TUI 从同步 in-process `Submit` 改成 daemon task 模式：

- 已新增 `-tui-daemon`，可连接已运行 daemon。
- 已创建 async task。
- 已用 `daemonclient.StreamEvents` 驱动实时渲染。
- 已在 daemon-backed session 层支持 `/cancel` 调用 `daemonclient.Cancel`。
- 已在 daemon-backed session 层支持 `/apply` 调用 apply API。
- 待做：line-based TUI 当前仍会在任务运行期间阻塞输入，真正的运行中快捷键取消需要 Bubble Tea/异步输入层。
- 待做：diff 出现后的动作区需要升级为更明确的交互控件，而不是只依赖用户手输 `/apply`。

当前实现仍是 line-based TUI，不是 Bubble Tea 全屏 UI。它已经把任务执行链路迁到 daemon/SSE，可作为下一步全屏 TUI 的数据通路验证。

## 验证门槛

- `GOTOOLCHAIN=local go test -count=1 ./...` 通过。
- daemon smoke 通过。
- TUI smoke 至少覆盖：启动、`/tools`、自然语言 list、空格文件名 stat。
- 任何 TUI 重构都必须保留非交互 CLI 和 daemon API 可用性。
