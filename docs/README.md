# Liora Docs

这份目录只保留当前仍应作为事实来源的文档。旧的探索稿、一次性计划和已被 1.0 计划吸收的对标文档不再保留。

## 权威文档

- [Liora 1.0 计划](liora-1.0-plan.md)：未来主路线，覆盖 tool-use loop、transcript、上下文治理、权限、subagent、定时任务、个性化、hook、eval 和发布基线。
- [架构与分层规划](coding-agent-architecture-plan.md)：Go core、daemon、protocol、TUI/未来桌面端的边界和依赖方向。
- [技术选型](tech-stack-selection.md)：Go core、Bubble Tea TUI、TypeScript protocol/桌面预留等技术取舍。
- [v0.1 Exit Audit](v0.1-exit-audit.md)：当前 v0.1 能力底座的最终验收矩阵。
- [MVP Exit Benchmark](mvp-exit-benchmark.md)：v0.1 产品和能力边界说明，以 exit audit 为最终口径。
- [Release Packaging](release.md)：本地 tarball、安装脚本和 release smoke 说明。

## 维护规则

- 新路线优先写进 `liora-1.0-plan.md`，不要再新增零散 roadmap。
- 架构边界变更写进 `coding-agent-architecture-plan.md`。
- 发布验收变更写进 `v0.1-exit-audit.md` 或未来对应版本的 audit 文档。
- 具体实现取舍继续记录在仓库根目录的 `implementation-notes.md`。
