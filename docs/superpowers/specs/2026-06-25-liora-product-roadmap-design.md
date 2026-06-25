# Liora 产品规划：本地小工坊 Agent

## 1. 产品判断

Liora 的长期方向不是一个带可爱皮肤的 CLI，也不是普通聊天助手。它是一个运行在用户本地 Mac 上的 **sandbox for everything agent**：用户把愿望、闪念或任务交给 Liora，Liora 在可控的本地工坊中规划、执行、观察结果，并把重要上下文沉淀为长期记忆。

核心原则：

- UI 负责吸引用户，能力负责留住用户。
- 可爱角色感是入口，不是产品护城河。
- 性能、可靠性、安全边界和真实可执行能力优先于视觉装饰。
- 默认本地优先，默认可观察，默认不偷偷改用户电脑。

一句话定位：

```text
Liora 是住在 Mac 里的本地任务工坊，能陪你思考，也能在 Docker sandbox 里替你做事。
```

## 2. 目标用户

第一阶段服务一类核心用户：每天在 Mac 上处理大量信息、文件、代码和碎片想法的个人用户。

典型用户包括：

- 独立开发者：希望一个本地 Agent 帮他读代码、跑命令、整理项目、调用工具。
- 学生和求职者：希望整理文档、简历、资料、日程和学习笔记。
- 重度电脑用户：希望把 Downloads、PDF、图片、网页摘录、想法和脚本自动化起来。
- AI 工具爱好者：愿意配置模型和 MCP，希望拥有一个长期可成长的本地助手。

不优先服务：

- 企业多人协作场景。
- 云端 SaaS 工作流。
- 强社交或强娱乐陪伴。
- 完整设计白板或复杂知识库产品。

## 3. 产品形态

第一阶段形态定为：

```text
macOS 原生 SwiftUI App
  + 菜单栏常驻
  + 精致小巧的小工坊面板
  + 可展开任务详情窗口
  + Go Core Daemon
  + Docker sandbox
```

主界面不是大仪表盘，而是一个小巧的 Mac 面板：

- 顶部展示 Liora 状态、Docker 状态、当前模型。
- 中部是愿望输入框。
- 下方展示当前任务卡和进度。
- 右侧或折叠区域展示轻量 Liora 房间。
- 复杂任务展开为详情窗口，显示 plan、tool call、sandbox 日志、diff 和产出。

视觉方向：

- Mac 原生优先，避免过度二次元装饰。
- 低饱和浅色或深色材质，精致、克制、耐看。
- 角色存在感轻量常驻，更多体现在状态、文案和小房间。
- 不做 Live2D、语音、亲密度系统作为第一阶段重点。

## 4. 核心体验闭环

```text
唤起 Liora
  -> 写下愿望 / 闪念 / 任务
  -> Liora 判断进入白板、记忆或任务工坊
  -> 需要执行时创建任务卡
  -> 任务进入 Docker sandbox
  -> 展示计划、工具调用、进度和确认点
  -> 产出结果、文件、diff 或摘要
  -> 用户确认保存为记忆、日记或后续任务
```

关键体验要求：

- 输入要快：像 Raycast 一样随叫随到。
- 执行要稳：每个任务都有状态、日志和失败原因。
- 用户要放心：危险操作必须可见、可停、可确认。
- 结果要能沉淀：任务不是聊天记录里的文本，而是可复盘的卡片。

## 5. 核心模块

### 5.1 Mac App

职责：

- 菜单栏入口和小工坊面板。
- 闪念白板。
- 任务列表和任务详情。
- 记忆查看和编辑。
- 模型、MCP、Docker、权限设置。
- 通知、快捷键、文件选择器和系统权限。

不负责：

- 直接执行 Agent。
- 直接操作 Docker。
- 直接读写业务数据库。

### 5.2 Go Core Daemon

职责：

- 提供本地 HTTP/WebSocket API。
- 管理任务生命周期。
- 调度 Planner、Tool Registry、Sandbox Runner。
- 管理 SQLite memory、task、event、whiteboard 数据。
- 接入多供应商 LLM。
- 接入 MCP server。
- 向 SwiftUI 和 CLI 输出统一事件流。

### 5.3 Docker Sandbox

职责：

- 为任务创建隔离执行环境。
- 挂载 workspace 和临时输出目录。
- 限制命令超时、输出大小、工作目录和环境变量。
- 支持任务停止和清理。
- 将执行结果回写为可审查的 patch、文件或 artifact。

第一阶段策略：

- 所有执行类任务默认进入 Docker sandbox。
- 只允许显式选择的 workspace mount。
- 文件写入先在 sandbox 中完成，再由用户确认应用到真实 workspace。
- 对危险命令做拦截和确认。

### 5.4 Task Workshop

任务是 Liora 的核心产品对象。

任务状态：

```text
draft -> planning -> waiting_approval -> running -> waiting_user -> completed
                                             -> failed
                                             -> cancelled
```

任务必须包含：

- 用户原始输入。
- Planner 输出。
- tool call 事件。
- sandbox 日志。
- 产出文件和 diff。
- 最终摘要。
- 是否写入 memory。

### 5.5 Flash Whiteboard

第一阶段白板不是复杂画布，而是闪念缓冲区。

核心流程：

```text
随手写
  -> 自动分类
  -> Liora 给建议
  -> 转任务 / 转记忆 / 转日记 / 暂存
```

数据对象：

- `flash_notes`：闪念。
- `flash_clusters`：按主题聚合的闪念。
- `flash_suggestions`：Liora 建议动作。
- `linked_task_id`：从闪念转出的任务。
- `linked_memory_id`：保存后的记忆。

第二阶段再演进为每日画布。

### 5.6 Memory

Memory 是留存核心之一，不是简单聊天历史。

第一阶段 memory 类型：

- `preference`：用户偏好。
- `fact`：长期事实。
- `habit`：习惯。
- `project`：项目上下文。
- `diary`：日记和回顾。
- `task_result`：任务结果摘要。

Memory 要求：

- 可查看。
- 可搜索。
- 可编辑。
- 可删除。
- 可解释来源。

## 6. 能力路线

### v0.1 Core Daemon 稳定化

目标：把当前 CLI MVP 变成可被桌面 App 调用的本地核心。

必须完成：

- 本地 HTTP API。
- WebSocket/SSE 任务事件流。
- task schema 和 event schema。
- tool registry 重构。
- SQLite schema 升级。
- LLM provider 配置 API。
- MCP 配置 API。
- CLI 复用 daemon 能力。

验收指标：

- 创建任务到收到第一条事件小于 300ms，不含模型响应。
- 本地 API 冷启动小于 1s。
- 任务事件不丢失，可从 SQLite 恢复。

### v0.2 Docker Sandbox Runner

目标：所有执行任务进入本地 Docker sandbox。

必须完成：

- workspace mount。
- 临时输出目录。
- 命令超时。
- 输出截断。
- task cancel。
- sandbox 清理。
- diff/artifact 回传。
- 危险命令拦截。

验收指标：

- 普通命令启动延迟小于 1.5s。
- 大输出不会卡死 UI。
- 取消任务后容器和子进程能被清理。
- 未确认前不改真实 workspace。

### v0.3 Mac Mini Workshop

目标：做出可日常打开的 Mac 原生产品壳。

必须完成：

- 菜单栏入口。
- 小工坊面板。
- 任务输入。
- 当前任务卡。
- 任务详情窗口。
- 设置页。
- 模型/MCP/Docker 状态。

验收指标：

- App 启动小于 1s。
- 面板唤起小于 150ms。
- 任务状态更新延迟小于 100ms。
- 关键路径不依赖云端服务，除 LLM API。

### v0.4 Flash Whiteboard

目标：让 Liora 具备生活陪伴和思考入口。

必须完成：

- 闪念输入。
- 闪念列表。
- 自动分类。
- 转任务。
- 转记忆。
- 转日记。
- 每日回顾摘要。

验收指标：

- 创建闪念小于 100ms。
- 转任务流程不超过 2 次确认。
- 用户能清楚看到闪念是否已沉淀。

### v0.5 Capability Polish

目标：增强真实留存能力。

重点：

- MCP 长连接池。
- 工具并发和队列。
- Replan。
- 任务恢复。
- 权限规则。
- 结果质量评估。
- 常用任务模板。
- Memory 检索质量。

## 7. 性能优先级

性能是 Liora 的核心体验，而不是优化项。

优先级从高到低：

1. 面板唤起速度。
2. 本地 API 响应速度。
3. 任务事件流稳定性。
4. sandbox 启动和清理速度。
5. 工具执行速度。
6. memory 检索速度。
7. UI 动效流畅度。

第一阶段禁止：

- 为视觉效果牺牲启动速度。
- 在主线程做模型请求、Docker 操作或大文件扫描。
- 无限制读取大文件或输出。
- 把任务状态只存在内存里。
- 让用户看不到 sandbox 正在做什么。

## 8. 数据模型草案

```text
tasks
  id
  title
  user_input
  status
  workspace
  sandbox_id
  created_at
  updated_at
  completed_at

task_events
  id
  task_id
  type
  payload_json
  created_at

tool_calls
  id
  task_id
  tool_name
  input_json
  output_text
  status
  started_at
  ended_at

memories
  id
  text
  kind
  source
  importance
  created_at
  updated_at
  last_used_at

flash_notes
  id
  text
  status
  cluster_id
  linked_task_id
  linked_memory_id
  created_at
  updated_at
```

## 9. 产品护栏

Liora 每个版本都必须同时满足：

- 有陪伴感。
- 有真实执行能力。
- 有可观察状态。
- 有安全边界。
- 有长期记忆。
- 有可验证性能指标。

最重要的产品判断标准：

```text
用户敢不敢把一个真实任务交给 Liora。
```

如果用户只是觉得可爱，但不敢让她做事，产品失败。  
如果用户觉得她可靠，即使 UI 还很小，产品成立。

## 10. 第一阶段不做什么

明确不做：

- Live2D。
- 语音陪伴。
- 云同步。
- 插件市场。
- 多用户协作。
- 复杂视觉白板。
- 社交分享。
- 移动端。

这些都可以以后做，但不是留住用户的第一原因。

## 11. 近期实现顺序

推荐接下来的工程顺序：

1. 重构 Go daemon API。
2. 增加 task/event SQLite schema。
3. 增加 Docker sandbox runner。
4. 将 CLI 改为调用 daemon。
5. 增加 SwiftUI Mac App skeleton。
6. 实现菜单栏小面板。
7. 实现任务详情事件流。
8. 实现 Flash Whiteboard MVP。

每一步都必须有测试或可运行 smoke。不要先做复杂 UI 动效。
