# Super CDN v0.4.0

[English](release-v0.4.0.md) | 简体中文

发布日期：2026-05-14

状态：阶段性稳定版本。

v0.4.0 冻结了重构前的稳定点：现有产品表面保持不变，重点补齐 onboarding、诊断、cleanup 和存储 guardrails。

## 包含内容

- 更清晰的新用户上手路径和 README 文档入口。
- `upload-bucket-dir` 批量上传：dry-run、并发、重试、跳过已存在、报告文件和失败汇总。
- 存储策略和容量/文件大小/批次数量/日上传约束。
- R2、Pinata/IPFS、AList/OpenList 等后端的 guardrail 可见错误。
- `cdn-doctor` 和相关 CLI 错误里给出下一步诊断命令。
- release checklist 和后续 refactor handoff。

## 验证

v0.4.0 发布前应确认：

- Go 测试通过；
- Worker 测试、类型检查和 npm audit 通过；
- 关键 CLI 构建通过；
- storage constraint 错误能从 API/CLI 看到；
- 文档记录真实边界，不夸大成熟度。

## 已知边界

- 缺少完整 CI、正式 OpenAPI lint、版本化迁移和覆盖广泛的审计事件。
- `internal/server/server.go` 和 `cmd/supercdnctl/main.go` 仍偏大，需要后续重构拆分。
- 发布后下一步从 `docs/refactor-plan.md` 开始，不要重新做大范围摸底。
