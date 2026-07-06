# Liora Development Workflow

这份文档给下一阶段开发做入口整理：先知道从哪里开始、哪些目录归谁、改完跑什么验证、哪些本地产物可以清理。

## 1. 开发前检查

```sh
git status --short --branch
pnpm clean:dry
```

如果要跑 TypeScript protocol 测试，而依赖不存在：

```sh
pnpm install --frozen-lockfile
```

如果只是改 Go core、TUI 或脚本，不需要先碰 `node_modules`。

## 2. 目录入口

- `apps/cli`：CLI、TUI、daemon 的进程入口和启动参数。
- `internal/agent`：自然语言任务规划、tool-use loop、todo 工具和调度。
- `internal/daemon`：本地 HTTP API、SSE 事件流、任务与 session 查询。
- `internal/daemonclient`：TUI 和未来客户端复用的 daemon client。
- `internal/task`：任务模型、runner、队列、子任务、todo 和 task event。
- `internal/store`：SQLite schema、迁移、session、timeline、memory 和 artifact。
- `internal/tui`：Go 原生 TUI 渲染、输入、命令、Markdown 和 viewport。
- `internal/tuisession`：TUI 命令到 daemon API 的 session 级适配层。
- `internal/llm`：provider registry、planner、streaming、tool schema 和能力描述。
- `internal/capabilities`：内建工具的 access、schema 和 registry。
- `internal/sandbox`：local / Docker shell executor 和 patch mode workspace。
- `packages/protocol`：daemon API / SSE event 的 TypeScript 契约包。
- `scripts`：安装、打包、smoke、eval、release 和 audit gate。
- `docs`：编号后的长期事实来源，不放一次性草稿。

## 3. 常用命令

```sh
pnpm clean:dry
pnpm clean
pnpm build:cli
pnpm test:go
pnpm test:protocol
pnpm smoke:tui
pnpm audit:v0.1
```

`pnpm clean` 只清理可再生成的本地构建产物，不删除依赖目录、`.omo`、`.superpowers`、`.codegraph` 或 `implementation-notes.md`。

## 4. 验证选择

- 只改文档：跑中文标点检查和 `git diff --check`。
- 改 `scripts`：跑 `go test -count=1 ./scripts`，必要时跑对应 smoke。
- 改 CLI 或配置：跑 `go test -count=1 ./apps/cli ./internal/config ./scripts`。
- 改 daemon/task/store：跑受影响包测试，并优先补 `./scripts/daemon-smoke.sh "$PWD"`。
- 改 TUI：跑 `go test -count=1 ./internal/tui ./internal/tuisession ./internal/daemonclient`，可见体验改动再跑 `./scripts/tui-smoke.sh "$PWD"`。
- 改 protocol：跑 `pnpm --filter @liora/protocol test`。
- 准备发布或大范围合入：跑 `./scripts/v0.1-exit-audit.sh "$PWD"` 或 `./scripts/liora-1.0-audit.sh "$PWD"`。

## 5. 本地 artifacts

以下路径是本地构建产物或临时安装产物，可以通过 `pnpm clean` 清掉：

- `dist/`
- `bin/`
- 根目录 `/cli`
- 根目录 `/liora-demo`
- `packages/*/dist/`
- `packages/*/*.tsbuildinfo`

不要把这些产物作为功能交付的一部分提交。需要分发时走 `scripts/package-release.sh` 和 release smoke。

## 6. 下一阶段开发原则

- 先从 `docs/01-liora-1.0-plan.md` 选择切片，再回到对应代码包实施。
- 保持 Go core、daemon、TUI 和 TypeScript protocol 的边界，不为了短期便利让 UI 直接扫存储或直接执行工具。
- 16 人格产品线仍是产品探索文档；如果推进 PRD、persona、对话样例、原型或代码实现，要同步更新 `docs/12-16人格日记本.md`。
- `implementation-notes.md` 只记录本地实施取舍，不提交到远端。
