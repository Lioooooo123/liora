# Liora DeepEval

这套评测把 Liora 当作黑盒 coding agent：输入自然语言任务，收集 daemon 事件和最终 workspace，再由 DeepEval 指标判断任务是否完成、是否调用了必要工具、文件结果是否符合预期。

## 快速运行

仓库根目录执行：

```sh
./scripts/deepeval.sh
```

默认只运行离线 contract tests，不需要模型密钥或 Confident AI 登录，也不会上传评测结果。它验证数据集、结果协议和自定义 DeepEval metric，适合 CI。

## 真实模型评测

先配置 Liora 的 API Key 模型，再显式开启 live case：

```sh
export LIORA_LLM_PROVIDER="openai-responses"
export LIORA_LLM_API_KEY="YOUR_API_KEY"
export LIORA_LLM_MODEL="gpt-5.4"
LIORA_DEEPEVAL_LIVE=1 ./scripts/deepeval.sh live
```

也可以设置 `LIORA_DEEPEVAL_BINARY` 复用已有二进制，或设置 `LIORA_DEEPEVAL_TIMEOUT` 调整单个任务超时秒数。live runner 会使用临时的 `LIORA_HOME` 和 workspace，不污染用户数据。

用例保存在 `cases/coding.json`。每个 case 可以声明：

- `files`：任务开始前的文件；
- `expected_files`：任务完成后必须精确匹配的文件；
- `expected_tools`：事件流中至少出现一次的工具；
- `allowed_changed_files`：允许新增、修改或删除的路径。

DeepEval 会把各项 contract check 汇总为 0 到 1 的分数；默认阈值是 1，任何不满足都会让评测失败。需要 Confident AI 的共享报告时，可自行执行 `deepeval login`；本仓库默认保持 local-first。
