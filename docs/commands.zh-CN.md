# Super CDN 命令大全

[English](commands.md) | 简体中文

这是给高级 `supercdnctl` 用户看的工作流式命令手册。它回答“下一步该跑哪个命令”，参数细节和 API 结构请继续查参数级参考。

深入参考：

- 参数细节：[cli-reference.md](cli-reference.md)
- 新用户流程：[onboarding.md](onboarding.md)
- 运维手册：[operations.md](operations.md)
- API 契约：[../api/openapi.yaml](../api/openapi.yaml)

## 约定

示例默认使用本地 Go runner：

```powershell
go run .\cmd\supercdnctl -- <command> ...
```

如果已经构建了二进制，参数完全相同：

```powershell
.\bin\supercdnctl.exe <command> ...
```

全局参数与环境变量：

| 参数 | 环境变量 | 用途 |
| --- | --- | --- |
| `-server` | `SUPERCDN_URL` | Super CDN 服务地址。`login` 后可保存到 profile。 |
| `-token` | `SUPERCDN_TOKEN` | 管理员或用户 API token，优先级高于本地 profile。 |
| `-profile` | `SUPERCDN_PROFILE` | 本地 profile 名称，建议一个服务/用户对应一个 profile。 |
| `SUPERCDN_CONFIG` | `SUPERCDN_CONFIG` | 可选的本地 CLI profile 文件路径。 |

修改 server、profile、token、存储配置或 DNS 后，先跑：

```powershell
go run .\cmd\supercdnctl -- doctor
```

## 第一组命令

| 目标 | 命令 |
| --- | --- |
| 接受邀请并保存 profile | `login -invite-token sci_xxx` |
| 查看当前身份 | `whoami` |
| 生成可给支持人员看的环境报告 | `doctor` |
| 创建海外 CDN 资源桶 | `create-cdn-bucket -slug overseas-assets -types image,archive` |
| 上传一个桶对象 | `upload-bucket -bucket overseas-assets -file .\poster.jpg -path images/v1/poster.jpg -warmup` |
| 发布普通静态网站 | `deploy-site -site demo -dir .\dist -target cloudflare_static -static-spa` |
| 发布混合边缘网站 | `deploy-site -site demo -dir .\dist -target hybrid_edge -profile china_mobile -static-spa` |
| 诊断桶对象 | `cdn-doctor -bucket overseas-assets -path images/v1/poster.jpg` |
| 诊断网站路径 | `site-doctor -site demo -path /assets/app.js` |
| 探测公网 Web 交付 | `probe-site -site demo -spa-path /movie/123` |

## 团队与 Profile

| 命令 | 什么时候用 |
| --- | --- |
| `login` | 接受邀请，并把用户 token 保存到本地 profile。 |
| `logout` | 删除本地保存的 profile。 |
| `whoami` | 确认 server、workspace、用户 id 和角色。 |
| `doctor` | 打包认证、数据库、存储、route profile 和 policy 状态。 |
| `audit-log` | 按 workspace、action 或 resource 查看变更审计事件。 |
| `invite-user` | 为 owner、maintainer 或 viewer 创建一次性邀请。 |
| `list-users` | 列出当前 workspace 用户。 |
| `revoke-token` | 吊销用户 API token。 |

典型流程：

```powershell
go run .\cmd\supercdnctl -- -token <root-token> invite-user -name alice -role maintainer
go run .\cmd\supercdnctl -- -server https://qwk.ccwu.cc -profile alice login -invite-token sci_xxx
go run .\cmd\supercdnctl -- -profile alice whoami
go run .\cmd\supercdnctl -- -profile alice doctor
```

## 静态对象 Project

Project 适合简单的 `/o/{project}/{path}` 对象。如果需要资源桶、预热、清理、诊断和资产类型策略，优先用 asset bucket。

| 命令 | 什么时候用 |
| --- | --- |
| `create-project` | 创建一个简单静态对象命名空间。 |
| `upload` | 经过 preflight 后上传单个对象。 |

示例：

```powershell
go run .\cmd\supercdnctl -- create-project -id assets
go run .\cmd\supercdnctl -- upload -project assets -file .\README.md -path docs/readme.txt -profile overseas
```

## 资源桶

资源桶用于管理可复用 CDN 资源，对外路径是 `/a/{bucket}/{logical_path}`。

| 命令 | 什么时候用 |
| --- | --- |
| `create-bucket` | 用明确 route profile 创建桶。 |
| `create-cdn-bucket` | 创建海外对象 CDN 桶，通常背后是 R2/Cloudflare。 |
| `create-domestic-cdn-bucket` | 创建 AList/OpenList 国内 CDN 桶。 |
| `create-mobile-cdn-bucket` | 国内移动线路快捷命令。 |
| `create-ipfs-bucket` | 创建 IPFS/Pinata 持久资源桶。 |
| `init-bucket` | 初始化桶目录结构。 |
| `upload-bucket` | 上传单个对象并返回公开 URL/直连 URL。 |
| `upload-bucket-dir` | 上传本地目录，支持 dry-run、重试和报告文件。 |
| `list-bucket` | 列出桶对象和公开 URL 元数据。 |
| `cdn-doctor` | 诊断桶状态、对象路由、副本和候选 URL。 |
| `purge-bucket` | 清理选中桶 URL 的 Cloudflare 缓存。 |
| `warmup-bucket` | 探测或预热选中桶 URL。 |
| `delete-bucket-object` | 删除一个路径、多个路径、一个前缀或全部桶对象。 |
| `delete-bucket` | 远端清理后删除桶元数据。 |

单文件上传：

```powershell
go run .\cmd\supercdnctl -- create-cdn-bucket -slug downloads -name downloads -types archive
go run .\cmd\supercdnctl -- upload-bucket -bucket downloads -file .\app.zip -path release/v1/app.zip -asset-type archive -warmup
go run .\cmd\supercdnctl -- cdn-doctor -bucket downloads -path release/v1/app.zip
```

批量上传并保留可审计报告：

```powershell
go run .\cmd\supercdnctl -- upload-bucket-dir -bucket downloads -dir .\release -prefix release/v1 -dry-run -report-file .\upload-plan.json
go run .\cmd\supercdnctl -- upload-bucket-dir -bucket downloads -dir .\release -prefix release/v1 -skip-existing -retry 2 -report-file .\upload-report.json
```

清缓存、预热、删除：

```powershell
go run .\cmd\supercdnctl -- purge-bucket -bucket downloads -prefix release/v1/ -dry-run
go run .\cmd\supercdnctl -- warmup-bucket -bucket downloads -path release/v1/app.zip -method GET
go run .\cmd\supercdnctl -- delete-bucket-object -bucket downloads -path release/v1/app.zip
```

## 网站与部署

Site 用于带域名、部署记录、readiness probe 和回滚/恢复证据的 Web 属性。

| 命令 | 什么时候用 |
| --- | --- |
| `create-site` | 创建带 route profile、target 和 domains 的网站。 |
| `list-sites` | 列出当前 workspace 可见的网站。 |
| `bind-domain` | 添加或替换网站域名。 |
| `domain-status` | 检查 DNS、绑定和证书状态。 |
| `deploy-site` | 从目录或 zip 发布新的不可变部署。 |
| `update-site` | 用现有 target/domains 默认值更新已有网站。 |
| `inspect-site` | 上传前检查本地 bundle。 |
| `probe-site` | 探测 HTML、资源、MIME/CORS、重定向和 SPA fallback。 |
| `list-deployments` | 列出某个网站的部署历史。 |
| `deployment` | 获取单个部署记录。 |
| `promote-deployment` | 将兼容的旧部署提升为生产。 |
| `delete-deployment` | 删除未激活、未 pin 的部署。 |
| `offline-site` | 让网站下线但保留部署。 |
| `online-site` | 恢复下线网站。 |
| `delete-site` | 破坏性删除网站及被追踪对象。 |
| `purge-site` | 按网站 manifest 规划清理 URL。 |
| `gc-site` | 按网站清理路径清理过期内容。 |

普通 Cloudflare 原生静态网站：

```powershell
go run .\cmd\supercdnctl -- create-site -site blog -profile overseas -target cloudflare_static -domains blog.example.com
go run .\cmd\supercdnctl -- deploy-site -site blog -dir .\dist -target cloudflare_static -domains blog.example.com -static-spa
go run .\cmd\supercdnctl -- probe-site -site blog -spa-path /movie/123 -require-edge-static-html -require-immutable-assets
```

混合边缘网站：

```powershell
go run .\cmd\supercdnctl -- create-site -site media -profile china_mobile -target hybrid_edge -domains media.example.com
go run .\cmd\supercdnctl -- deploy-site -site media -dir .\dist -target hybrid_edge -profile china_mobile -domains media.example.com -static-spa -resource-failover
go run .\cmd\supercdnctl -- probe-site -site media -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
```

`origin_assisted` 只建议用于本地测试、集成和旧流程兼容：

```powershell
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist -target origin_assisted -profile overseas
```

## Edge Manifest 与混合路由

| 命令 | 什么时候用 |
| --- | --- |
| `export-edge-manifest` | 导出部署 manifest，但不改变线上流量。 |
| `publish-edge-manifest` | 将部署 manifest 发布到 Workers KV。 |
| `refresh-edge-manifest` | locator 或健康状态变化后重建并发布 active manifest。 |
| `route-explain` | 解释某个网站路径的路由、候选和选择原因。 |
| `site-doctor` | 打包网站、部署、路由和预期边缘头诊断。 |
| `switch-plan` | 为桶对象或网站文件生成只读手动线路切换计划。 |
| `switch-apply` | 对受支持的一个对象/路径执行确认过的主目标切换。 |

常用序列：

```powershell
go run .\cmd\supercdnctl -- route-explain -site media -path /assets/app.js -country CN -client-ip 203.0.113.10
go run .\cmd\supercdnctl -- switch-plan -site media -path /assets/app.js -country CN
go run .\cmd\supercdnctl -- switch-apply -site media -path /assets/app.js -target repo_backup -dry-run=false -confirm switch
```

重要边界：

- `switch-plan` 只读。
- `switch-apply` 只修改一个对象/文件的主目标，不修改 route profile、routing policy、Worker 代码或 KV manifest。
- 被 `routing_policy` 或 `resource_failover` 控制的路径，应优先用 `route-explain`、`routing-policy-status` 和 `refresh-edge-manifest` 诊断。
- 静态资源失败切换要求存在就绪副本，不会回退到 Go 源站。

## 回滚与恢复

| 命令 | 什么时候用 |
| --- | --- |
| `rollback-plan` | 为某个部署生成安全回滚计划。 |
| `rollback-apply` | 执行确认过的回滚计划，默认 dry-run。 |
| `reconcile-deployment` | readiness 超时后对比 Super CDN 元数据和 provider 真实状态。 |
| `recover-cloudflare-static` | 验证未记录的 Cloudflare Static provider 写入。 |
| `recover-hybrid-edge` | provider 成功但本地元数据缺失时，验证或写回 hybrid edge 证据。 |
| `activate-cloudflare-static` | 证据检查通过后激活恢复出的 Cloudflare Static 部署。 |

示例：

```powershell
go run .\cmd\supercdnctl -- rollback-plan -site blog -deployment dpl-old
go run .\cmd\supercdnctl -- rollback-apply -site blog -deployment dpl-old -dir .\dist-rollback -dry-run=false -confirm rollback
```

Cloudflare Static 和 `hybrid_edge` 回滚需要 provider-aware。不要在 Worker assets 或 KV manifest 需要重发时只改元数据。

## 资源库与路由策略

| 命令 | 什么时候用 |
| --- | --- |
| `init-libraries` | 初始化资源库目录结构和 marker。 |
| `init-job` | 查看资源库初始化任务。 |
| `resource-status` | 查看配置的资源库状态和缓存健康状态。 |
| `routing-policy-status` | 检查智能路由策略定义和来源就绪情况。 |
| `health-check` | 运行被动或显式资源库健康检查。 |
| `e2e-probe` | 通过真实 route profile 主路径上传、读取并清理。 |

示例：

```powershell
go run .\cmd\supercdnctl -- init-libraries -dry-run
go run .\cmd\supercdnctl -- init-libraries
go run .\cmd\supercdnctl -- init-job -id 1
go run .\cmd\supercdnctl -- resource-status -library repo_china_all
go run .\cmd\supercdnctl -- routing-policy-status -policy global_smart
go run .\cmd\supercdnctl -- health-check -libraries repo_china_all
go run .\cmd\supercdnctl -- e2e-probe -profile china_all
```

## 任务与副本

| 命令 | 什么时候用 |
| --- | --- |
| `job` | 查看一个异步任务。 |
| `replicas` | 按 object id 查看副本。 |
| `refresh-replicas` | 重新检查远端可见性、签名 locator 和 IPFS 元数据。 |
| `repair-replicas` | 为缺失、失败或指定副本重新排队。 |
| `gc` | 清理过期本地 staging 文件，默认 dry-run。 |

示例：

```powershell
go run .\cmd\supercdnctl -- job -id 1
go run .\cmd\supercdnctl -- replicas -object-id 1
go run .\cmd\supercdnctl -- refresh-replicas -bucket downloads -prefix release/v1/
go run .\cmd\supercdnctl -- repair-replicas -object-id 1 -target repo_backup
go run .\cmd\supercdnctl -- gc -dry-run -older-than 1h
```

## IPFS

| 命令 | 什么时候用 |
| --- | --- |
| `ipfs-status` | 不上传文件，只检查 Pinata/IPFS token 和网关状态。 |
| `ipfs-smoke` | 上传测试资源、刷新元数据并探测网关读取。 |
| `ipfs-web-smoke` | 跑 IPFS-backed Web 部署路径。 |
| `refresh-ipfs-pins` | 刷新已知对象的 Pinata/IPFS pin 状态。 |

示例：

```powershell
go run .\cmd\supercdnctl -- ipfs-status
go run .\cmd\supercdnctl -- ipfs-smoke -file .\poster.jpg -download-runs 3
go run .\cmd\supercdnctl -- ipfs-web-smoke -file .\poster.jpg -cleanup
go run .\cmd\supercdnctl -- refresh-ipfs-pins -object-id 123
```

## Cloudflare 操作

| 命令 | 什么时候用 |
| --- | --- |
| `cloudflare-status` | 读取 Cloudflare account、zone、DNS、Worker 和 R2 状态。 |
| `publish-cloudflare-static` | 低层 Cloudflare Workers Static Assets 发布，不写 Super CDN 部署记录。 |
| `sync-site-dns` | 创建或验证被管理的网站 DNS 记录。 |
| `sync-worker-routes` | 创建或验证 Worker route pattern。 |
| `purge-site` | 从部署 manifest 规划精确 URL 清理。 |
| `sync-cloudflare-r2` | 同步 R2 CORS 和公开域名配置。 |
| `provision-cloudflare-r2` | 规划或创建 R2 bucket，并挂载 CORS/domain。 |
| `create-r2-credentials` | 创建 R2 S3 凭据，并可写入本地 config。 |
| `set-r2-credentials` | 将已有 R2 S3 凭据导入本地 config。 |
| `purge` | 清理指定 Cloudflare URL。 |

示例：

```powershell
go run .\cmd\supercdnctl -- cloudflare-status -all
go run .\cmd\supercdnctl -- sync-site-dns -site blog -dry-run
go run .\cmd\supercdnctl -- sync-worker-routes -site blog -dry-run
go run .\cmd\supercdnctl -- purge-site -site blog -dry-run
go run .\cmd\supercdnctl -- provision-cloudflare-r2 -cloudflare-library overseas_accel -dry-run
go run .\cmd\supercdnctl -- sync-cloudflare-r2 -cloudflare-library overseas_accel
go run .\cmd\supercdnctl -- purge -urls https://example.com/a.css
```

## 操作安全规则

- 改状态前优先跑 `doctor`、`cdn-doctor`、`site-doctor`、`probe-site` 和 `route-explain`。
- 普通 Web 托管默认选择 `cloudflare_static`。
- 入口 HTML 留在 Cloudflare、非入口资源走资源库时选择 `hybrid_edge`。
- 把 `origin_assisted` 当作测试或兼容路径，不要作为新生产默认运行时。
- 静态资源失败切换必须显式加 `-resource-failover`，并且要求多个就绪资源来源。
- 不要依赖 Cloudflare Static 元数据-only promote。回滚应通过 provider-aware redeploy 或 recovery 命令完成。
- 破坏性操作如果支持 dry-run，先 dry-run，再 apply。
