# Super CDN v0.2.0

[English](release-v0.2.0.md) | 简体中文

发布日期：2026-05-13

状态：内部稳定基线。

v0.2.0 把静态网站发布、资源库、R2/IPFS/AList 方向和初步线上 canary 固定成第二个稳定点。

## 包含内容

- 静态网站部署的基本流程：create/list/deploy/update/probe。
- 资源库和 route profile 的初步模型。
- R2 作为海外对象/CDN 线路的集成方向。
- AList/OpenList 作为国内资源库的集成方向。
- IPFS/Pinata 作为持久资源线路的集成方向。
- 站点部署历史、active deployment 和 preview/production 访问路径。
- 初步 Cloudflare Worker 兼容路径。
- 维护文档和 roadmap 边界。

## 线上 Canary

v0.2.0 的 live canary 用于证明 Web 发布和资源读取路径可以跑通，但不能直接等价为所有 provider 写入、回滚和智能路由都成熟。

## 验证

发布前应确认：

- Go 测试通过；
- CLI 站点命令可用；
- 本地静态站点部署和 probe 可用；
- R2/AList/IPFS 相关配置不会破坏基础流程；
- 文档说明当前边界和下一阶段任务。

## 已知边界

- Cloudflare Static 不是主要发布路径。
- 混合边缘、KV manifest、资源 failover、智能路由和 provider-aware rollback 尚未成熟。
- 资源库健康、容量约束和批量上传还需要后续增强。
