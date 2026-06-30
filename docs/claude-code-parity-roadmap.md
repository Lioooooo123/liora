# Liora Claude Code 能力对标路线图

> 更新日期：2026-06-30
> 目标：把 Liora 从当前可运行的本地 Coding Agent 底座，分阶段推进到接近 Claude Code 的本地 coding agent 能力。

## 1. 对标范围

这份文档不把 Claude Code / Kimi Code 的所有产品功能一次性搬进 Liora。当前路线仍以 Liora 已确立的技术底座为前提：

- Go core + daemon + SQLite + HTTP/SSE 是执行与状态中心。
- TUI 与未来桌面端都只消费 daemon/protocol，不复制 agent core。
- TTY 下采用 Go 原生 Bubble Tea，全屏 TUI 与 line-based renderer 共享 daemon 事件。
- v0.1 仍以 `scripts/v0.1-exit-audit.sh` 收敛，不把桌面端、完整插件市场、多 agent 调度等作为 v0.1 结束条件。

最终对标的是“可长期工作的本地 coding agent”：能可靠执行多轮工具调用，能恢复上下文，能安全处理文件和命令，能在终端里清楚展示任务、权限、diff、transcript、长输出和进度。

## 2. 当前进度

### 已具备

- **Core/daemon 主链路**：Liora 已有 CLI/TUI -> daemon/session/task -> runtime/agent/LLM -> workspace/sandbox/MCP -> event/store 的主链路。
- **结构化 tool-use loop**：`internal/agent/loop.go` 已支持多轮工具调用、工具结果回灌、失败自修复回调、读工具并发与写/shell/external 串行。
- **大输出落盘**：tool-use loop 已把超过预算的工具输出保存到 `.liora/tool-results/...`，并把 `output_path`、大小和预览返回给模型，开始对齐 Claude Code / Kimi Code 的大输出预算实践。
- **daemon API 与 SSE**：任务、事件、session、timeline、workbench、memory、capabilities、diff/apply/cancel/approval 已通过 daemon API 暴露。
- **patch-first 安全基线**：默认 patch mode 先写临时 workspace，用户通过 `/diff` / `/apply` 显式落到真实 workspace。
- **会话与历史**：SQLite 已保存 task/event/session/message/memory，TUI 支持 `/timeline`、`/transcript`、`/history`、`/resume-session`、`/resume-latest`、`/new-session`。
- **MCP 基线**：支持 stdio MCP 工具 list/call，并进入 capabilities、审批与事件链路。
- **TUI 基线**：已有 line-based TUI 与 Bubble Tea 全屏骨架，命令集覆盖 `/tools`、`/diff`、`/apply`、`/cancel`、`/approvals`、`/tail`、`/watch`、`/spawn` 等。

### 仍有差距

- **全屏 REPL 体验不足**：Bubble Tea 已可运行，但还不是 Claude Code 那种 transcript、tool stream、todo、approval、diff、status 多面板主控制台。
- **权限仍偏 task 级**：当前 `LIORA_PERMISSION=prompt` 能拦截危险 shell、非 patch 写、MCP 外部调用，但不是逐 tool-call 的审批队列，也没有 always allow/deny 规则、hook/classifier/remote 共同裁决。
- **transcript 仍是投影**：assistant/tool/diff/approval 主要从 task_events 投影，还不是可长期 resume、搜索、压缩、导出的 materialized transcript。
- **compaction 缺失**：还没有 token 阈值、手动 `/compact`、自动 compaction、compact boundary 和 resume 后 compact 状态恢复。
- **协议层未沉淀**：Go `internal/daemonclient` 已有，但 `packages/protocol` 的 TS 类型、client、SSE parser、event reducer 尚未建立。
- **后台任务和子 agent 不完整**：已有 `/spawn` / `/watch` 的后台 task MVP，但没有 Claude/Kimi 风格的持久 background task、TaskOutput/TaskStop、subagent resume 与任务输出日志。
- **daemon 安全边界弱**：daemon 当前适合本机临时使用，常驻/桌面端前需要 token、Unix socket 或 localhost-only + capability gate。
- **Docker 默认化未完成**：v0.1 只要求 Docker executor 可配置，尚未把 Docker sandbox 作为默认策略。

## 3. 参考源码结论

### Claude Code 最值得对齐的实践

- **tool-use loop 以实际 tool_use 为终止信号**：Claude Code 在 `src/query.ts` 明确不信 `stop_reason === tool_use`，而是以 streaming 中是否出现 tool_use block 判断是否继续。
- **工具系统自带权限模型**：每个 tool 都有 schema、permission model、执行逻辑和结果映射，权限流可以由配置、用户、hook、classifier、remote/bridge 多方裁决。
- **TodoWrite 是正式工具**：TodoWrite 不只是 UI 清单，而是模型可调用工具，用来持续维护当前任务状态，并能在缺少验证项时提醒补齐。
- **transcript 是恢复核心**：session resume 不只是恢复 UI，还恢复 transcript、plan、file history、worktree、cost、hooks 和 content replacement state。
- **compaction 是边界管理**：自动/手动 compact 和 token budget、resume、task budget 联动，不是简单摘要一次历史。
- **大输出文件化**：长任务和工具输出进入 session/task 目录，模型和用户拿到预览与完整文件路径，而不是把超长内容塞进上下文。
- **hooks/MCP 是系统能力**：hooks、MCP、权限、session start、transcript 持久化彼此联动，不是孤立扩展点。

### Kimi Code 最值得对齐的实践

- **Session 是单会话控制面**：Kimi Code 的 Session API 统一承载 prompt、steer、cancel、permission、plan、compact、background task、goal、MCP、plugins。
- **事件是 UI 的一等输入**：assistant/thinking/tool/approval/question/compaction/subagent/background 都有公开事件，TUI 以事件状态机投影 UI。
- **TUI 有专门 streaming controller**：tool call、pending 缓存、子 agent 聚合、read group、长输出 preview 都在 UI controller 中集中处理。
- **审批是 session-scoped handler**：approval/question handler 有缺省兜底和 TUI 面板，不只是单次阻塞。
- **ACP/协议边界清楚**：`kimi acp` 通过 stdio 暴露协议入口，协议层消费 session snapshot/command snapshot，不耦合 TUI 组件。
- **MCP 与插件状态可见**：MCP server status、startup metrics、reconnect、plugin MCP enable/disable 都是 session/事件层能力。
- **已验证测试覆盖面广**：session event union、permission mode、compact/usage/resume 等都有专门测试，Liora 后续应补同类 contract/e2e 验证。

## 4. 对标差距矩阵

| 能力 | Claude Code / Kimi Code 参考 | Liora 当前状态 | 下一步 |
| --- | --- | --- | --- |
| Tool-use loop | 以 tool_use/toolCalls 继续循环，工具结果成对回灌，异常也转 tool result | 已有 native loop，provider 不支持 tools 时回退 planner 文本 | 收敛双路径差异，扩大 provider tool-call 支持，补工具结果 contract 测试 |
| TodoWrite / Task plan | TodoWrite/TaskList 是模型工具和 UI 状态源 | 只有 Codex 侧计划工具；Liora 内部没有 TodoWrite 工具 | 新增 `todo_write`/`todo_read` 或 session task-plan 工具，进入 TUI panel |
| 长输出预算 | output_path / output.log / TaskOutput 可分页读取 | tool-use 大输出已落 `.liora/tool-results`，patch-mode 持久性仍弱 | 抬到 daemon/session artifact store，增加 `/v1/tasks/{id}/artifacts` 与 TUI 分页 |
| 权限审批 | per tool-call，配置/用户/hook/classifier/remote 共同裁决 | task 级 prompt，危险 shell/非 patch 写/MCP 拦截 | 做 approval queue：tool_call_id、diff/command preview、always allow/deny、超时与重复响应保护 |
| Transcript/resume | 可重建 transcript + plan/file history/worktree/cost/hooks | timeline 投影可用，但不是 materialized transcript | 建 `transcript_entries`，记录原生 tool calls/results、compact boundary、content replacements |
| Compaction | 手动 `/compact` + 自动阈值 + overflow retry | 缺失 | Phase 2 先做手动 compact，Phase 3 做自动 compact 与 compact 后 resume |
| TUI/REPL | 多面板、快捷键、approval/dialog、todo、transcript 搜索 | line-based 稳定，Bubble Tea 骨架已有 | 全屏 TUI 升级为主体验，line-based 保留 smoke |
| Protocol/SDK | Kimi node-sdk/ACP，Claude SDK/remote IO | Go daemonclient 已有，TS protocol 未建 | 建 `packages/protocol`，输出 typed client、SSE parser、fixtures |
| MCP/hooks/plugins | MCP 与 hooks 进入权限、事件、session 生命周期 | MCP 基线已进 daemon；hooks/plugins 基本缺失 | 先做 hook contract，再做 plugin manifest 与 MCP 状态面板 |
| Background tasks | TaskOutput/TaskStop，session task output 持久化 | `/spawn`/`/watch` MVP | 后台任务持久化、输出日志、restart lost 状态、TaskOutput/TaskStop 工具 |
| Sandbox/daemon safety | 权限模式、沙箱、网络权限、remote session | patch-first + Docker 可配置，daemon 无鉴权 | Docker 默认化，daemon token/socket，network/file policy |

## 5. 分阶段路线

### Phase 0：锁定 v0.1 基线

目标：不继续扩散 v0.1 范围，把当前实现、文档、audit 和 release 打磨成可复现底座。

验收：

- `GOTOOLCHAIN=local ./scripts/v0.1-exit-audit.sh "$PWD"` 在干净 main 上通过。
- `docs/v0.1-exit-audit.md` 的 P0 evidence matrix 没有空项。
- `implementation-notes.md` 记录 tool-use loop、大输出落盘、Bubble Tea 路线、未完成风险。
- release 包含 README、关键 docs、二进制、install script，并通过 `scripts/release-smoke.sh`。

当前进度：开发态已可用 `--skip-git-clean` 跑 audit；本轮只沉淀路线图，不声称 v0.1 最终完成。

### Phase 1：Protocol 与事件合同

目标：把 daemon API/SSE 从“Go client 可用”升级为“多入口稳定合同”。

工作项：

- 新建 `packages/protocol`：task/session/event/capability/approval/memory 类型。
- 增加 SSE parser 与 event reducer，把 task events 规约为 UI view model。
- Go daemon 产出 fixture，TS protocol 用同 fixture 做测试。
- 明确 event versioning，避免后续 TUI/Mac/ACP 入口被 Go 内部结构牵连。

验收：

- `pnpm test --filter @liora/protocol` 通过。
- Go 侧 contract fixture 与 TS parser/reducer 对齐。
- Bubble Tea 与 line-based TUI 仍通过 daemonclient 共享同一事件语义。

### Phase 2：Transcript、TodoWrite 与可恢复上下文

目标：把“能回看 timeline”升级为“能长期恢复工作上下文”。

工作项：

- 新增 materialized `transcript_entries`：user、assistant、tool_call、tool_result、diff、approval、compact_boundary。
- 新增 TodoWrite/TodoRead 或 session plan 工具，模型可以维护当前任务清单；TUI 增加 todo panel。
- 把大输出从 workspace `.liora/tool-results` 抬到 daemon/session artifact store，支持分页读取和长期 resume。
- 为 `/resume-latest`、`/resume-session`、`/new-session` 做统一 session 操作面。

验收：

- 重启后同一 session 能恢复 todo、tool results、diff、approval 历史和大输出 artifact。
- transcript 搜索不依赖临时 task_events 投影。
- 任务执行中断后，用户能清楚看到哪些 tool result 已记录，哪些需要重跑。

### Phase 3：权限、沙箱与 Compaction

目标：让 Liora 具备可长期运行的安全边界和上下文边界。

工作项：

- 逐 tool-call approval queue：包含 `tool_call_id`、tool name、args、risk、diff/command preview、decision、resolved_at。
- 支持 always allow / always deny / always ask 规则，并按 workspace/session 持久化。
- 引入 hook contract：PreToolUse、PostToolUse、SessionStart、Stop、PermissionRequest。
- Docker sandbox 默认化，补网络/file policy，明确 patch mode 与 Docker workspace 的关系。
- 新增手动 `/compact`，再做 token 阈值自动 compaction、overflow retry、compact boundary 和 compact 后 resume。

验收：

- 危险 shell、写文件、MCP 外部调用都能在 TUI 面板逐项审查。
- 权限重复响应、取消、超时、任务中断都有可回放事件。
- 长会话达到阈值时能 compact 并继续，compact 后 transcript/resume 不丢 tool result 配对。

### Phase 4：全屏 TUI 主体验

目标：把 Bubble Tea 从骨架推进成 Claude Code/Kimi Code 级别的终端工作台。

工作项：

- 左/主/右或上下多面板：transcript、current tool stream、todo、approval queue、diff preview、status bar。
- 快捷键：transcript 展开/折叠、todo panel、approval focus、diff paging、cancel、redraw。
- 长输出分页与 task artifact viewer。
- session switcher、workbench、MCP status、memory/search 面板。
- line-based renderer 继续作为非 TTY smoke surface。

验收：

- TTY 下默认进入全屏 TUI，支持 running task 期间即时 `/cancel` 和 approval 操作。
- 长 transcript、长 tool output、宽路径不会导致布局错乱。
- `scripts/tui-smoke.sh` 保留非 TTY 断言，另补 pty/expect 或录制式 TUI QA。

### Phase 5：Background tasks、MCP/hooks/plugins 与 ACP

目标：补齐高级工作流和生态入口。

工作项：

- Background task store：TaskList/TaskOutput/TaskStop 工具、output.log、lost/recovered 状态。
- Subagent/worker MVP：先做单机本地队列，不急着多 agent swarm。
- MCP 长连接池、status metrics、reconnect、auth/elicitation UI。
- Plugin/skill manifest 与 hooks/MCP 配置导入。
- ACP server over stdio 或兼容入口，让编辑器能驱动 Liora session。

验收：

- 后台任务重启后能列出、读取输出、标记 lost 或继续观察。
- MCP server 状态在 daemon API/TUI/protocol 中一致。
- ACP 客户端能创建 session、prompt、cancel、接收 session/update，并走同一权限/approval 机制。

### Phase 6：桌面端与产品化

目标：在 core/protocol 稳定后启动桌面入口，而不是把桌面端当作 agent core 的替代品。

工作项：

- 评估 Tauri 2 vs SwiftUI，优先复用 daemon/protocol。
- 做任务工坊、session browser、memory/artifact manager、approval center。
- 增加 daemon 鉴权、自动启动、更新、日志诊断与 crash recovery。

验收：

- 桌面端不直接执行工具、不读写 SQLite，只通过 daemon/protocol。
- CLI/TUI/桌面看到同一 task/session/transcript/artifact 状态。

## 6. 最近建议执行顺序

1. **先跑通 Phase 0**：把当前 dirty worktree 中的 v0.1 变更整理成可审阅状态，确保 exit audit 在开发态持续通过。
2. **再做 Phase 1 protocol**：它会决定后续 TUI、桌面端、ACP 的合同稳定性，越早沉淀越少返工。
3. **随后做 transcript + TodoWrite**：这是对标 Claude Code “能持续工作”的核心，不应等 UI 全部完成后才补。
4. **把权限与 compaction 作为下一组大工程**：二者都触及执行安全和上下文安全，需要单独阶段做设计、测试和 QA。

## 7. 当前风险

- 当前工作区已有大量未提交改动，路线图只记录现状，不代表所有改动已经最终验收。
- `.liora/tool-results` 放在 workspace 内方便模型 read，但 patch-mode 临时 workspace 结束后长期可用性不足，后续应迁移到 daemon/session artifact store。
- Tool-use loop 与文本 planner 双路径会带来 provider 行为差异，后续需要 contract tests 明确哪些 provider 支持 native tools。
- Bubble Tea 与 line-based renderer 共存时，任何事件语义调整都必须同时验证 TTY 与非 TTY surface。
- 参考 Claude Code/Kimi Code 时只吸收工程实践，不复制其 TypeScript/React/Ink 路线；Liora 的 TUI 路线仍以 Go 原生为准。
