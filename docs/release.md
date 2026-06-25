# Liora Release Packaging

Liora v0.1 先以 macOS 本地 tarball 分发。包内包含一个可执行的 `liora` 二进制、安装脚本和必要文档，适合发给其他人直接试用。

## 构建发布包

```sh
LIORA_VERSION=v0.1.0 ./scripts/package-release.sh
```

默认会根据本机 `go env GOOS/GOARCH` 构建，例如：

```text
dist/liora_v0.1.0_darwin_arm64.tar.gz
dist/liora_v0.1.0_darwin_arm64.tar.gz.sha256
```

可以用环境变量覆盖目标平台：

```sh
LIORA_VERSION=v0.1.0 GOOS=darwin GOARCH=arm64 ./scripts/package-release.sh
```

## 验证发布包

```sh
./scripts/release-smoke.sh dist/liora_v0.1.0_darwin_arm64.tar.gz
```

这个 smoke 会解包到临时目录，运行包内 `install.sh`，再执行安装后的 `liora -version`。

验证 daemon-backed TUI 主链路：

```sh
LIORA_TUI_SMOKE_DAEMON_ADDR=127.0.0.1:19090 \
LIORA_TUI_SMOKE_LLM_ADDR=127.0.0.1:19091 \
./scripts/tui-smoke.sh "$PWD"
```

这个 smoke 会启动临时 fake LLM、Core Daemon 和 `-tui-daemon` 交互入口，覆盖 streaming 输出、`/timeline` 和运行中 `/cancel`。

验证 coding task 能力基线：

```sh
LIORA_EVAL_DAEMON_ADDR=127.0.0.1:19092 \
LIORA_EVAL_LLM_ADDR=127.0.0.1:19093 \
./scripts/coding-eval.sh
```

这个 eval 会启动临时 fake LLM 和 Core Daemon，覆盖自然语言规划、patch-first 写入、apply 落盘、事件历史、timeline 和 cancel。

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
docs/mvp-exit-benchmark.md
```

`install.sh` 不会写入 API key。LLM 配置仍由用户自己放到 `~/.config/liora/.env`，或通过环境变量设置。
