# Coding Agent 工具调用与执行系统技术方案

## 1. 项目定位

本项目目标是实现一版面向研发任务的 Coding Agent 系统。它不追求完整复刻 Kimi Code CLI，而是聚焦一个可演示、可扩展、能体现工程深度的最小闭环：

- 理解用户提出的代码修改、问题排查或命令验证任务。
- 自动读取项目文件、搜索代码、制定执行计划。
- 调用标准化工具完成文件读取、文件修改、Shell 执行和结果观察。
- 记录完整执行轨迹，支持失败步骤复现、行为分析和后续评测。
- 通过沙盒限制文件访问和命令执行范围，降低 Agent 自动执行风险。

第一版建议优先做后端和 CLI/API 闭环，前端 UI 可以后置。

## 2. 推荐技术栈

| 模块 | 技术选型 | 说明 |
| --- | --- | --- |
| 后端语言 | Go | 贴近后端岗位表达，适合并发、服务化和工程落地 |
| Agent 编排 | Eino | 用于编排 ReAct、Plan-Execute-Replan 和工具调用流程 |
| API 服务 | Gin 或 Fiber | 提供任务创建、会话查询、执行流式输出等接口 |
| 流式输出 | SSE | 向 CLI 或 Web 前端实时推送 Agent 思考、工具调用和命令输出 |
| 执行隔离 | Docker | 每个任务使用独立工作目录和容器执行 Shell |
| 数据库 | PostgreSQL | 持久化 session、task、tool call、artifact、diff 和 eval result |
| 状态/队列 | Redis | 管理异步任务、执行状态、短期缓存和 Eval Runner 队列 |
| 前端可选 | Vue 3 | 用于展示会话、计划、工具调用轨迹和最终 diff |
| CI/Eval | GitHub Actions | 在 Prompt、工具逻辑或模型配置变更后触发回归评测 |

## 3. 总体架构

```text
User / CLI / Web
        |
        v
API Gateway
        |
        v
Session Service ---- PostgreSQL
        |
        v
Agent Engine
        |
        +---- Planner
        +---- Tool Selector
        +---- Context Manager
        +---- Trace Recorder ---- PostgreSQL
        |
        v
Tool Registry
        |
        +---- read_file
        +---- write_file
        +---- list_files
        +---- search_code
        +---- run_shell
        +---- git_diff
        +---- ask_user
        |
        v
Sandbox Executor ---- Docker Workspace
```

核心执行链路：

```text
用户需求
  -> 创建 session/task
  -> Agent 生成初始计划
  -> 选择工具并生成参数
  -> Tool Registry 校验参数和权限
  -> Sandbox Executor 执行工具
  -> 记录工具结果和执行轨迹
  -> Agent 根据观察结果更新计划
  -> 直到任务完成或需要用户补充信息
  -> 输出总结、文件变更和验证结果
```

## 4. 核心模块设计

### 4.1 Session Service

职责：

- 管理用户会话和任务生命周期。
- 保存多轮对话上下文。
- 维护任务状态，例如 `pending`、`running`、`waiting_user`、`failed`、`completed`。
- 提供会话恢复能力，让 Agent 能继续之前的执行上下文。

建议数据表：

- `sessions`
- `tasks`
- `messages`
- `task_events`

### 4.2 Agent Engine

职责：

- 根据用户需求生成计划。
- 在每一步选择合适工具。
- 根据工具结果更新上下文和下一步动作。
- 判断任务是否完成、失败或需要用户确认。

第一版建议实现两种执行模式：

- `ReAct`：适合短任务，例如查文件、定位错误、跑测试。
- `Plan-Execute-Replan`：适合多步骤任务，例如实现一个功能、修复一组测试、整理一份报告。

### 4.3 Tool Registry

所有工具都用统一结构注册：

```go
type Tool struct {
    Name        string
    Description string
    InputSchema  any
    Handler     ToolHandler
    Permission  PermissionPolicy
}
```

第一版工具集：

- `list_files`：列出目录文件。
- `read_file`：读取文件内容。
- `search_code`：基于 ripgrep 搜索代码。
- `write_file`：写入或覆盖文件。
- `apply_patch`：应用局部补丁，优先于整文件覆盖。
- `run_shell`：在沙盒中执行命令。
- `git_diff`：展示当前文件变更。

后续可扩展：

- `web_search`
- `db_query`
- `openapi_call`
- `log_search`
- `ask_user`
- MCP 工具接入适配器

### 4.4 Sandbox Executor

职责：

- 为每个任务创建隔离工作目录。
- 将用户项目复制或挂载到容器内。
- 限制容器可访问路径。
- 控制命令超时、输出大小和并发数量。
- 拦截高风险命令。

建议策略：

- 默认只允许访问 workspace。
- Shell 命令设置超时，例如 30 秒或 120 秒。
- stdout/stderr 做最大长度截断。
- 禁止 `rm -rf /`、磁盘格式化、系统级服务修改等高风险命令。
- 文件写入必须经过路径校验，不能越过 workspace。

### 4.5 Trace & Audit

这是项目的简历亮点之一。每次 Agent 执行都要保存结构化轨迹：

- 用户输入。
- Agent 当前计划。
- 工具名称。
- 工具参数。
- 工具执行结果。
- Shell stdout/stderr/exit code。
- 文件变更 diff。
- 错误原因。
- token、latency、cost 等运行元数据。

这些数据可以支持：

- 失败步骤复现。
- Agent 行为分析。
- Prompt 迭代对比。
- 自动评测。
- 问题排查和审计。

### 4.6 Eval Runner

第二阶段后可以加入自动化评测平台：

- 维护固定 coding task 测试集。
- 批量运行 Agent。
- 统计任务完成率、工具调用正确率、测试通过率、延迟和 token cost。
- 记录失败 case 和历史趋势。
- 在 GitHub Actions 中自动触发回归评测。

推荐指标：

- `task_completion`
- `tool_call_correctness`
- `test_pass_rate`
- `patch_apply_success`
- `latency`
- `token_cost`
- `human_intervention_count`

## 5. MVP 范围

第一版目标是形成可演示闭环，不做过度平台化。

必须实现：

- 创建任务会话。
- 指定一个本地代码目录作为 workspace。
- Agent 可以读取文件、搜索代码、修改文件、运行 Shell。
- 所有工具调用都有结构化日志。
- 输出最终总结和 git diff。
- Shell 在 Docker 沙盒中执行。

暂不实现：

- 完整 Web UI。
- 多用户权限系统。
- 复杂 MCP 市场。
- 长期记忆。
- 企业级审计后台。
- 大规模分布式调度。

## 6. API 草案

### 创建任务

```http
POST /api/v1/tasks
Content-Type: application/json

{
  "workspace": "/path/to/project",
  "prompt": "修复测试失败并说明原因",
  "mode": "plan_execute"
}
```

### 订阅任务事件

```http
GET /api/v1/tasks/{task_id}/events
Accept: text/event-stream
```

事件类型：

- `message`
- `plan`
- `tool_call`
- `tool_result`
- `shell_output`
- `file_diff`
- `final`
- `error`

### 查询任务详情

```http
GET /api/v1/tasks/{task_id}
```

### 查询任务 diff

```http
GET /api/v1/tasks/{task_id}/diff
```

## 7. 数据表草案

### sessions

| 字段 | 说明 |
| --- | --- |
| id | 会话 ID |
| created_at | 创建时间 |
| updated_at | 更新时间 |
| workspace_path | 工作目录 |

### tasks

| 字段 | 说明 |
| --- | --- |
| id | 任务 ID |
| session_id | 所属会话 |
| prompt | 用户需求 |
| status | 任务状态 |
| mode | 执行模式 |
| final_summary | 最终总结 |
| created_at | 创建时间 |
| updated_at | 更新时间 |

### tool_calls

| 字段 | 说明 |
| --- | --- |
| id | 工具调用 ID |
| task_id | 所属任务 |
| tool_name | 工具名称 |
| input_json | 工具参数 |
| output_json | 工具结果 |
| started_at | 开始时间 |
| ended_at | 结束时间 |
| status | 成功或失败 |

### artifacts

| 字段 | 说明 |
| --- | --- |
| id | 产物 ID |
| task_id | 所属任务 |
| type | 产物类型，例如 diff、log、file |
| path | 产物路径 |
| content | 产物内容 |

## 8. 开发里程碑

### 阶段 1：基础 Agent Loop

- 实现 API 服务骨架。
- 实现 session/task 数据模型。
- 接入 LLM。
- 实现 ReAct 循环。
- 实现 `read_file`、`search_code`、`run_shell`。

验收标准：

- 用户输入一个排查任务后，Agent 能读取文件、搜索代码、执行命令，并给出总结。

### 阶段 2：代码修改能力

- 实现 `apply_patch` 和 `git_diff`。
- 支持文件变更记录。
- 支持失败后的重新计划。

验收标准：

- Agent 能完成一个小型 bugfix，并输出可查看的 diff。

### 阶段 3：沙盒和权限

- 接入 Docker sandbox。
- 加入路径限制、命令超时、输出截断和危险命令拦截。
- 高风险工具调用进入待确认状态。

验收标准：

- Shell 命令在容器内执行，不能访问 workspace 以外路径。

### 阶段 4：轨迹持久化

- 落库保存完整执行轨迹。
- 提供任务详情和工具调用查询接口。
- 支持失败任务复盘。

验收标准：

- 任意一次任务都能查看完整计划、工具调用、命令输出和文件 diff。

### 阶段 5：评测闭环

- 建立固定 coding task 测试集。
- 实现 Eval Runner。
- 接入 GitHub Actions。
- 输出指标趋势和失败 case。

验收标准：

- Prompt 或工具逻辑变更后，可以自动跑回归评测并生成报告。

## 9. 简历表达建议

可以将项目描述为：

> 设计并实现面向研发任务的 Coding Agent 执行系统，支持代码理解、文件修改、Shell 执行、工具反馈和执行轨迹追踪。基于 Eino 编排 ReAct 与 Plan-Execute-Replan 流程，将文件、Shell、Git、日志等能力封装为标准化工具，并通过 Docker 沙盒限制文件访问与命令执行范围，降低 Agent 自动执行风险。

可拆成简历 bullet：

- 参考 Kimi Code CLI 的终端 Coding Agent 形态，设计支持代码理解、文件修改、Shell 执行和工具反馈的 Agent 执行系统，帮助用户完成代码修改、问题排查和命令验证等研发任务。
- 基于 Eino 编排 ReAct 与 Plan-Execute-Replan 流程，将文件读取、文件改写、Shell 执行、代码搜索和 Git diff 封装为标准化 Agent 工具，统一完成工具注册、参数校验、权限控制、调用执行和结果解析。
- 设计任务上下文与执行轨迹管理能力，记录用户需求、执行计划、工具调用结果、命令输出和文件变更，支持多轮对话续写、失败步骤复现和 Agent 行为分析。
- 通过 Docker 沙盒约束文件访问和命令执行范围，隔离任务运行目录与执行上下文，降低 Coding Agent 在执行生成代码或 Shell 命令时的权限风险。

## 10. 后续风险和取舍

- 不建议第一版直接做完整插件市场或 MCP 生态，优先把工具调用闭环、沙盒执行和轨迹持久化做好。
- 不建议一开始做复杂 Web UI，CLI 或简单 API 更利于快速验证核心能力。
- 如果没有真实模型预算，可以先用 mock LLM 或 scripted planner 验证工具执行链路。
- Docker 沙盒不是绝对安全边界，后续若要面向真实用户，需要补充更严格的资源限制、网络隔离和权限模型。
