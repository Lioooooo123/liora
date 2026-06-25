# Liora MVP Exit Benchmark

这份基准用于判断当前长期目标什么时候可以结束：Liora 不是做到“无限完美”，而是做到一个本地友好、能力可信、性能不拖后腿、UI 足够吸引用户试用的 MVP。

## 产品定位

Liora v0.1 是本地 Mac 上的轻量 agent 工坊。

- 主场景：用户把一个本地 workspace 交给 Liora，让它规划、执行、展示进度、产出 diff，并由用户确认 apply。
- 产品气质：本地友好、可爱、有陪伴感，但不能让角色设定压过能力。
- 优先级：能力和性能优先，UI 用来吸引和解释能力，真正留住用户的是执行可靠性。

## 结束标准

当前目标达到以下基准后，可以认为 v0.1 能力底座完成，长期目标可以结束，后续进入 v0.2 规划。

### 1. 任务能力

- 支持 natural task：用户用自然语言描述任务，Liora 能调用 LLM 规划步骤并执行工具。
- 支持 script task：无需 LLM，直接执行显式步骤，便于调试、测试和自动化。
- 支持工具事件：`list/read/search/write/edit/run/mcp` 等核心工具的 call/result 能被记录并通过 API 查询。
- 支持 bounded replan：natural task 的工具步骤失败后，能把失败上下文交回 LLM 生成一次修正计划，并在同一任务时间线内继续执行。
- 支持 diff-first 写入：任务执行阶段默认可以在副本 workspace 生成 diff，用户确认后再 apply 到真实 workspace。
- 支持取消任务：用户取消当前 daemon 进程内运行中的任务时，任务会停止，终态保持 `cancelled`。

### 2. 性能与可观测性

- 任务事件必须实时流出：planning、plan ready、tool call/result、summary、diff、completed/cancelled/error 应通过 SSE 被客户端按顺序看到。
- SSE 不允许固定高频轮询 SQLite；同进程事件通知优先，跨进程或遗漏通知只用低频 fallback。
- SSE 对长任务必须使用增量游标读取，不能每次唤醒都扫描该任务全部历史事件。
- shell 取消必须清理子进程组，避免后台进程残留污染 workspace。
- 本地 smoke 任务应在普通 Mac 开发机上稳定完成，不出现偶发 daemon 未启动就打 API 的情况。

### 3. 本地数据与记忆

- 使用 SQLite 保存任务、事件、记忆和配置相关数据。
- 基础记忆能力可用：添加、列出、搜索记忆，并能注入 planner 上下文。
- 任务历史可查询，事件可回放，客户端重连后能恢复任务时间线。

### 4. Sandbox 与安全边界

- 默认执行路径必须有 workspace 限制，文件操作不能越过 workspace。
- daemon 和默认 TUI 默认启用 patch mode：写入先发生在临时副本，再由 apply API 显式落到真实 workspace；调试时可用 `LIORA_PATCH_MODE=0` 关闭。
- 支持 Docker sandbox 配置项，但 v0.1 不要求 Docker 成为默认执行方式。
- 支持最小权限审批：`LIORA_PERMISSION=prompt` 下危险 shell、非 patch 写操作和 MCP 外部调用会进入 `waiting_user`，可通过 approve/deny API 继续或取消；完整逐步授权 UI 和长期后台守护安全策略进入 v0.2。

### 5. API 与客户端可用性

- daemon API 覆盖健康检查、创建任务、查询任务、查询事件、SSE、diff、apply、cancel、approval、session/history/timeline。
- CLI 能启动独立 daemon；默认交互 TUI 也能自动拉起 embedded daemon，并通过 smoke script 覆盖核心 API。
- TUI 可以作为开发入口，但 v0.1 结束不要求精致桌面 UI。
- UI 最小要求是“能解释状态、展示进度、展示 diff、允许 apply/cancel”；二次元视觉、白板和 Mac 原生体验进入 v0.2。

### 6. 验证门槛

结束当前目标前必须同时满足：

- `GOTOOLCHAIN=local go test -count=1 ./...` 通过。
- `LIORA_HOME=$(mktemp -d) LIORA_DAEMON_ADDR=127.0.0.1:19089 ./scripts/daemon-smoke.sh "$PWD"` 通过。
- `LIORA_TUI_SMOKE_DAEMON_ADDR=127.0.0.1:19090 LIORA_TUI_SMOKE_LLM_ADDR=127.0.0.1:19091 ./scripts/tui-smoke.sh "$PWD"` 通过。
- `LIORA_EVAL_DAEMON_ADDR=127.0.0.1:19092 LIORA_EVAL_LLM_ADDR=127.0.0.1:19093 ./scripts/coding-eval.sh` 通过。
- smoke、eval 和 CLI 测试覆盖至少一个 natural coding task、一个 multi-file patch task、一个 failed-tool replan task、一个 apply API 调用、一个 large-output truncation task、一个 permission approve/deny task、一个 running cancel task、一个 child-process cleanup case、一个 SSE 事件流、一个 daemon-backed TUI timeline、一个默认 embedded-daemon TUI timeline 和一个 TUI running cancel。
- `implementation-notes.md` 已记录所有重要技术取舍和后续风险。
- `git status --short --branch` 显示本地分支和 `origin/main` 同步且无未提交改动。

## 当前不作为结束阻塞

- 精致 Mac 原生 App。
- 完整角色系统、Live2D、二次元皮肤。
- 白板闪念的完整产品形态。
- 多 agent 调度和长期任务队列。
- Docker sandbox 默认化和权限审批 UI。
- 多供应商 LLM 的完整模型路由、计费统计和重试策略。

这些是 v0.2+ 的产品化方向，不应阻塞 v0.1 能力底座收口。
