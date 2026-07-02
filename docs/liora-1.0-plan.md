# Liora 1.0 计划

> 更新日期：2026-07-01
> 目标：先把 Liora 做成一个可靠的本地 coding agent，再在同一套 core 上扩展定时任务、用户个性化、上下文治理和 hook 生态。

## 1. 1.0 定位

Liora 1.0 的核心不是“多 agent swarm”，也不是套一个通用 agent 框架。1.0 要交付的是一个用户敢长期交给真实仓库使用的本地 coding agent：

- 能稳定理解任务、调用工具、运行测试、产出 diff，并让用户确认后落盘。
- 能恢复长期会话，知道当前计划、历史工具结果、用户偏好和上下文边界。
- 能把危险动作、后台任务、定时任务和 hook 都纳入可观察、可取消、可审计的 daemon task 模型。
- CLI/TUI/未来桌面端都只消费 daemon/protocol，不复制 agent core。

一句话：**1.0 先成为可靠的本地研发工作台，再谈复杂子 agent 协作。**

## 2. 设计原则

- **自研主干**：继续沿用 Go core、daemon、SQLite、HTTP/SSE、patch-first、Bubble Tea TUI；Eino/ADK 只作为可选参考或 adapter 实验，不接管主执行链。
- **事件是一等公民**：tool、todo、transcript、approval、hook、schedule、subagent 都必须落事件和持久化记录，UI 只做投影。
- **默认安全**：真实 workspace 默认不在执行阶段直接写入；危险 shell、外部 MCP、非预期网络和 hook 副作用必须可见、可拒绝、可回放。
- **上下文有边界**：长会话必须有 transcript、artifact、summary、compact boundary 和 token budget，不靠无限塞历史。
- **用户个性化可解释**：偏好、记忆、规则和自动化必须能查看、编辑、禁用，不能变成模型隐形指令。

## 3. 1.0 横切基线

这些不是单独功能，而是所有 1.0 能力落地前必须先稳定的地基。

### 3.1 Protocol / API Contract

目标：先稳定 daemon contract，再让 TUI、桌面端、多线程对话、per-thread model、subagent、schedule 和 hook 复用。

工作项：

- `packages/protocol` 必须覆盖 task、session、conversation thread、thread model config、cross-thread message、event、transcript、artifact、approval、schedule、hook、memory、capability 类型。
- Go daemon 与 TS protocol 通过同一组 fixture 对齐，包含 SSE `event/id/data`、多 task envelope、多 thread envelope、错误响应和 contract version。
- 所有新增 event 都要进入 event versioning：新增字段向后兼容，破坏性变更必须升级 contract version。
- Go `internal/daemonclient` 与 TS client 都不得暴露手拼路径作为主要入口。
- Bubble Tea TUI 与 line-based TUI 必须消费同一 daemon event 语义；line-based smoke 继续作为非 TTY 回归基线。

验收：

- `pnpm test --filter @liora/protocol`、Go protocol fixture 测试和 daemonclient 测试同时通过。
- 任一新增 daemon event 都有 Go fixture、TS parser/reducer 测试和至少一个 UI/daemonclient 消费断言。

### 3.2 Data Model and Migrations

目标：1.0 新增的持久化模型都能从用户已有 `liora.db` 平滑升级。

工作项：

- 为 transcript、todo、artifact、approval item、schedule、hook、memory type、conversation thread、thread model binding、thread relation、cross-thread message、subagent relation、compact boundary 增加 schema version 和幂等 migration。
- 每个 migration 要有旧库 fixture 测试，覆盖重复启动、部分迁移失败、损坏数据库诊断和只读备份建议。
- 新增表必须有清晰保留策略：哪些长期保存，哪些可清理，哪些只保存引用。
- 回滚不要求自动降级 schema，但必须提供恢复路径：备份位置、doctor 诊断、导出方式和安全删除方式。

验收：

- 用旧版 `liora.db` fixture 启动新 daemon 后，核心数据可读，新表可写，重复启动不重复迁移。
- `liora -doctor` 能报告 schema version、migration 状态和可恢复错误。

### 3.3 Security, Privacy, and Trust Baseline

目标：在 schedule、hook、subagent 进入可用前，先完成本地控制面和自动化安全底线。

工作项：

- daemon 常驻场景必须有本地鉴权、Unix socket 或等价 capability gate；apply、approval、memory、artifact、schedule、hook API 不允许长期裸奔。
- Docker sandbox 或等价隔离策略要成为自动化默认基线；网络访问默认 deny，必要时通过 domain allowlist 或审批放行。
- 所有来自 repo 文件、MCP/tool 输出、hook 输出、artifact、transcript、memory candidate 的内容都标记为 untrusted，不得提升权限或改写安全策略。
- memory/transcript/artifact 需要隐私策略：secret/PII 检测、redaction、delete/export、retention TTL、workspace 隔离、文件权限基线；`credential_hint` 只能保存提示，不能保存密钥正文。
- 跨线程消息默认只共享摘要、artifact 引用和显式转发内容，不隐式复制完整 prompt、secret、memory 或 approval rule；跨 workspace 转发必须显式授权。
- 子任务默认最小权限：child task 不能继承超过 parent scope 的 path、network、MCP、approval 权限；child task 不能替 parent 自动 approve。
- 发布链路需要 supply-chain 基线：release checksum、package provenance、依赖扫描或 SBOM、MCP/hook manifest 审查。

验收：

- 未鉴权请求不能调用 apply、approval、memory、artifact、schedule、hook 等敏感 API。
- 恶意 repo 文本、MCP 输出、hook 输出、memory candidate 不能绕过 approval、修改 policy 或泄露密钥。
- schedule/hook/subagent 的危险动作在无用户授权时 pause，而不是静默执行。

### 3.4 Queue, Concurrency, and User Input

目标：明确 foreground turn、后台任务、子任务、审批等待和用户补充信息之间的调度规则。

工作项：

- foreground turn 默认进入 session queue；同一 session 的 running / waiting_user 不被后续 foreground 输入抢跑。
- `waiting_user` 必须区分 approval 和普通 user input；下一条用户消息只在明确处于 user-input wait 时路由为回答。
- `/spawn`、subagent、schedule 是后台任务，但必须有并发上限、资源上限、取消句柄和 lost/recovered 状态。
- 多 conversation thread 可以在同一 workspace 并行执行，但单个 thread 内 foreground turn 仍按队列串行化；跨 thread 并发由 daemon scheduler 统一限流。
- schedule catch-up 默认不无限补跑；同一 workspace 的 catch-up、subagent 和 foreground queue 要有冲突规则。
- approval、user input、schedule trigger 和 hook timeout 都要有 expiry / stale 状态。

验收：

- daemon 重启后能恢复 queued、waiting_user、background、schedule-triggered task 的可解释状态。
- 同一 session 中 queued foreground turn、pending approval、pending user input 和 running subagent 不会互相覆盖终态。
- 同一 workspace 中多个 conversation thread 并行运行时，慢工具、大输出或等待用户输入不会阻塞其它 thread 的事件流。

### 3.5 Threaded Conversations and Go Concurrency

目标：1.0 支持多线程对话，并允许线程之间交流；这里的 thread 是 Liora 的会话线程，不是操作系统线程。

工作项：

- 在产品语义上把现有 session 升级为 conversation thread：每个 thread 有独立 transcript、todo、context budget、approval queue、active task 和 lifecycle。
- 支持同一 workspace 创建多个 thread、切换 thread、重命名 thread、归档 thread，并在 workbench/TUI 中展示 active、waiting、queued、completed 状态。
- 每个 thread 可以绑定独立的 provider/model/base URL/profile：例如一个 thread 用强推理模型做架构，一个 thread 用低成本模型做检索或批量修改；未显式设置时继承 workspace/global default。
- 支持 thread 之间通过 daemon 发送消息：`thread_message.sent`、`thread_message.received`、`thread_link.created` 等事件必须同时进入源 thread 和目标 thread 的 transcript。
- 跨线程交流只传递结构化 message、summary、artifact reference、task reference 或 explicit handoff，不共享可变内存和隐式 prompt。
- Go 实现优先使用 `context.Context`、goroutine、channel、`errgroup`、bounded worker pool、SSE fan-in 和 backpressure；任一 thread 的 cancel、timeout、panic recovery 不应影响 sibling thread。
- daemon scheduler 统一管理 per-thread、per-workspace、global 并发上限；大输出通过 artifact store 分页，事件流通过 bounded channel 和 drop/overflow 策略可观测。
- TUI 支持 thread switcher、thread inbox、cross-thread mention/handoff；line-based TUI 至少提供 `/threads`、`/thread <id>`、`/thread-send <id> <message>`。

验收：

- 用户能在同一 workspace 创建至少 3 个 conversation thread 并行运行，并在 TUI 中切换观察。
- 用户能在不同 thread 选择不同模型，并在 workbench、transcript metadata 和 trace 中看见实际使用的 provider/model。
- 一个 thread 可以向另一个 thread 发送摘要、artifact 引用或任务交接消息；daemon 重启后源/目标 transcript 都能恢复这条交流记录。
- 取消或失败一个 thread 不会取消 sibling thread；达到并发上限时新任务进入 queued 或 waiting_resource，状态可见。
- Go race/leak 风险有回归覆盖：并发 thread eval、取消隔离、SSE fan-in、bounded channel/backpressure 至少有本地 deterministic 测试或 smoke。

### 3.6 Provider Client and Model Routing

目标：不同 conversation thread 可以使用不同模型，provider client 支持高并发、多配置、可观测和可恢复。

工作项：

- `internal/llm` 从单一进程级 client 升级为 provider registry + per-request resolved config：每次 generate/tool-loop 请求都显式携带 provider、model、base URL/profile、tool-use capability、timeout、retry policy、token budget 和 trace labels。
- provider client 必须并发安全，不能把 model、API key、base URL、tool-use support、retry state 等可变配置挂在共享全局状态上。
- 支持 thread/model binding：global default、workspace default、thread override、task override 四级解析，解析结果写入 task metadata 和 transcript/trace，便于 resume 后复现。
- 不同 provider/model 的限流、重试、熔断、成本估算和 token/latency 指标要分桶统计，避免一个模型 429 或超时拖垮其它 thread。
- provider capability 要可查询：是否支持 native tool-use、streaming、vision、long context、JSON schema、max output tokens；planner fallback 根据 resolved model capability 自动选择路径。
- TUI 支持 `/model` 查看当前 thread 模型，`/model set <profile>` 或等价入口切换 thread model；line-based smoke 至少覆盖查询和切换。
- schedule/subagent 创建 task 时默认继承触发 thread 的 model binding，但可以显式 override；child thread 不应静默升级到更贵或权限更高的模型。

验收：

- 同一 workspace 中两个 thread 分别使用 fake strong model 和 fake cheap model 并行执行，task metadata、transcript 和 trace 都记录各自 provider/model。
- 一个 provider 返回 429/5xx 时只影响使用该 provider/model/profile 的 thread；其它 provider/model 的 thread 继续运行。
- doctor/config 能列出 provider profile、capability、默认模型、thread override 和 redacted credential 状态。

### 3.7 Support, Doctor, and Release Gates

目标：1.0 的发布不是“文档说完成”，而是有固定 audit、诊断和安装验证。

工作项：

- 新增 `scripts/liora-1.0-audit.sh`，串联 Go tests、protocol tests、daemon smoke、TUI smoke、coding eval、migration fixture、security adversarial eval、package smoke。
- `liora -doctor`、`/doctor`、`/config` 要覆盖 provider profile、tool-use support、per-thread model override、schema/migration、daemon auth、sandbox/network policy、MCP status、schedule/hook 状态，并继续 redaction API key。
- release 继续覆盖 GitHub/npm lazy build 和 tarball 安装路径；installed-binary smoke 必须从任意 workspace 启动。
- 诊断包只导出 redacted metadata、event summary、schema status 和日志摘要，不导出 token、cookie、API key、原始 PII。

验收：

- 干净 checkout 运行 `GOTOOLCHAIN=local ./scripts/liora-1.0-audit.sh "$PWD"` 通过。
- release tarball、npm GitHub install 和本地源码 install 都能启动 `liora -doctor` 并通过基础 smoke。

## 4. 1.0 能力柱

### 4.1 Tool-use Loop 稳定化

目标：让原生 tool-call loop 成为默认主路径，文本 planner fallback 只是兼容路径。

工作项：

- 统一 native tool loop 与 planner fallback 的工具语义、错误语义和 trace 语义。
- 工具结果成对回灌：每个 tool_call 都必须有 tool_result，失败也作为结构化结果回灌。
- 保留并继续强化重复失败去重、最大 turn、replan、provider retry、输出预算裁剪。
- 增加 provider/model、token、latency、retry count、stop reason、replan reason 的可观测字段。
- 将大输出从临时 workspace `.liora/tool-results` 迁移到 daemon/session artifact store。

验收：

- 同一 deterministic coding eval 能在 native tool provider 和 fallback provider 下产出同一组文件变更或同一 contract-level 结果。
- 同一 workspace 的不同 thread 使用不同 provider/model 时，tool loop、planner fallback、trace 和 transcript 都能正确归因到各自 resolved model。
- 重复失败不会耗尽 turn；失败原因在 `/tail`、timeline 和 trace 中可定位。
- 长输出不会污染模型上下文，用户仍能分页查看完整 artifact。

### 4.2 Todo / Plan 成正式工具

目标：让计划不只是日志，而是模型和 UI 共享的任务状态。

工作项：

- 新增 `todo_write` / `todo_read`，或等价的 session plan 工具。
- todo item 至少包含 `id`、`content`、`status`、`priority`、`source_task_id`、`updated_at`。
- TUI 增加 todo panel，line-based TUI 至少支持 `/todo` 查看。
- task 完成前检查 todo：如果仍有 `in_progress` 或关键 `pending`，需要模型解释或继续执行。

验收：

- 一个多步骤任务执行过程中，用户能看到计划从 pending 到 in_progress 到 completed。
- 重启后 todo 可恢复，不依赖当前内存状态。

### 4.3 Transcript / Resume 做实

目标：session resume 恢复的是工作上下文，不只是历史事件列表。

工作项：

- 新增 materialized `transcript_entries`：user、assistant、tool_call、tool_result、todo、diff、approval、hook、schedule、compact_boundary。
- 把 task events 继续保留为底层事实，把 transcript 作为面向 resume/search/export 的稳定投影。
- 支持 `/transcript`、`/resume-session`、`/resume-latest` 从 transcript 恢复模型上下文。
- transcript 记录 artifact 引用，不内嵌超长输出。

验收：

- daemon 重启后，用户能恢复同一 session 的 assistant 消息、工具调用、工具结果、diff、approval 和 todo。
- timeline 搜索和 transcript 搜索不再依赖临时拼接 task event 文本。

### 4.4 上下文治理

目标：长会话不会因为历史膨胀、无关记忆或大输出把 agent 拖垮。

工作项：

- 增加上下文预算模型：system、user、transcript、memory、tool result、artifact preview 分桶计量。
- 新增手动 `/compact`，再做自动 compact 阈值。
- compact 产物写入 `compact_boundary`，保留原始 transcript 和摘要之间的映射。
- 引入 context packer：按当前任务选择相关 transcript、todo、memory、artifact preview，而不是全量灌入。
- 增加 context diagnostics：当前 prompt 为什么包含这些记忆、历史和工具结果。

验收：

- 长 session 经过 compact 后仍能继续执行，tool_call/tool_result 配对不丢。
- 用户可以查看最近一次 prompt context 的来源摘要。

### 4.5 权限升级到 Tool-call 级

目标：权限从 task 级 prompt 变成逐动作的 approval queue。

工作项：

- 新增 approval item：`tool_call_id`、tool name、args preview、risk、command/diff preview、decision、decided_by、resolved_at。
- 支持 always allow、always deny、always ask，并按 workspace/session/tool/risk scope 持久化。
- 写文件、危险 shell、外部 MCP、网络访问、hook 副作用进入统一权限策略。
- TUI 增加 approval queue；line-based TUI 保留 `/approvals`、`/approve`、`/deny`。

验收：

- 一个任务中多个危险动作可以逐项审批，不需要重跑整个 task 才继续。
- 重复响应、取消、超时、daemon 重启后的未决审批都有确定行为。

### 4.6 Background Task / Subagent

目标：先做本地后台 task 和子 task，不急着做复杂 supervisor。

工作项：

- task model 增加 parent/child/thread 关系：`parent_task_id`、`parent_thread_id`、`child_thread_id`、`subagent_name`、`role`、`scope`。
- 新增模型可调用工具：`Task`、`TaskOutput`、`TaskStop`。
- 子 agent 本质是 parent task 创建 child task，仍走同一套 runner、permission、patch mode、artifact、events。
- child task 默认继承 parent 的 workspace/thread scope，但不自动继承 always-allow 规则、网络权限或外部 MCP 权限。
- 后台任务输出写入 artifact store，支持分页读取、tail、lost/recovered 状态。
- TUI 支持 subagent/task/thread 列表、聚合 watch、按 child task 或 child thread 展开输出。

验收：

- 主任务可以派生一个后台子任务，读取其输出，并在需要时停止。
- daemon 重启后能列出未完成/丢失/已完成的后台任务和输出日志。

### 4.7 定时任务与自动化

目标：把“稍后提醒/定期执行/周期检查”纳入同一套 daemon task，而不是另起一套脚本系统。

工作项：

- 新增 schedule model：one-shot、interval、cron-like、本地时区、quiet hours、enabled/disabled。
- schedule 触发时创建 daemon task，带 `trigger=schedule`、`schedule_id` 和 workspace/session scope。
- 支持用户命令：`/schedule add`、`/schedule list`、`/schedule pause`、`/schedule resume`、`/schedule delete`。
- 定时任务默认 patch-first；涉及 shell、网络、MCP、文件写入仍走 approval/hook 策略。
- unattended schedule 遇到危险动作默认 pause，不静默 approve；approval 有过期时间，catch-up 默认 run-once 或 skip，不无限补跑。
- 支持 missed-run 策略：skip、run-once、catch-up limited，并有 per-workspace 并发和频率上限。

验收：

- 用户能创建一次性和周期性 coding/检查任务。
- daemon 重启后 schedule 仍存在，下一次触发时间可见。
- 定时任务的所有输出、diff、approval 和失败原因都进入同一 timeline/transcript。

### 4.8 用户个性化

目标：Liora 记住用户偏好，但用户始终能看见和修改。

工作项：

- 区分 memory 类型：fact、preference、workflow、project_rule、credential_hint、do_not_do；`credential_hint` 只能保存定位线索或获取方式，不能保存密钥正文。
- 自动记忆必须先作为候选，不直接永久写入；用户可 approve/edit/reject。
- 支持 workspace 级和全局级偏好，workspace 级优先。
- TUI 提供 `/memory`、`/preferences`、`/rules` 视图。
- context packer 引用偏好时要记录来源，便于解释“为什么这么做”。
- 支持记忆导出、删除、禁用和按 workspace 隔离。

验收：

- 用户可以设置“默认先跑测试”“不要自动 commit”“某仓库优先用某命令”等偏好。
- 用户可以查看本轮任务使用了哪些记忆和偏好。

### 4.9 Hook 系统

目标：hook 是系统能力，不是散落脚本；它要接入权限、事件、transcript 和上下文治理。

工作项：

- 定义 hook contract：`SessionStart`、`PreToolUse`、`PostToolUse`、`PermissionRequest`、`TaskComplete`、`TaskFail`、`ScheduleTrigger`、`Compact`。
- hook 输入输出使用 JSON schema；hook 可以返回 allow/deny/modify/context/warning，但默认不能直接写真实 workspace。
- hook 执行有 timeout、输出预算、错误隔离和 event 记录。
- hook 副作用进入 permission 策略；高风险 hook 默认 ask。
- 支持 workspace `.liora/hooks` 与全局 hooks；repo-provided hook 默认 quarantined，需要用户显式启用。
- hook 默认无 secret env、无网络、受 sandbox 限制；持久 always-allow 只允许用户信任的全局 hook 或已审查 manifest。
- hook 输出按 untrusted content 处理，不能直接修改 policy、approval decision 或 system prompt。

验收：

- 用户能配置一个 PreToolUse hook 拦截危险命令，或一个 TaskComplete hook 发通知/写报告。
- hook 失败不会让 daemon 崩溃，失败事件和 stderr 可回看。

### 4.10 MCP / Plugin / ACP 状态面

目标：外部扩展能力必须和内建工具一样可见、可诊断、可限制。

工作项：

- MCP server 状态进入 daemon API、TUI 和 protocol：enabled、startup latency、last error、tool count、auth/elicitation 状态。
- MCP 长连接池、重连和失败隔离不能阻塞内建工具可见性。
- Plugin/skill manifest 与 hook/MCP 配置导入必须有来源、版本、权限声明和禁用入口。
- ACP 作为 1.0 后段或明确延后项；如果进入 1.0，必须复用同一 session、permission、transcript、protocol。

验收：

- 一个 MCP server 启动失败不会影响内建工具或其它 MCP server。
- 用户能在 `/doctor`、`/tools` 或未来面板看到 MCP/plugin 状态和禁用入口。

### 4.11 Eval Harness

目标：用固定评测约束 1.0，不靠主观“感觉更聪明”。

工作项：

- 1.0-alpha 先建立 eval suite 骨架；之后每个能力 landing 必须同时补 deterministic fake-provider eval 和 daemon/TUI smoke。
- 扩展 `scripts/coding-eval.sh` 或新增 `scripts/liora-1.0-audit.sh` 覆盖：tool loop、todo、resume、多线程对话、per-thread model、cross-thread message、approval、subagent、schedule、hook、context compact。
- 每个 eval case 都使用临时 workspace、fake/real provider 可切换、可复现 trace。
- 增加成本指标：turn count、tool count、token estimate、latency、retry count。
- 增加安全回归：no-write-before-apply、approval-bypass prevention、malicious repo prompt injection、malicious MCP/hook output、secret redaction、cross-thread data leak、schedule abuse、child-task escalation。
- 后续逐步接 Terminal-Bench / SWE-bench 风格任务，但 1.0 先以 Liora 自己的本地能力闭环为准。

验收：

- 1.0 release 前 eval suite 全绿。
- 任一核心能力回退会在本地测试或 smoke 中暴露。

## 5. 1.0 Acceptance Evidence Matrix

| Pillar | Surface | Required evidence |
| --- | --- | --- |
| Protocol/API contract | Go daemon, `internal/daemonclient`, `packages/protocol` | Go fixture + TS parser/reducer/client tests; event version asserted |
| Data migrations | SQLite store, doctor | Old `liora.db` fixture migrates idempotently; doctor reports schema state |
| Tool-use loop | Runtime, trace, eval | Native and fallback deterministic evals; repeated failure short-circuit; token/latency/retry fields |
| Todo/Plan | Tool schema, transcript, TUI | `todo_write/read` eval; restart restores todo; TUI and line command render same state |
| Transcript/Resume | SQLite, daemon API, TUI | Restart/resume restores user/assistant/tool/diff/approval/todo/artifact references |
| Context governance | Context packer, compact | Manual compact eval; compact boundary persisted; prompt context diagnostics visible |
| Threaded conversations | Scheduler, transcript, daemon SSE, TUI | 3+ threads run concurrently; cross-thread message persists in both transcripts; cancellation isolated; bounded worker/backpressure tested |
| Provider/model routing | `internal/llm`, daemon task metadata, TUI, doctor | Per-thread model eval; provider failure isolation; capability/profile visible with redacted credentials |
| Tool-call approval | Daemon API, TUI, events | Multiple pending approvals, duplicate response, expiry, restart recovery, deny path |
| Background/subagent | Task store, tools, SSE | Parent creates child, TaskOutput reads artifact, TaskStop cancels, least-privilege enforced |
| Schedule | Scheduler, task runner, TUI | One-shot and interval trigger; missed-run policy; dangerous action pauses for approval |
| Personalization | Memory/rules APIs, context packer | Candidate approval flow; delete/export; credential_hint secret rejection |
| Hook | Hook runner, permission, events | Hook timeout/failure isolation; quarantined repo hook; no secret env by default |
| MCP/plugin/ACP | Capabilities, doctor, protocol | MCP partial failure isolated; status visible; plugin manifest permissions visible |
| Security baseline | Daemon, sandbox, artifact store | Auth/socket gate; default-deny network eval; redaction/retention/delete/export checks |
| Release | Scripts, package, installed binary | `scripts/liora-1.0-audit.sh`, release smoke, installed-binary smoke from arbitrary workspace |

## 6. 版本阶段

### 1.0-alpha：执行链可信

范围：

- Protocol/API contract hardening：event version、fixture、Go/TS client、SSE reducer。
- Data migration harness 和 1.0 eval/audit 骨架。
- Security baseline 第一刀：daemon auth/socket 方案、doctor schema/auth/sandbox 诊断、默认安全策略落文档和测试。
- native tool loop 与 fallback 语义收敛。
- todo 工具和 TUI todo 面板。
- transcript_entries MVP。
- artifact store MVP。
- conversation thread data model MVP：兼容现有 session，补 thread metadata、thread model binding、thread event envelope 和 cross-thread message fixture。
- provider registry / per-request resolved model config MVP，先让 task metadata、trace 和 doctor 能看见实际 provider/model。

退出标准：

- 一个多文件 coding task 能稳定计划、执行、测试、产出 diff、恢复 transcript。
- 失败、重试、replan、token/latency 基础指标可见。
- protocol tests、migration fixture、deterministic coding eval 和 no-write-before-apply 安全回归通过。

### 1.0-beta：长期会话可信

范围：

- 手动 `/compact` 与 context packer。
- tool-call approval queue。
- always allow/deny/ask 规则。
- 多线程对话执行：thread switcher、per-thread model switch、cross-thread message、bounded scheduler、取消隔离。
- background task / subagent MVP。
- schedule MVP，但危险 unattended action 默认 pause。
- Docker/network/file policy 默认化到自动化路径。

退出标准：

- 长 session 能 compact 后继续。
- 不同 thread 可以使用不同模型，provider 失败和限流互相隔离。
- 后台子任务可创建、观察、停止、恢复输出。
- 多个 conversation thread 可并行推进，跨线程交流可恢复，单个 thread 失败不影响其它 thread。
- 定时任务能在 daemon 重启后继续按计划触发。
- approval、schedule、subagent、hook 的安全回归没有 bypass。

### 1.0-rc：自动化与个性化可信

范围：

- 用户个性化：偏好、规则、自动记忆候选。
- hook contract 与 hook runner。
- context diagnostics。
- MCP/plugin 状态面、doctor/config、release diagnostics。
- TUI 主体验补齐 approval、todo、subagent、schedule、memory 面板。

退出标准：

- 用户能解释和控制 Liora 为什么这么做。
- hook、schedule、memory、approval 全部可观察、可禁用、可回放。
- repo hook、MCP、memory candidate、artifact prompt injection 都按 untrusted content 处理。

### 1.0-stable：发布基线

范围：

- release packaging、doctor、audit、eval、文档、迁移。
- daemon 本地鉴权或 Unix socket 策略。
- 失败诊断和 recovery 指南。

退出标准：

- 干净 checkout 通过 `GOTOOLCHAIN=local ./scripts/liora-1.0-audit.sh "$PWD"`。
- 安装包在任意 workspace 可运行。
- release tarball、npm GitHub install、本地源码 install 都通过 installed-binary smoke。
- 用户能用文档完成配置、诊断、迁移恢复、导出、删除、卸载。

## 7. 1.0 不做

- 不做复杂多机 agent swarm。
- 不把 conversation thread 等同于 OS thread，也不暴露共享可变内存给模型或 hook。
- 不把 Eino/Google ADK/LangChainGo 作为主 runtime。
- 不让桌面端复制 agent core。
- 不做团队协作权限系统。
- 不默认自动 commit/push。
- 不让定时任务绕过 approval、patch-first 或 hook 安全边界。
- 不让 repo-provided hook 默认获得信任。
- 不在 memory 中保存真实密钥。

## 8. 最近执行顺序

1. 先完成 Protocol/API contract hardening，锁住 event version、fixtures、Go/TS client 和 SSE reducer。
2. 建 1.0 eval/audit 骨架和 migration fixture，让后续能力都有可复现验收。
3. 补安全基线：daemon auth/socket、sandbox/network/file policy、secret redaction、untrusted content 规则。
4. 完成 `transcript_entries` 与 artifact store，解决 resume 和大输出长期可用性。
5. 做 conversation thread 数据模型、thread model binding、thread event envelope、cross-thread message 和 bounded scheduler。
6. 改造 provider client 为 provider registry + per-request resolved config，支持 per-thread model、限流/重试/指标隔离。
7. 接 `todo_write` / `todo_read`，把计划变成正式状态。
8. 做 tool-call approval queue 和 queue/user-input wait，把权限与等待状态从 task 级细化。
9. 做 context packer 与手动 `/compact`，避免长会话失控。
10. 做 background task/subagent MVP，复用 daemon task/thread 模型并限制 child capability。
11. 做 schedule MVP，把定时任务接入同一套 task/event/transcript，危险动作默认 pause。
12. 做用户个性化、hook、MCP/plugin 状态面，让 Liora 从“会做任务”变成“按用户方式安全做任务”。
13. 扩展 eval suite 和 release smoke，所有能力都要有本地可复现验证。
