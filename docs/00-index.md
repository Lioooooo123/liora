# Liora Docs

这份目录只保留当前仍应作为事实来源的文档。一次性对标报告、临时评审稿和已被主路线吸收的探索稿不再保留。

## 0. 文档入口

- [00 文档索引](00-index.md)：当前 `docs` 目录入口、编号规则和维护边界。

## 1. Liora 工程主线

- [01 Liora 1.0 计划](01-liora-1.0-plan.md)：未来主路线，覆盖 tool-use loop、transcript、上下文治理、权限、subagent、定时任务、个性化、hook、eval 和发布基线。
- [02 架构与分层规划](02-coding-agent-architecture-plan.md)：Go core、daemon、protocol、TUI/未来桌面端的边界和依赖方向。
- [03 技术选型](03-tech-stack-selection.md)：Go core、Bubble Tea TUI、TypeScript protocol/桌面预留等技术取舍。
- [04 v0.1 Exit Audit](04-v0.1-exit-audit.md)：当前 v0.1 能力底座的最终验收矩阵。
- [05 MVP Exit Benchmark](05-mvp-exit-benchmark.md)：v0.1 产品和能力边界说明，以 exit audit 为最终口径。
- [06 Release Packaging](06-release-packaging.md)：本地 tarball、安装脚本和 release smoke 说明。
- [07 Development Workflow](07-development-workflow.md)：仓库入口、常用命令、清理规则和下一阶段开发前检查。
- [13 Hardening & Remediation Plan](13-hardening-remediation-plan.md)：2026-07-11 四维审计（安全、架构、健壮性、测试）发现的问题清单与分阶段修复计划。

## 2. 16 人格产品探索

- [10 16 人格 Agent PRD](10-16-personality-agent-prd.md)：手机端 16 人格 Agent App 的产品设想、人格引擎和 MVP 验收。
- [11 16 人格 Agent Prompt / Persona Spec](11-16-personality-agent-persona-spec.md)：16 个 Agent 的 persona 配置、语气基线和运行时字段。
- [12 16 人格日记本](12-16人格日记本.md)：从想法、调研、文档推进到后续更新的项目日记。

## 维护规则

- 新工程路线优先写进 `01-liora-1.0-plan.md`，不要再新增零散 roadmap。
- 架构边界变更写进 `02-coding-agent-architecture-plan.md`。
- 发布验收变更写进 `04-v0.1-exit-audit.md` 或未来对应版本的 audit 文档。
- 开发入口、命令和本地 artifacts 规则变更写进 `07-development-workflow.md`。
- 16 人格方向更新必须同步 `12-16人格日记本.md`。
- 一次性对标、临时评审和已被主路线吸收的探索稿不进入 `docs` 根目录。
- 具体实现取舍继续记录在仓库根目录的 `implementation-notes.md`。
