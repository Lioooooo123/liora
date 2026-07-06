# Liora Release Packaging

Liora v0.1 先以 macOS 本地 tarball 分发。包内包含一个可执行的 `liora` 二进制、安装脚本和必要文档，适合发给其他人直接试用。

## 自动发版

默认发版路径是合入 `main`。每次 `main` 收到新 commit 后，`.github/workflows/release.yml` 会：

1. 用 `scripts/next-release-version.sh` 读取最新 `vMAJOR.MINOR.PATCH` tag，并自动递增 patch 版本。
2. 在 GitHub macOS arm64 runner 上构建 `liora_<version>_darwin_arm64.tar.gz`。
3. 运行 `scripts/release-smoke.sh`，确认安装包、`-doctor`、本地 `liora update --from` 和任意 workspace 启动路径可用。
4. 给当前 commit 打 tag，创建或更新 GitHub Release，并把该 release 标记为 latest。

因此用户侧升级路径是：

```sh
liora update
```

只要 latest release 里有当前平台对应的 tarball，更新器会下载、校验 `.sha256` 并替换当前 `liora` 可执行文件。

## 构建发布包

本地构建只用于试包或应急发版。正式对外版本优先走 `main` 自动发版。

```sh
LIORA_VERSION=v0.1.0 ./scripts/package-release.sh
```

默认会根据本机 `go env GOOS/GOARCH` 构建，例如：

```text
dist/liora_v0.1.0_darwin_arm64.tar.gz
dist/liora_v0.1.0_darwin_arm64.tar.gz.sha256
dist/liora_v0.1.0_darwin_arm64.tar.gz.provenance.json
dist/liora_v0.1.0_darwin_arm64.tar.gz.sbom.json
dist/liora_v0.1.0_darwin_arm64.tar.gz.manifest-review.json
```

可以用环境变量覆盖目标平台：

```sh
LIORA_VERSION=v0.1.0 GOOS=darwin GOARCH=arm64 ./scripts/package-release.sh
```

## 验证发布包

```sh
./scripts/release-smoke.sh dist/liora_v0.1.0_darwin_arm64.tar.gz
```

这个 smoke 会先运行 supply-chain audit，验证 checksum、provenance、依赖清单和 MCP/hook manifest 审查报告，再解包到临时目录，运行包内 `install.sh`，执行安装后的 `liora -version`，并用安装后的二进制跑一遍 `liora update --from <archive>`。

也可以单独运行 supply-chain audit：

```sh
./scripts/release-supply-chain-audit.sh dist/liora_v0.1.0_darwin_arm64.tar.gz
```

审计要求 tarball 旁边存在同名 `.sha256`、`.provenance.json`、`.sbom.json` 和 `.manifest-review.json`。这些文件必须引用相同的 package name、version 和 git commit；依赖清单不能为空；manifest 审查不能包含未批准的联网 MCP server 或 `/tmp`、`/var/tmp`、`/private/tmp` 下的绝对路径 hook 命令。

验证 daemon-backed TUI 主链路：

```sh
LIORA_TUI_SMOKE_DAEMON_ADDR=127.0.0.1:19090 \
LIORA_TUI_SMOKE_LLM_ADDR=127.0.0.1:19091 \
./scripts/tui-smoke.sh "$PWD"
```

这个 smoke 会启动临时 fake LLM、fake MCP server、Core Daemon 和 `-tui-daemon` 交互入口，覆盖 `/tools` 的 MCP 工具展示、streaming 输出、`/tail` 历史回看、`/timeline` 和运行中 `/cancel`；`/diff` 预览、`/approvals` 审批队列、显式 session resume、`/new-session`、`/transcript` 展开回看、`/todo` session plan 展示、`todo_write/read` 工具、`/artifact` 长输出分页、daemon `/v1/workbench` snapshot、daemon `/v1/timeline/search`、daemon multi-task SSE、TUI `/history`、`/workbench` workspace 状态视图、TUI `/spawn` 后台任务、TUI `/watch` 多任务观察和 daemonclient multi-task event stream 由 Go 测试覆盖。

验证 coding task 能力基线：

```sh
LIORA_EVAL_DAEMON_ADDR=127.0.0.1:19092 \
LIORA_EVAL_LLM_ADDR=127.0.0.1:19093 \
./scripts/coding-eval.sh
```

这个 eval 会启动临时 fake LLM、fake MCP server 和 Core Daemon，覆盖自然语言规划、DOCX 文档读取、MCP 外部工具审批与调用、失败诊断、失败后 replan、单文件和多文件 patch-first 写入、apply 落盘、大输出截断、事件历史、timeline、permission approve/deny、cancel 和子进程清理。

## 用户安装

用户拿到 tarball 后执行：

```sh
tar -xzf liora_v0.1.0_darwin_arm64.tar.gz
cd liora_v0.1.0_darwin_arm64
./install.sh
```

默认安装到：

```text
~/.local/bin/liora
```

如果 `~/.local/bin` 不在 `PATH`，把下面这行加入 shell profile：

```sh
export PATH="$HOME/.local/bin:$PATH"
```

## 快速试用

```sh
liora -version
liora -workspace /path/to/project -prompt $'list .\ndiff'
```

启动本地 daemon：

```sh
liora -daemon -daemon-addr 127.0.0.1:18080
```

## 包内容

```text
bin/liora
install.sh
README.md
docs/00-index.md
docs/01-liora-1.0-plan.md
docs/02-coding-agent-architecture-plan.md
docs/03-tech-stack-selection.md
docs/04-v0.1-exit-audit.md
docs/05-mvp-exit-benchmark.md
docs/06-release-packaging.md
docs/10-16-personality-agent-prd.md
docs/11-16-personality-agent-persona-spec.md
docs/12-16人格日记本.md
```

`install.sh` 不会写入 API key。LLM 配置仍由用户自己放到 `~/.config/liora/.env`，或通过环境变量设置。

## 更新体验

安装后的用户入口对齐 Claude Code 的日常体验：

```sh
liora update
```

默认会读取 GitHub latest release metadata，选择当前平台的 `liora_<version>_<goos>_<goarch>.tar.gz`。如果 GitHub API 被限流，会回退到 GitHub Releases 网页的 `/releases/latest` 重定向来发现最新 tag，并按约定拼出安装包地址。如果 release asset 旁边存在同名 `.sha256`，更新前会先校验再替换当前 `liora` 可执行文件。

本地或内测包可以直接指定 tarball：

```sh
liora update --from dist/liora_v0.1.0_darwin_arm64.tar.gz
```

只检查最新版本、不安装：

```sh
liora update --check
```

正式对外版本必须在发版时显式注入：

```sh
LIORA_VERSION=v0.1.0 ./scripts/package-release.sh
```

不要用裸 commit hash 当正式对外版本。开发包可以继续使用 `git describe --always --dirty` 生成的临时版本；正常发版由 `main` workflow 自动决定下一个版本号。
