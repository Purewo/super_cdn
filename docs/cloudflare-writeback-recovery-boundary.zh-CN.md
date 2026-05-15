# Cloudflare 写回与恢复边界

[English](cloudflare-writeback-recovery-boundary.md) | 简体中文

本文定义 Cloudflare Static / hybrid edge 发布过程中 provider 写入成功、但 Super CDN 元数据或证据未及时落库时的恢复边界。

## 当前决策

恢复命令必须以证据为中心。不能因为 operator 认为 provider 写入“应该成功”就直接补 metadata；必须用源目录、provider 标识、真实域名 probe 和预期边缘头证明状态一致。

当前应使用：

```powershell
supercdnctl reconcile-deployment -site <site> -deployment <deployment>
supercdnctl recover-cloudflare-static -site <site> -dir <dist> -domains <domain> -worker-name <worker> -version-id <version>
supercdnctl recover-hybrid-edge -site <site> -deployment <deployment> -dir <dist> -domains <domain>
supercdnctl activate-cloudflare-static -site <site> -deployment <deployment> -dir <dist> -dry-run=false -confirm activate
```

## 失败形态

需要 recovery 的常见形态：

- Wrangler 或 Cloudflare API 返回成功，但 Super CDN readiness probe 超时。
- Worker assets 发布完成，但 active deployment 没有写入。
- KV manifest 写入成功，但 evidence row 缺失。
- Custom domain 稍后才生效，第一次 probe 失败。
- provider 成功后 CLI 或网络中断。

不应 recovery 的形态：

- 源目录已经不确定；
- provider version / worker / domain 无法确认；
- 真实域名 probe 不符合预期；
- edge header 与目标 deployment 不一致；
- 需要写入的 provider 对象无法定位。

## 写命令行为要求

恢复命令默认 dry-run。它们应输出：

- site、deployment、domain、worker、version、KV namespace；
- 本地 source 摘要；
- provider 证据；
- probe 结果；
- 是否允许写回；
- 如果允许，下一步确认命令。

确认写入必须要求明确的 `-confirm` 值，并写入审计事件。

写回后必须再次 probe 真实域名，并在失败时报告不一致，不得只因为数据库写入成功就宣称恢复完成。

## 服务端边界

服务端应把 provider evidence 与 deployment metadata 分开保存。未通过验证的 evidence 不能自动成为 active deployment。

服务端 API 应拒绝缺少必要字段的恢复写入，拒绝跨 site/deployment 的 evidence 写入，并把失败原因返回给 CLI。

## 非目标

- 不做“猜测式恢复”。
- 不绕过 Cloudflare readiness 和真实域名 probe。
- 不把 metadata-only activate 当作 Cloudflare Static 的正常回滚。
- 不清理 Worker 版本、custom domain 或 KV 条目；这仍是独立 provider cleanup。

## 测试与 Canary 要求

本地测试要覆盖 dry-run、确认、审计、缺证据拒绝、probe 失败拒绝和成功写回。

真实 provider canary 要覆盖：

- Cloudflare Static provider 成功但 metadata 未激活后的恢复；
- `hybrid_edge` KV/Worker evidence 写回；
- 激活后 strict probe；
- recovery 输出中的下一步命令。
