# Super CDN v0.3.0

[English](release-v0.3.0.md) | 简体中文

发布日期：2026-05-13

状态：功能稳定里程碑。

v0.3.0 重点推进资源库能力模型、副本状态、复制策略、显式 failover、IPFS 路径和真实站点验证。

## 包含内容

- 资源能力模型和容量/约束字段。
- 对象副本状态和修复命令。
- `replication_policy`：`primary_only`、`best_effort_backups`、`require_backups`。
- CDN 资源桶和批量上传路径。
- `cdn-doctor`、`site-doctor`、`route-explain` 等诊断能力。
- IPFS/Pinata bucket 和 smoke 流程。
- 初步 `hybrid_edge` / edge manifest 路由方向。
- 真实 Web canary 记录。

## 当前稳定站点

v0.3.0 的 live stable site 证明了当时的 Web 发布、Cloudflare/R2 或 IPFS 资源路径可以实际访问。具体域名和部署 id 以英文 release note 和 handoff 文档记录为准。

## 验证

发布前应确认：

- Go 测试通过；
- Worker 测试和类型检查通过；
- bucket 上传、批量上传、诊断、资源状态命令可用；
- 至少一个真实 Web 路径 probe 通过；
- 文档记录智能路由和 failover 的显式边界。

## 已知边界

- `routing_policy` 和 `resource_failover` 仍需要严格 ready 候选和真实边缘验证。
- Cloudflare Static rollback 不能只改元数据。
- provider 写入和恢复路径需要更多 canary。
