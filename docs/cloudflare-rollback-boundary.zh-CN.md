# Cloudflare 回滚边界

[English](cloudflare-rollback-boundary.md) | 简体中文

本文记录 Cloudflare Static / Worker / KV 相关回滚的安全边界。核心原则是：只改 Super CDN 元数据不能证明真实 Cloudflare 流量已经回滚。

## 当前决策

Cloudflare-backed 网站不能用 metadata-only promote 当作安全回滚。只要线上流量依赖 Worker assets、custom domain、Worker route 或 KV manifest，回滚就必须移动 provider 状态，并用真实域名验证。

支持的 operator 路径是先跑：

```powershell
supercdnctl rollback-plan -site <site> -deployment <deployment>
```

计划会告诉 operator 当前 deployment 是否能通过元数据回滚，还是必须重新发布 Cloudflare Static 或 hybrid edge manifest。

## 线上验证

Cloudflare 回滚完成后，至少验证：

- 自定义域名 HTTPS 可访问；
- root HTML 是期望版本；
- SPA path 返回期望 entry；
- JS/CSS MIME 正确；
- hybrid edge 资源带有预期 `X-SuperCDN-Edge-*` 响应头；
- 回滚结果和 Super CDN active deployment 元数据一致。

## 为什么元数据回滚不安全

Cloudflare Static 的真实资源在 Worker assets 或 Pages/Workers 侧，Super CDN 的 active deployment 只是控制面记录。只改 active row 可能让控制面和真实 Worker 版本分离。

`hybrid_edge` 还依赖 KV manifest。即使 Super CDN 指向旧 deployment，Worker 仍可能读取旧 manifest 或新 manifest，导致入口和资源路由不一致。

Custom domain、DNS、Worker route、KV namespace 和 Worker version 都是 provider 侧状态，不会因为本地 SQLite 元数据改变而自动回滚。

## 最小事实来源

一次可信回滚至少需要这些事实：

- 目标 deployment id；
- 对应的本地或归档 source directory；
- 目标 domain；
- Worker name / route / KV namespace；
- provider 发布证据；
- 严格 probe 结果；
- 审计事件。

缺少这些事实时，只能生成计划或恢复建议，不应直接写 active metadata。

## 必需写入流程

Cloudflare Static 回滚应重新发布目标 source，并在 provider 成功后记录 active deployment。

`hybrid_edge` 回滚应重新发布 Worker assets 和 edge manifest。资源路由必须来自目标 deployment manifest，而不是手写或猜测。

每个写流程都必须：

- 默认 dry-run；
- 要求显式确认；
- 失败时输出下一步 recovery 命令；
- 写 audit event；
- 通过真实域名 strict probe 后才报告完成。

## 验证要求

本地测试要覆盖：

- metadata-only promote 被拒绝的 Cloudflare Static 场景；
- rollback plan 能区分 provider-aware 和 metadata-only；
- rollback apply 的确认、dry-run 和错误输出；
- 成功写入后的审计事件；
- readiness/probe 失败时不会静默激活。

真实环境 canary 要覆盖一次 Cloudflare Static 回滚，以及一次 `hybrid_edge` manifest 回滚或恢复。

## 当前 operator 路径

优先使用：

```powershell
supercdnctl rollback-plan -site <site> -deployment <deployment>
supercdnctl rollback-apply -site <site> -deployment <deployment> -dir <historical_dist> -dry-run=false -confirm rollback
supercdnctl reconcile-deployment -site <site> -deployment <deployment>
```

provider 写入已经成功但元数据缺失时，使用对应 recovery 命令，不要手工改数据库。
