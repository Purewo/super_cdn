# 真实场景回归

[English](real-scenario-regression.md) | 简体中文

这份文档说明 `scripts/real-scenario-regression.ps1` 的用途：在不改线上状态的前提下，收集真实公网网站、边缘头、资源路由和控制面诊断证据。

## 脚本

典型只读运行：

```powershell
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -PublicUrl https://cyberstream-ipfs-0501.qwk.ccwu.cc/ `
  -SpaPath /movie/123 `
  -RequireEdgeStaticHtml `
  -RequireEdgeManifestAssets `
  -OutputPath .\data\real-regression-public.json
```

可选参数用于跳过控制面 `doctor`、指定 site/bucket/path、启用浏览器渲染检查，或把报告写入指定 JSON 文件。

## 脚本会运行什么

脚本聚合这些只读检查：

- `doctor`；
- `cdn-doctor`；
- `site-doctor`；
- `probe-site`；
- `reconcile-deployment`；
- 公网 URL root/SPAs/assets 探测；
- 需要时检查浏览器白屏；
- 汇总失败步骤和建议命令。

输出报告应作为发布、回归和事故排查证据保存。

## 必需真实场景

发布或重构前至少保留一个真实公网 Web 场景：

- `cloudflare_static`：入口 HTML 和 SPA fallback 由 Cloudflare Static 提供；
- `hybrid_edge`：入口由 Cloudflare Static 提供，非入口资源经 Worker/KV manifest 到资源库；
- IPFS/Pinata 或 AList/OpenList/R2 资源路线至少覆盖当前改动涉及的 provider；
- 边缘响应头必须能证明真实 route，例如 `X-SuperCDN-Edge-Source` 和 `X-SuperCDN-Edge-Manifest`。

## 什么时候需要更多真实环境测试

只读回归不足以覆盖 provider 写入风险。以下改动需要 mutating canary：

- Cloudflare Worker/Static/Pages 发布；
- DNS、custom domain、Worker route；
- KV manifest 写入；
- R2 bucket/CORS/domain provisioning；
- AList/OpenList 上传与可见性；
- Pinata/IPFS 上传、CID、gateway；
- `switch-apply`、rollback、recovery 等写行为。

## 报告处理

报告包含公网 URL、路径、响应状态、edge header、失败步骤、命令输出摘要和建议。提交 release 或做成熟度判断时，应记录：

- 脚本命令；
- 输出文件路径；
- 结果状态；
- 失败步骤数量；
- 涉及的公网域名；
- 如果失败，下一步诊断命令。

不要把 root URL 的 `404` 直接当成服务失败；应检查真实 API endpoint、站点路径和 preflight 行为。
