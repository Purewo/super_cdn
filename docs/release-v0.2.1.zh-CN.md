# Super CDN v0.2.1

[English](release-v0.2.1.md) | 简体中文

发布日期：2026-05-13

状态：维护稳定点。

v0.2.1 是 v0.2 线的维护版本，重点是修正诊断、发布路径和文档，使 v0.2 可作为继续演进的稳定基础。

## 包含内容

- 补强 `doctor`、站点诊断和 provider 状态输出。
- 改进静态站点发布后的探测和错误说明。
- 维护 R2/AList/IPFS 配置示例。
- 文档明确 v0.2 的版本边界和非目标。
- 保留 v0.2 线作为 bugfix、文档和运维硬化分支。

## 验证

发布前应确认：

- Go 测试通过；
- 基础 CLI 命令可运行；
- 站点部署和 probe 路径仍可用；
- 诊断输出不泄露 token 或 secret；
- README、roadmap 和 maintenance 文档一致。

## 已知边界

- 新功能应转向 v0.3 计划。
- v0.2 不承诺完整智能路由、自动 failover 或 provider-aware rollback。
- 真实 provider canary 覆盖仍需后续阶段扩展。
