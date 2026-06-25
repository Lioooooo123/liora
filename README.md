# Liora

Liora 是一个可运行的最小 Coding Agent MVP，用于验证“工具调用 + 文件修改 + 命令执行 + 执行轨迹”的基础闭环。

它支持两种使用方式：

- 脚本模式：直接输入工具步骤，便于稳定调试。
- 自然语言模式：通过 OpenAI-compatible LLM API 将用户需求转换成工具步骤，再交给本地执行器运行。
- 交互模式：启动一个轻量 TUI，连续输入自然语言任务并查看计划、工具调用、总结和 diff。

## 功能

- 默认使用当前目录作为 workspace。
- 可通过 `-workspace` 指定其他 workspace。
- 按步骤执行基础 coding 工具。
- 支持读取、搜索、写入、替换、运行 Shell、输出 diff。
- 支持持久化 `goal` 和 `memory`，用于给后续轮次补充上下文。
- 支持扫描全局和项目级 `skill`。
- 支持通过 stdio MCP server 列出和调用工具。
- 记录每次工具调用的输入、输出和状态。
- 可将 trace 保存为 JSONL 文件。
- 文件读写会限制在 workspace 内，防止路径穿越。
- 可接入 OpenAI-compatible `/chat/completions` API 做自然语言规划。
- 提供基于 Lip Gloss 样式的轻量终端界面，展示 workspace、model、plan、tools、summary 和 diff。

## 支持的步骤

每行一个步骤：

```text
list <path>
read <path>
search <query>
write <path> <content>
replace <path> <old> <new>
run <shell command>
mcp <server> <tool> <json arguments>
diff
```

示例：

```text
list .
read app.txt
replace app.txt old new
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

自然语言模式通过 OpenAI-compatible Chat Completions API 生成工具步骤。当前本地 `.env.local` 已按 DeepSeek 配置，文件不会被 git 跟踪。

环境变量：

```sh
export OPENAI_API_KEY="YOUR_API_KEY"
export OPENAI_BASE_URL="https://api.deepseek.com"
export OPENAI_MODEL="deepseek-v4-pro"
```

也可以复制示例配置：

```sh
cp .env.example .env.local
```

然后把 `.env.local` 里的 `OPENAI_API_KEY` 改成自己的 key。`.env.local` 不会被 Git 跟踪。

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
  -llm-base-url "https://api.openai.com/v1" \
  -llm-model "deepseek-v4-pro" \
  -prompt "修复测试失败并输出 diff"
```

如果想使用更低成本模型，可以把 `OPENAI_MODEL` 改为 `deepseek-v4-flash`。

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

## 测试

```sh
go test ./...
```

## 架构分层

- `cmd/coding-agent`：CLI 参数、配置加载和模式选择。
- `internal/tui`：交互循环和单轮结果渲染，不直接执行工具。
- `internal/runtime`：连接 Planner 和 Agent，是交互模式的一轮执行编排层。
- `internal/llm`：OpenAI-compatible 客户端和自然语言 Planner。
- `internal/store`：goal、memory、skill 和 MCP 配置的本地持久化。
- `internal/mcp`：stdio MCP client 和 server/tool manager。
- `internal/agent`：解析工具步骤并调度工具。
- `internal/tools`：workspace 内的文件、搜索、目录查看和 Shell 能力。
- `internal/trace`：工具调用轨迹记录和 JSONL 落盘。

## 当前边界

- LLM Planner 只允许输出受控工具步骤；如果模型输出未知工具，程序会拒绝执行。
- 当前没有多轮自动反思。LLM 只负责生成初始计划，执行失败后不会再次请求模型重新规划。
- MCP 当前实现为 stdio JSON-RPC MVP，每次 list/call 会启动一次 server；后续可优化为长连接 session pool。
- Skill 当前以本地 `SKILL.md` 摘要形式注入 Planner，没有实现独立 skill 执行沙盒。
- `list` 是安全目录查看工具；Planner 会优先用它处理“看看文件夹里有什么”这类请求。
- TUI 是轻量 Go 实现，借鉴 Kimi Code CLI 的信息结构，使用 Lip Gloss 做样式，不复用原 TypeScript/pi-tui 组件。
- Shell 命令当前在 workspace 目录下执行，但还没有 Docker 隔离。
- 文件工具已经做 workspace 路径限制；Shell 命令仍需要后续增加 Docker sandbox、危险命令审批、超时和资源限制策略。
- Trace 当前支持内存记录和 JSONL 落盘，后续可替换为 PostgreSQL。

## 下一步

- 增加 Docker sandbox 执行器。
- 增加 API Server 和 SSE 事件流。
- 将 trace、task、tool call 持久化到 PostgreSQL。
- 建立一组 coding task eval case，支持回归评测。
- 增加执行失败后的 Replan 能力。
