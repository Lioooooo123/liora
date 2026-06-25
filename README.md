# Liora

Liora 是一个可运行的最小 Coding Agent MVP，用于验证“工具调用 + 文件修改 + 命令执行 + 执行轨迹”的基础闭环。

它支持两种使用方式：

- 脚本模式：直接输入工具步骤，便于稳定调试。
- 自然语言模式：通过可切换供应商的 LLM client 将用户需求转换成工具步骤，再交给本地执行器运行。
- 交互模式：启动一个轻量 TUI，连续输入自然语言任务并查看计划、工具调用、总结和 diff。
- Daemon 模式：启动本地 Core Daemon，通过 HTTP API 和 SSE 暴露任务工坊能力，供未来 macOS 客户端接入。

## 功能

- 默认使用当前目录作为 workspace。
- 可通过 `-workspace` 指定其他 workspace。
- 按步骤执行基础 coding 工具。
- 支持读取、搜索、写入、替换、运行 Shell、输出 diff。
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

## 支持的步骤

每行一个步骤：

```text
list <path>
tree <path> <max depth>
glob <pattern> <path>
stat <path>
read <path> [start line] [line count]
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
agent > 帮我读取 app.txt，把 old 改成 new，并输出 diff
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
/exit
```

交互界面会展示：

- `You`：本轮用户输入。
- `Plan`：LLM 生成的工具步骤。
- `Tools`：每个工具的执行状态和多行输出预览。
- `Summary`：本轮执行总结。
- `Diff`：文件变更。

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
- `liora.db`：SQLite 本地数据库，保存长期记忆，由 `/memory add <text>` 写入。
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

当前 v0.1 API：

```text
GET  /healthz
POST /v1/tasks
GET  /v1/tasks
GET  /v1/tasks/{id}
GET  /v1/tasks/{id}/events
GET  /v1/tasks/{id}/events/stream
```

## 测试

```sh
go test ./...
```

## 架构分层

- `cmd/coding-agent`：CLI 参数、配置加载和模式选择。
- `internal/daemon`：本地 HTTP API 和 SSE 事件流。
- `internal/task`：任务模型、SQLite 仓储和任务 runner。
- `internal/sandbox`：Shell executor 抽象，支持 local 和 Docker。
- `internal/tui`：交互循环和单轮结果渲染，不直接执行工具。
- `internal/runtime`：连接 Planner 和 Agent，是交互模式的一轮执行编排层。
- `internal/llm`：多供应商 LLM client 和自然语言 Planner。
- `internal/store`：goal、memory、skill 和 MCP 配置的本地持久化。
- `internal/mcp`：stdio MCP client 和 server/tool manager。
- `internal/agent`：解析工具步骤并调度工具。
- `internal/tools`：workspace 内的文件、搜索、目录查看和 Shell 能力。
- `internal/trace`：工具调用轨迹记录和 JSONL 落盘。

## 工具性能策略

- `search` 使用 `rg -F --line-number` 优先执行，大仓库里比纯 Go 递归扫描快；如果系统没有 `rg`，自动回退到 Go walker。
- `glob` 使用 `rg --files -g` 优先执行，最多返回 100 条，避免大目录展开过量。
- `read` 默认最多读取 1000 行 / 100KB，并给每行加行号；可以通过 `read <path> <start> <count>` 分页读取。
- `tree` 默认深度 2，最大深度 6，最多返回 300 行。
- Shell stdout/stderr 会截断，避免 TUI 因超大输出卡死。
- 文件遍历会跳过 `.git`、`node_modules`、`vendor`、`.env*` 等目录或敏感文件。
- 二进制文件读取会被拒绝，避免把不可读内容送进 Planner/TUI。

## 当前边界

- LLM Planner 只允许输出受控工具步骤；如果模型输出未知工具，程序会拒绝执行。
- 当前没有多轮自动反思。LLM 只负责生成初始计划，执行失败后不会再次请求模型重新规划。
- Daemon 当前默认适合本机开发使用，尚未实现本地 token 或 Unix socket 鉴权。
- SSE 当前先输出任务已有事件，后续可扩展为实时订阅式事件总线。
- MCP 当前实现为 stdio JSON-RPC MVP，每次 list/call 会启动一次 server；后续可优化为长连接 session pool。
- Skill 当前以本地 `SKILL.md` 摘要形式注入 Planner，没有实现独立 skill 执行沙盒。
- `list`、`tree`、`glob` 是安全目录查看工具；Planner 会优先用它们处理“看看文件夹里有什么”或“找文件”这类请求。
- TUI 是轻量 Go 实现，借鉴 Kimi Code CLI 的信息结构，使用 Lip Gloss 做样式，不复用原 TypeScript/pi-tui 组件。
- Shell 命令可通过 `LIORA_SANDBOX=docker` 进入 Docker；默认 local 方便无 Docker 环境开发。
- 文件工具已经做 workspace 路径限制；Docker 版本仍需要补危险命令审批、产物 apply 和更完整的资源隔离策略。
- Trace 当前支持内存记录和 JSONL 落盘；任务和记忆已经进入本地 SQLite。

## 下一步

- 将 Docker sandbox 从可配置能力升级为任务默认执行策略。
- 将 daemon SSE 扩展成实时事件订阅。
- 将 task event 和 tool call 事件进一步结构化。
- 建立一组 coding task eval case，支持回归评测。
- 增加执行失败后的 Replan 能力。
