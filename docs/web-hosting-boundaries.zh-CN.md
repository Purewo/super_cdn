# Web 托管边界

[English](web-hosting-boundaries.md) | 简体中文

最后更新：2026-05-02 Asia/Shanghai。

本文记录 Super CDN Web 托管已经接受的产品边界。当实现、文档、测试或配置示例不一致时，以这里为准。

## 托管模式

Super CDN Web 托管支持两种形态。

1. Go entry 模式。

   Go 服务负责首页或 preview entry HTML，也可以在存在直连 URL 时把非入口文件重定向到配置好的存储。这个模式用于简单集成测试、早期兼容检查和诊断，不是推荐的生产 Web 托管形态。

2. Cloudflare entry 模式。

   Cloudflare 通过 Workers Static Assets 或 Pages 承担首页和 SPA entry path。非入口资源放在明确的资源库上：AList/OpenList、Cloudflare-native static assets 或 IPFS/Pinata。这是推荐的 Web 托管形态。

Cloudflare R2 不是主流 Web 托管目标。R2 继续作为海外 CDN/object 加速线，用于大文件、媒体、归档、图片和下载。已有 R2-backed site 流程可保留给旧部署和诊断，但不应继续扩展为主路径。

## 资源归属

- Go service：控制面、部署接收、bundle 检查、元数据、manifest 生成、provider 健康检查、存储同步、备份上传和回滚记录。
- Cloudflare Static/Pages：推荐的首页和 SPA entry 交付。
- AList/OpenList：国内资源库。
- IPFS/Pinata：CID 地址化持久资源，以及可选 Web 资源库。
- R2：CDN/object 加速，不是主要 Web 托管面。

推荐的 Cloudflare entry 模式下，公网 Web 交付不应依赖 Go origin。

## 智能路由与失败切换

智能路由和失败切换都要求多个资源库，最低要求是两个 ready 来源。Super CDN 必须先把同一个对象上传或复制到 primary 和配置的 backups，这些来源才可参与路由。

Failover 默认关闭，只在用户显式要求时启用。

备份是存储副本，不是自动 Web 交付兜底。没有显式 routing/failover policy 时，Web 资源路由只使用 primary 资源库。

Route profile 通过 `replication_policy` 明确复制行为：

- `primary_only`：只放主线路；
- `best_effort_backups`：异步排队备份副本；
- `require_backups`：备份复制失败就让上传或部署失败。

Web 资源失败切换开关是 `resource_failover` / `deploy-site -resource-failover`。它要求 route profile 同时有 primary 和 backup targets。在边缘路径中，failover route 由 Worker 按 manifest 候选顺序代理。这个路径比 redirect 更重，所以保持显式 opt-in。

当 HTTPS Cloudflare-entry 页面指向 HTTP-only AList/OpenList `/d` URL 时，Worker 会 same-origin 代理该资源并打上 `X-SuperCDN-Edge-Source: storage` 和 `X-SuperCDN-Edge-Proxy: mixed_content`，避免浏览器 mixed-content 拦截，同时不回退到 Go origin。

发布带 routing policy 或 resource failover 的 `hybrid_edge` active Worker KV manifest 前，CLI 应确认每个非入口资源 route 至少有两个 ready 候选，避免异步备份未完成时把单来源 route 当作智能路由上线。

Ready 的含义是：副本存在、route 能解析出直连 locator 或 gateway URL，并且资源库目标在 `limits.resource_health_min_interval_seconds` 窗口内没有最近失败健康记录。

运行时恢复比发布守卫宽松。部署已经 active 后，`refresh-edge-manifest` 可以在健康过滤移除一个候选后，把剩余健康单来源 route 重新发布为降级恢复状态；这不代表新的 smart-routing/failover 部署可绕过两候选要求。

首页兼容开关是 `deploy-site -entry-origin-fallback` / `EDGE_ENTRY_ORIGIN_FALLBACK=true`。它只用于 `hybrid_edge` entry HTML/SPAs 在 Cloudflare entry delivery 失败后的临时兼容。响应必须带 warning header 和 `Cache-Control: no-store`，且不得影响 JS/CSS/图片等静态资源。

两类失败要分开：

- 首页失败：只有用户强烈要求，并且所有首页托管方式都失败后，才可临时回退到 Go entry。CLI 或响应必须提示这是临时状态，应尽快迁回 Cloudflare entry delivery。
- 静态资源失败：永远不回退到 Go server。静态资源只能在已经包含对象的 ready 资源库之间 failover；全部失败时应返回错误或暴露失败资源状态。

`origin_assisted` 仍可用于测试和兼容，但不是静态资源的生产 failover 目标。

## 对齐审计

- CDN/object acceleration：已对齐。R2 定位为海外 CDN/object 线，AList/OpenList/IPFS 可作为存储线。
- `cloudflare_static`：已对齐。普通海外静态网站在 Cloudflare-native hosting，不涉及 R2。
- `origin_assisted`：只在测试和兼容意义上对齐。它仍从 Go 服务 HTML，并可能在 redirect 不可用时 stream 资源，所以不能描述为推荐 Web runtime。
- `hybrid_edge`：已对齐。推荐部署使用 Cloudflare Static `ASSETS` 交付入口，Worker/KV manifest 路由资源。静态资源 fallback 限定在资源库内，首页 Go fallback 是单独临时开关。
- IPFS/Pinata：已对齐用于显式 Web resource route。`ipfs_pinata` 状态、CID 元数据、gateway probe、`ipfs-web-smoke` 和 live `hybrid_edge` canary 已通过。
- Smart routing：首个 live Web canary 已对齐。routing policy 和 resource failover 是显式 opt-in，至少两个来源，只用 ready 副本，`hybrid_edge` 发布 KV 前等待候选。
- 示例配置：主流 Web 示例应避免把 R2 作为默认非入口 Web resource path。R2 示例应标记为 CDN/object 或 legacy Web compatibility。

## 实现说明

- 普通海外静态网站默认用 `cloudflare_static`。
- `hybrid_edge` 是推荐 split model：Cloudflare entry 加 manifest-routed 资源。
- `origin_assisted` 用于本地测试、smoke test 和底层集成。
- 新功能应面向 Cloudflare entry 加非 R2 Web 资源库。
- `hybrid_edge` 默认发布 `EDGE_ORIGIN_FALLBACK=false`，打开它是显式兼容/故障动作。
- `hybrid_edge` 仅在 operator 传 `deploy-site -entry-origin-fallback` 时启用 `EDGE_ENTRY_ORIGIN_FALLBACK`。
- 开启 routing-policy 或 resource-failover 时，`hybrid_edge` 发布 KV 前等待 ready 资源候选。
- `refresh-edge-manifest` 可修复 active manifest 的签名 locator 过期或健康变化，但不应静默提升失败来源。
- 资源库上传应持久化已验证直连 locator，尤其是 AList/OpenList 签名路径。
- AList/OpenList provider 发布不可达 IPv6 记录时，客户端可强制 `network: "tcp4"`；HTTP 客户端必须保留有限 dial 和 response-header timeout。
- 任何未来 failover 开关都必须用户显式 opt-in，并保留首页/静态资源边界。
