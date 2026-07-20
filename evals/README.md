# Liora DeepEval

这套评测把 Liora 当作黑盒 coding agent：输入自然语言任务，收集 daemon 事件和最终 workspace，再由 DeepEval 从正确性、安全、验证、工具使用和效率五个维度判分。

## 快速运行

仓库根目录执行：

```sh
./scripts/deepeval.sh
```

默认模式会构建并启动真实 Liora daemon，再接入本地脚本化 OpenAI-compatible 模型，执行 12 个确定性 benchmark。它不需要模型密钥或 Confident AI 登录，也不会上传评测结果，适合作为 PR CI 的稳定质量门禁。

只验证数据集和评分器、不启动 Liora 时使用：

```sh
./scripts/deepeval.sh contract
```

确定性 benchmark 覆盖精确替换、多文件创建、搜索后修改、测试失败后恢复、无关文件保护、删除生成物、追加内容、JSON 配置修改、文档链接修复、glob 批量修改、fixture 创建和缺失路径恢复。

## 真实模型评测

先配置 Liora 的 API Key 模型，再显式开启 live case：

```sh
export LIORA_LLM_PROVIDER="openai-responses"
export LIORA_LLM_API_KEY="YOUR_API_KEY"
export LIORA_LLM_MODEL="gpt-5.4"
./scripts/deepeval.sh live
```

live 层只运行 4 个核心任务：精确替换、多文件创建、搜索后修改、测试失败后恢复。也可以设置 `LIORA_DEEPEVAL_BINARY` 复用已有二进制，或设置 `LIORA_DEEPEVAL_TIMEOUT` 调整单个任务超时秒数。runner 使用临时的 `LIORA_HOME` 和 workspace，不污染用户数据。

用例保存在 `cases/coding.json`。每个 case 可以声明：

- `files`：任务开始前的文件；
- `expected_files`：任务完成后必须精确匹配的文件；
- `expected_absent_files`：任务结束后必须不存在的文件；
- `expected_tools` / `required_successful_tools`：必须调用及必须成功的工具；
- `forbidden_tools`：禁止使用的工具；
- `allowed_changed_files`：允许新增、修改或删除的路径；
- `required_event_types`：必须产生的 daemon 事件，例如失败恢复时的 `task.replanning`；
- `max_tool_calls` / `max_duration_ms`：效率上限；
- `profiles`：选择 `deterministic`、`live` 或两者；
- `scripted_steps`：确定性模型在 CI 中返回的工具调用序列。

总分权重为：正确性 50%、安全 20%、验证 15%、工具使用 10%、效率 5%。默认阈值仍为 1，任何 contract check 不满足都会让 CI 失败。需要 Confident AI 的共享报告时可自行执行 `deepeval login`；本仓库默认保持 local-first。
