# Liora

Liora 是一个可运行的最小 Coding Agent MVP，用于验证“工具调用 + 文件修改 + 命令执行 + 执行轨迹”的基础闭环。

它支持几种使用方式：

- 脚本模式：直接输入工具步骤，便于稳定调试。
- 自然语言模式：通过可切换供应商的 LLM client 将用户需求转换成工具步骤，再交给本地执行器运行。
- 交互模式：启动 Go 原生 TUI（TTY 下为 Bubble Tea 全屏，管道/CI 下为 line-based），自动拉起本地 Core Daemon，连续输入自然语言任务并查看计划、工具调用、总结和 diff。
- Daemon 模式：启动本地 Core Daemon，通过 HTTP API 和 SSE 暴露任务工坊能力，供未来 macOS 客户端接入。

## Monorepo 结构

Liora 现在按 monorepo 组织，Go core 和未来 desktop 入口分开演进：

```text
apps/
  cli/        当前可用的 liora CLI/TUI/daemon 入口
internal/     Go agent core、daemon、task、sandbox、LLM、store、tools
  tui/        Go 原生 TUI：Bubble Tea 全屏 + line-based fallback
packages/     预留给跨 app 复用的协议、UI 或 eval 包
scripts/      安装、打包、smoke 和 exit audit
docs/         产品、架构和发布文档
```

当前 `apps/cli` 负责安装产物 `liora`，TUI 直接由 Go 原生实现（`internal/tui`，基于 charmbracelet Bubble Tea），与 core 同二进制、无 Node 依赖。未来桌面/Web 入口应通过 Core Daemon API 复用 Go core，而不是重写 agent 执行逻辑。

技术选型见 [Liora 技术选型](docs/tech-stack-selection.md)。

## 功能

- 默认使用当前目录作为 workspace。
- 可通过 `-workspace` 指定其他 workspace。
- 按步骤执行基础 coding 工具。
- 支持读取文本/PDF/DOCX、搜索、写入、替换、运行 Shell、输出 diff。
- 支持 glob、tree、stat、append、edit、mkdir、delete 等更完整的本地工具。
- 搜索和 glob 优先使用 `rg`，不可用时回退到 Go walker。
- 支持持久化 `goal` 和 `memory`，用于给后续轮次补充上下文。
- 支持 SQLite 持久化任务和任务事件。
- 支持可配置 Shell sandbox executor，本机开发默认 `local`，可通过环境变量切到 Docker。
- 支持扫描全局和项目级 `skill`。
- 支持通过 stdio MCP server 列出和调用工具。
- 记录每次工具调用的输入、输出和状态。
- 可将 trace 保存为 JSONL 文件。
- 文件读写会限制在 workspace 内，防止路径穿越。
- 可接入 DeepSeek、OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 和 Gemini generateContent 做自然语言规划。
- 提供基于 Lip Gloss 样式的轻量终端界面，展示 workspace、model、plan、tools、summary 和 diff。

## MVP 结束基准

当前长期目标按 [Liora MVP Exit Benchmark](docs/mvp-exit-benchmark.md) 验收。v0.1 的结束点是本地能力底座可靠可用：任务、事件流、SQLite 持久化、patch/apply、cancel、sandbox 基线和 smoke 验证达标；精致 Mac App、白板和角色系统进入 v0.2+。

## 支持的步骤

每行一个步骤：

```text
list <path>
tree <path> <max depth>
glob <pattern> <path>
stat <path>
read <path> [start line] [line count]
document <path> [start line] [line count]
search <query>
write <path> <content>
append <path> <content>
edit <path> <old text> <new text> [all]
replace <path> <old> <new>
mkdir <path>
delete <path>
run <shell command>
mcp <server> <tool> <json arguments>
diff
```

示例：

```text
list .
glob *.go .
read app.txt 1 80
document "Assignment Question.docx" 1 80
edit app.txt old new
run grep -q "hello new agent" app.txt
diff
```

## 安装

```sh
./scripts/install-local.sh
```

安装后会生成：

```sh
~/.local/bin/liora
```

如果 `~/.local/bin` 不在 PATH 中，把下面这行加入 shell profile：

```sh
export PATH="$HOME/.local/bin:$PATH"
```

安装脚本会把本地 `.env.local` 复制到 `~/.config/liora/.env`。因此安装后可以在任意项目目录直接运行：

```sh
cd /path/to/project
liora
```

## 打包给别人试用

构建可分发 tarball：

```sh
LIORA_VERSION=v0.1.0 ./scripts/package-release.sh
```

验证发布包可以安装并运行：

```sh
./scripts/release-smoke.sh dist/liora_v0.1.0_$(go env GOOS)_$(go env GOARCH).tar.gz
```

更多说明见 [Release Packaging](docs/release.md)。

## 脚本模式

```sh
liora \
  -workspace /path/to/project \
  -trace-out /tmp/liora-trace.jsonl \
  -prompt $'read app.txt\nreplace app.txt old new\nrun grep -q "hello new agent" app.txt\ndiff'
```

## 接入 LLM API

自然语言模式通过 `internal/llm` 的统一 client 生成工具步骤。CLI 和未来客户端都复用同一个 `llm.Config -> llm.NewClient` 入口。

环境变量：

```sh
export LIORA_LLM_PROVIDER="deepseek"
export LIORA_LLM_API_KEY="YOUR_API_KEY"
export LIORA_LLM_BASE_URL="https://api.deepseek.com"
export LIORA_LLM_MODEL="deepseek-v4-pro"
```

也可以复制示例配置：

```sh
cp .env.example .env.local
```

然后把 `.env.local` 里的 `LIORA_LLM_API_KEY` 改成自己的 key。`.env.local` 不会被 Git 跟踪。

支持的 provider：

```text
deepseek          DeepSeek OpenAI-compatible API
openai-chat       OpenAI-compatible Chat Completions
openai-responses  OpenAI Responses API
anthropic         Anthropic Messages API
gemini            Google Gemini generateContent
```

为了兼容旧配置，`OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_MODEL` 仍然可用；新的 `LIORA_LLM_*` 优先级更高。

接入新 API 前可以先做本地诊断；该命令只解析配置，不会请求供应商接口，也不会打印密钥明文：

```sh
liora -doctor
```

运行：

```sh
liora \
  -workspace /path/to/project \
  -natural \
  -trace-out /tmp/liora-trace.jsonl \
  -prompt "读取 app.txt，把 old 改成 new，然后运行 grep 校验并输出 diff"
```

也可以通过参数覆盖：

```sh
liora \
  -workspace /path/to/project \
  -natural \
  -llm-provider "openai-chat" \
  -llm-base-url "https://api.openai.com/v1" \
  -llm-api-key "YOUR_API_KEY" \
  -llm-model "gpt-5.5" \
  -prompt "修复测试失败并输出 diff"
```

如果想使用更低成本模型，可以把 `LIORA_LLM_MODEL` 改为 `deepseek-v4-flash`。

## 交互 TUI

启动：

```sh
cd /path/to/project
liora
```

进入后直接输入自然语言任务：

```text
liora > 帮我读取 app.txt，把 old 改成 new，并输出 diff
```

常用命令：

```text
/help
/goal show
/goal set <text>
/goal clear
/memory list
/memory add <text>
/memory search <query>
/skills
/skill <name>
/mcp
/doctor
/workbench
/spawn <request>
/watch [active|task_id...]
/tasks
/sessions
/timeline [limit]
/transcript [limit]
/history <query>
/last
/tail [lines|task_id lines]
/diff [task_id]
/approvals
/resume <task_id>
/resume-session <session_id>
/resume-latest
/new-session
/clear
/approve [task_id]
/deny [task_id]
/apply
/cancel [task_id]
/exit
```

默认交互入口会在本进程内启动临时 Core Daemon，并通过 HTTP/SSE 复用 daemon/session/task/event 主链路；如果已经有独立 daemon，可使用 `liora -interactive -tui-daemon -daemon-addr 127.0.0.1:18080` 连接它。

TUI 会自动继续当前 workspace 最近的 session，因此重启 `liora` 后可以直接 `/timeline` 或继续输入任务。需要手动接回最近 session 时可用 `/resume-latest`；想从干净上下文开始下一轮任务时可用 `/new-session`，也可以用 `/clear`。

`/timeline [limit]` 展示紧凑会话事件线，`/transcript [limit]` 展开 user、assistant、tool、diff、approval 和 status 内容，适合回看长会话；两者都来自 daemon 的 session timeline API。

`/history <query>` 会在当前 workspace 的会话 timeline 中搜索 user、assistant、tool、diff、approval 和 status 内容，适合重启后找回之前的任务线索。

`/workbench` 展示当前 workspace 下的 session、active tasks 和 recent tasks。`/tasks` 与 `/sessions` 默认也按当前 workspace 过滤，避免多个项目的任务混在一起。

`/doctor` 会在交互界面里显示当前 workspace、core/safety、LLM provider、model、base URL、API key 是否已配置，以及当前 provider 是否支持 tool-use loop；它不会请求远端 API，也不会打印密钥明文。`/config` 是同一命令的别名。

`/spawn <request>` 会在当前 workspace/session 后台启动一个 async task，并立即返回 task id。它适合同时发起多个任务，再用 `/watch` 聚合观察。

`/watch` 会订阅当前 workspace 的 active tasks，直到这些任务的 daemon SSE 结束；也可以用 `/watch task_xxx task_yyy` 显式观察多个任务。它复用 Go client 的多 task event fan-in，适合 TUI 和未来 Mac 客户端共享。

任务 streaming 期间可以直接输入 `/cancel` 中止当前任务；后台任务可用 `/cancel task_xxx` 指定取消。其它命令会在当前任务结束后按顺序执行，避免 `/apply` 或 `/exit` 抢在结果和 diff 之前生效。

CLI 侧也新增显式 session 控制：`liora -interactive -session <session_id> -workspace <path>` 可直接挂载已有会话；`liora -interactive -new-session -workspace <path>` 可固定走新上下文，这对脚本化重放或多入口（桌面端）接入很有用。

交互界面会展示：

- `Core` / `Safety`：当前连接的 agent core 和写入策略。
- 轻量进度行：任务启动、planning、workspace、tool call 和完成状态。
- `Plan`：LLM 生成的工具步骤。
- `Tools`：每个工具的执行状态和多行输出预览。
- `Summary`：本轮执行总结。
- `Diff`：文件变更。

长输出或历史任务可以用 `/tail` 回看最近事件输出，例如 `/tail 80` 查看最近任务的最后 80 行，或 `/tail task_xxx 80` 查看指定任务。

patch-first 任务完成后可以先用 `/diff` 预览最近任务的变更，或用 `/diff task_xxx` 查看指定任务；确认后再输入 `/apply` 写入真实 workspace。

需要人工确认的任务可以用 `/approvals` 查看等待审批队列，再用 `/approve task_xxx` 或 `/deny task_xxx` 处理；不带 task id 时会处理最近任务。

## Goal、Memory 和 Skill

Liora 默认把持久化数据放在：

```text
~/.config/liora
```

也可以通过 `LIORA_HOME` 覆盖：

```sh
LIORA_HOME=/tmp/liora-home liora
```

支持的本地数据：

- `goal.txt`：当前目标，由 `/goal set <text>` 写入。
- `liora.db`：SQLite 本地数据库，保存长期记忆，由 `/memory add <text>` 或 daemon `POST /v1/memories` 写入。
- `memory.jsonl`：旧版记忆文件；首次启动 SQLite store 时会自动导入到 `liora.db`。
- `skills/<name>/SKILL.md`：全局 skill。
- 项目内 `.liora/skills/<name>/SKILL.md`：当前 workspace 的项目级 skill。

每轮自然语言规划时，Liora 会把当前 goal、最近 memory、可用 skill 摘要和已配置 MCP server 名称放进 Planner 上下文。

## MCP 配置

MCP 配置文件为：

```text
~/.config/liora/mcp.json
```

示例：

```json
{
  "servers": {
    "demo": {
      "command": "node",
      "args": ["./mcp-server.js"],
      "env": {
        "TOKEN": "example"
      }
    }
  }
}
```

查看已配置 server 暴露的工具：

```text
/mcp
```

在默认 daemon-backed TUI 里，`/tools` 会同时展示内建工具和 daemon 从 `mcp.json` 读取到的 MCP 工具。

Agent 可执行 MCP 工具步骤：

```text
mcp demo echo {"text":"hello"}
```

也可以显式指定目录：

```sh
liora -workspace /path/to/project -interactive
```

## Core Daemon

启动本地 daemon：

```sh
liora -daemon -daemon-addr 127.0.0.1:18080
```

健康检查：

```sh
curl http://127.0.0.1:18080/healthz
```

创建并同步执行任务：

```sh
curl -s http://127.0.0.1:18080/v1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"workspace":"/path/to/project","prompt":"看看当前目录","natural":true}'
```

读取任务事件：

```sh
curl http://127.0.0.1:18080/v1/tasks/<task-id>/events
curl http://127.0.0.1:18080/v1/tasks/<task-id>/events/stream
curl http://127.0.0.1:18080/v1/tasks/<task-id>/diff
```

### Sandbox 配置

默认模式：

```sh
LIORA_SANDBOX=local liora -daemon
```

Docker 模式：

```sh
export LIORA_SANDBOX=docker
export LIORA_DOCKER_IMAGE=golang:1.24-alpine
export LIORA_DOCKER_NETWORK=none
export LIORA_DOCKER_MEMORY=1g
export LIORA_DOCKER_CPUS=2
liora -daemon -daemon-addr 127.0.0.1:18080
```

Docker executor 会把 workspace 挂载到容器 `/workspace`，使用 `--rm`、`--network none`、内存和 CPU 限制运行 `run` 工具。文件读写工具仍由 Liora 的 workspace guard 负责限制路径；后续版本会把更多文件变更也迁移到 sandbox apply 流程。

daemon 和默认交互 TUI 默认启用 patch mode：任务会在临时 workspace 副本中执行文件写入，真实 workspace 不会被直接修改。客户端可先读取 `/diff`，再调用 `/apply` 确认落地：

```sh
liora -daemon -daemon-addr 127.0.0.1:18080
```

如需回到直接写入的开发模式，可显式关闭：

```sh
LIORA_PATCH_MODE=0 liora
```

任务事件流会等待新事件并持续输出，直到任务进入 completed、failed 或 cancelled。事件里会包含 `sandbox.workspace`，用于显示本次任务使用的是 `direct` 还是 `copy` workspace。

应用 patch 到任务 workspace：

```sh
curl http://127.0.0.1:18080/v1/tasks/<task-id>/apply \
  -H 'Content-Type: application/json' \
  -d '{"patch":"--- a/file.txt\n+++ b/file.txt\n@@ -0,0 +1 @@\n+hello\n"}'
```

`/apply` 会校验 patch 路径不能越过 workspace，写入 `task.patch_applied` 事件，并在 TUI 中显示本次落盘的文件列表。

### 权限审批

默认 `LIORA_PERMISSION=auto`，保持本地开发效率。启用 prompt 模式后，危险 shell、非 patch mode 写操作、MCP 外部调用会让 task 进入 `waiting_user`，并写入 `permission.requested` 事件：

```sh
export LIORA_PERMISSION=prompt
liora -daemon -daemon-addr 127.0.0.1:18080
```

批准继续：

```sh
curl http://127.0.0.1:18080/v1/tasks/<task-id>/approval \
  -H 'Content-Type: application/json' \
  -d '{"decision":"approve"}'
```

拒绝并取消：

```sh
curl http://127.0.0.1:18080/v1/tasks/<task-id>/approval \
  -H 'Content-Type: application/json' \
  -d '{"decision":"deny","reason":"too risky"}'
```

daemon-backed TUI 中可直接使用 `/approvals` 查看等待审批队列，再用 `/approve <task-id>` 和 `/deny <task-id>` 处理指定 task；不带 task id 时会处理最近任务。命令结果会显示 task 状态和后续可查看的历史命令。当前是 task 级授权：批准后该 task 后续需要审批的步骤都会继续执行；逐步授权 UI 留给后续全屏 TUI / Mac 客户端。

取消任务：

```sh
curl http://127.0.0.1:18080/v1/tasks/<task-id>/cancel \
  -H 'Content-Type: application/json' \
  -d '{"reason":"user stopped task"}'
```

对当前 daemon 进程内正在运行的异步任务，`/cancel` 会触发执行 context 取消，并阻止 runner 把任务终态覆盖回 `completed` 或 `failed`。

当前 v0.1 API：

```text
GET  /healthz
GET  /v1/capabilities
GET  /v1/memories
POST /v1/memories
GET  /v1/workbench
GET  /v1/timeline/search
POST /v1/tasks
GET  /v1/tasks
GET  /v1/tasks/events/stream
GET  /v1/tasks/{id}
GET  /v1/tasks/{id}/events
GET  /v1/tasks/{id}/events/stream
GET  /v1/tasks/{id}/diff
POST /v1/tasks/{id}/apply
POST /v1/tasks/{id}/cancel
POST /v1/tasks/{id}/approval
GET  /v1/sessions
POST /v1/sessions
GET  /v1/sessions/{id}
GET  /v1/sessions/{id}/messages
GET  /v1/sessions/{id}/tasks
GET  /v1/sessions/{id}/timeline
```

`GET /v1/memories?q=<query>&limit=N` 支持列出或搜索 SQLite 记忆，`POST /v1/memories` 写入手动记忆。daemon-backed TUI 的 `/memory list|add|search` 会走这组 API，因此未来 Mac 客户端可以复用同一条 memory core path。

`GET /v1/tasks`、`GET /v1/sessions`、`GET /v1/workbench` 和 `GET /v1/timeline/search?q=<query>` 支持 `?workspace=<absolute-path>&limit=N` 过滤。`/v1/workbench` 会一次返回 sessions、active tasks、recent tasks 和 pending approvals，TUI 和未来客户端可用它构建多 workspace / 多 session 工作台。

Go client 层提供 `ListMemories`、`SearchMemories`、`AddMemory`、`StreamEvents(ctx, taskID)` 和 `StreamTaskEvents(ctx, taskIDs)`。后者会通过 daemon 原生 `GET /v1/tasks/events/stream?task_id=...` 单连接订阅多个 task，并聚合成带 `TaskID` 的事件流，TUI 和未来 Mac 客户端可以直接复用它构建多 session / 多任务视图。

## 测试

```sh
GOTOOLCHAIN=local go test -count=1 ./...
LIORA_HOME=$(mktemp -d) LIORA_DAEMON_ADDR=127.0.0.1:19089 ./scripts/daemon-smoke.sh "$PWD"
LIORA_TUI_SMOKE_DAEMON_ADDR=127.0.0.1:19090 LIORA_TUI_SMOKE_LLM_ADDR=127.0.0.1:19091 ./scripts/tui-smoke.sh "$PWD"
LIORA_EVAL_DAEMON_ADDR=127.0.0.1:19092 LIORA_EVAL_LLM_ADDR=127.0.0.1:19093 ./scripts/coding-eval.sh
./scripts/v0.1-exit-audit.sh "$PWD"
```

`v0.1-exit-audit.sh` 是当前长期目标的最终收敛验收入口；开发中可用 `--skip-git-clean` 跳过工作区干净检查，真正结束目标时必须在已推送的干净 `main` 上直接运行通过。

## 架构分层

- `apps/cli`：当前 Go CLI/TUI/daemon 入口，负责参数、配置加载和模式选择。
- `packages`：未来跨 app 复用的协议、UI 和 eval 包。
- `internal/daemon`：本地 HTTP API 和 SSE 事件流。
- `internal/task`：任务模型、SQLite 仓储和任务 runner。
- `internal/sandbox`：Shell executor 抽象，支持 local 和 Docker。
- `internal/tui`：Go 原生 TUI —— TTY 下 Bubble Tea 全屏、非 TTY 下 line-based renderer，不直接执行工具；默认通过 embedded daemon 访问任务事件流。
- `internal/runtime`：连接 Planner 和 Agent，是交互模式的一轮执行编排层。
- `internal/llm`：多供应商 LLM client 和自然语言 Planner。
- `internal/store`：goal、memory、skill 和 MCP 配置的本地持久化；daemon 对 memory 暴露结构化 API。
- `internal/mcp`：stdio MCP client 和 server/tool manager。
- `internal/agent`：解析工具步骤并调度工具。
- `internal/tools`：workspace 内的文件、搜索、目录查看和 Shell 能力。
- `internal/trace`：工具调用轨迹记录和 JSONL 落盘。

## 工具性能策略

- `search` 使用 `rg -F --line-number` 优先执行，大仓库里比纯 Go 递归扫描快；如果系统没有 `rg`，自动回退到 Go walker。
- `glob` 使用 `rg --files -g` 优先执行，最多返回 100 条，避免大目录展开过量。
- `read` 默认最多读取 1000 行 / 100KB，并给每行加行号；可以通过 `read <path> <start> <count>` 分页读取。
- `document` 用同样的分页格式读取 `.pdf` 和 `.docx`；DOCX 使用内置 XML 解析，PDF 依赖系统可用的 `pdftotext`。
- `tree` 默认深度 2，最大深度 6，最多返回 300 行。
- Shell stdout/stderr 会截断，避免 TUI 因超大输出卡死。
- 文件遍历会跳过 `.git`、`node_modules`、`vendor`、`.env*` 等目录或敏感文件。
- 二进制文件读取会被拒绝，避免把不可读内容送进 Planner/TUI。

## 当前边界

- LLM Planner 只允许输出受控工具步骤；如果模型输出未知工具，程序会拒绝执行。
- natural task 工具失败后会最多触发一次 bounded replan；它不是无限自修复循环，避免本地任务失控运行。
- Daemon 当前默认适合本机开发使用，尚未实现本地 token 或 Unix socket 鉴权。
- SSE 已使用同进程事件通知和增量游标；跨进程写库场景保留低频 fallback 轮询。
- MCP 当前实现为 stdio JSON-RPC MVP，每次 list/call 会启动一次 server；后续可优化为长连接 session pool。
- Skill 当前以本地 `SKILL.md` 摘要形式注入 Planner，没有实现独立 skill 执行沙盒。
- `list`、`tree`、`glob` 是安全目录查看工具；Planner 会优先用它们处理“看看文件夹里有什么”或“找文件”这类请求。
- 交互 TUI 由 Go 原生实现（`internal/tui`，基于 charmbracelet Bubble Tea）：TTY 下为全屏 UI，非 TTY（管道/CI/smoke）自动回退 line-based renderer，借鉴 Kimi/Claude 信息流展示方式；与 core 同二进制、无 Node 依赖。默认 `liora` 会自动拉起 embedded daemon，`-tui-daemon` 用于连接外部 daemon；`LIORA_FORCE_GO_TUI` 可强制 line renderer 用于调试。
- Shell 命令可通过 `LIORA_SANDBOX=docker` 进入 Docker；默认 local 方便无 Docker 环境开发。
- 文件工具已经做 workspace 路径限制；daemon 和默认 TUI 默认先产出 patch 再显式 apply，可用 `LIORA_PATCH_MODE=0` 回到直接写入模式；也支持 `LIORA_PERMISSION=prompt` 对危险 shell、非 patch 写操作和 MCP 外部调用做 task 级审批。完整逐步授权 UI 和更严格资源隔离仍留给后续版本。
- Trace 当前支持内存记录和 JSONL 落盘；任务和记忆已经进入本地 SQLite。

## 下一步

- 将 Docker sandbox 从可配置能力升级为任务默认执行策略。
- 将 task event 和 tool call 事件进一步结构化。
- 将 diff/apply 确认体验升级到全屏 TUI 或桌面端确认 UI。
- 建立一组 coding task eval case，支持回归评测。
- 增加执行失败后的 Replan 能力。
