# Super CDN v0.3.1

[English](release-v0.3.1.md) | 简体中文

发布日期：2026-05-13

状态：维护稳定点。

v0.3.1 是 v0.3 的维护版本，重点修正部署、诊断和文档一致性，保持 v0.3 作为继续重构前的可用基线。

## 包含内容

- 补强 Web 发布和资源诊断输出。
- 改进命令错误里的下一步建议。
- 固化 v0.3 roadmap 和已知边界。
- 保持资源桶、副本、IPFS 和 hybrid edge 相关路径可继续验证。

## 验证

发布前应确认：

- Go 测试通过；
- Worker 测试/类型检查通过；
- 核心 bucket 和 site 命令仍可用；
- 文档链接和 release note 一致；
- 当前 live canary 没有被文档误描述。

## 已知边界

- v0.3.1 不是大功能版本。
- Cloudflare provider-aware rollback、完整 CI、OpenAPI lint 和成熟度审计仍在后续阶段推进。
