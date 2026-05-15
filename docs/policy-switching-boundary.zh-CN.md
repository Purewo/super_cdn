# Policy 切换边界

[English](policy-switching-boundary.md) | 简体中文

最后更新：2026-05-15 Asia/Shanghai。

本文记录 `routing_policy` 和 `resource_failover` 切换的当前决策。它用来避免虚假的成熟感：Super CDN 现在可以检查 policy 候选，但在真实流量边界被证明前，不能暴露声称能切换 policy 流量的写命令。

## 当前决策

不要把 policy-level apply/rollback 实现成简单元数据写入。产品方向是让 operator 显式选择：Super CDN 展示候选就绪情况并解释路由决策，用户再决定是否修改 route profile、routing policy 配置，或重新部署 Cloudflare-backed assets。

当前支持行为：

- `cdn-doctor` 和 `site-doctor` 报告候选、被跳过目标、健康原因和建议。
- `switch-plan` 区分 `candidate_ready` 和 `apply_supported`。
- 对 `routing_policy` 和 `resource_failover` route，`switch-plan` 返回 `apply_supported=false`。
- `switch-plan` 建议继续运行 `routing-policy-status` 或 `route-explain`。
- `switch-apply` 拒绝 routing-policy 和 resource-failover 路径，不修改 `primary_target`。
- 被拒绝的切换尝试会写入审计。

## 为什么元数据 Apply 不够

简单非 policy 对象可以通过修改 `objects.primary_target` 控制选择目标。

`routing_policy` 的选择由 policy mode、region group、source weight/priority、health cache、候选 ready 状态和请求属性共同决定。改一个对象的 `primary_target` 不一定改变线上选择。

`resource_failover` 的候选顺序来自 route profile 和已导出的 manifest。改一个对象的 `primary_target` 不会改变已经发布到 Worker/KV 的 failover route 顺序。

对 `hybrid_edge` 来说，即使控制面 policy 修改正确，也要重新发布并验证 active edge manifest，真实流量才可能变化。

## 如果未来重新考虑写命令

未来的 policy apply/rollback 命令必须具备：

1. 持久化的 intended policy override 或 rollback target 来源。
2. dry-run 计划，准确列出会改变哪些 route、object、manifest 和 domain。
3. 写入需要显式确认。
4. 成功和拒绝都要有审计事件。
5. 成功后输出 rollback 命令或 rollback plan。
6. 目标是 `hybrid_edge` 时发布 active edge manifest。
7. 对真实自定义域名做 live verification，包括证明候选变化的 edge headers。
8. 测试覆盖不支持的 policy/failover metadata-only 写入仍会被拒绝。

## 当前可接受操作

在上述能力完成前，operator 应使用：

- `switch-plan` 检查候选就绪和 apply 支持；
- `routing-policy-status` 检查 policy source 健康；
- `route-explain` 查看网站路径的实际选择逻辑；
- 修复资源就绪或签名后，对 active hybrid deployment 运行 `refresh-edge-manifest`；
- Cloudflare-backed rollback 使用完整 `deploy-site -target hybrid_edge` 或 `deploy-site -target cloudflare_static` 流程。
