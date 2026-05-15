# Super CDN

[English](README.md) | 简体中文

Super CDN 是一个 Windows 优先的 CDN 资源桶与静态网站托管控制平面。它由 Go API 服务、`supercdnctl` 命令行工具、SQLite 元数据、存储适配器，以及 Cloudflare Worker / Static Assets 自动化组成。

你可以用它完成这些工作：

- 把普通静态网站发布到 Cloudflare 原生静态托管；
- 发布混合网站：入口 HTML 由 Cloudflare 承载，JS/CSS/图片等非入口资源由 Worker/KV 路由到资源库；
- 在 Cloudflare R2、AList/OpenList、Pinata/IPFS 上创建可复用 CDN 资源桶；
- 检查打包产物、探测线上访问、诊断桶/网站路由、手动切换就绪副本，以及做恢复或回滚。

## 快速开始

第一次使用建议按顺序跑完这一小段：

1. 在本机启动控制平面，或者让 `supercdnctl` 指向已经部署好的服务。
2. 使用邀请 token 登录，或者在本地测试时设置 `SUPERCDN_TOKEN`。
3. 创建资源前先运行 `doctor`。
4. 按需求创建 CDN 资源桶，或者创建一个静态网站。
5. 上传或部署后，用 `cdn-doctor`、`site-doctor` 或 `probe-site` 验证结果。

本地开发启动：

```powershell
.\scripts\start-dev.ps1 -Config .\configs\config.local.json
```

另开一个终端，做最短的资源桶上传验证：

```powershell
$env:SUPERCDN_TOKEN = "change-me"
go run .\cmd\supercdnctl -- doctor
go run .\cmd\supercdnctl -- create-cdn-bucket -slug overseas-assets -name overseas-assets -types image,archive
go run .\cmd\supercdnctl -- upload-bucket -bucket overseas-assets -file .\poster.jpg -path images/v1/poster.jpg -asset-type image -warmup
go run .\cmd\supercdnctl -- cdn-doctor -bucket overseas-assets -path images/v1/poster.jpg
```

发布普通静态网站：

```powershell
go run .\cmd\supercdnctl -- create-site -site demo -profile overseas -target cloudflare_static -domains demo.example.com
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist -target cloudflare_static -domains demo.example.com -static-spa
go run .\cmd\supercdnctl -- probe-site -site demo -spa-path /movie/123 -require-edge-static-html
```

发布混合边缘网站：

```powershell
go run .\cmd\supercdnctl -- create-site -site cyberstream -profile china_mobile -target hybrid_edge -domains cyberstream.example.com
go run .\cmd\supercdnctl -- deploy-site -site cyberstream -dir .\dist -target hybrid_edge -profile china_mobile -domains cyberstream.example.com -static-spa -resource-failover
go run .\cmd\supercdnctl -- site-doctor -site cyberstream -path /assets/app.js
```

`-resource-failover` 只应在当前 route profile 有主资源线和至少一个就绪备用资源线时使用。Super CDN 不会把静态资源静默回退到 Go 源站。

## 文档入口

- 新用户上手流程：[docs/onboarding.md](docs/onboarding.md)
- 中文命令大全：[docs/commands.zh-CN.md](docs/commands.zh-CN.md)
- 英文命令大全：[docs/commands.md](docs/commands.md)
- 参数级 CLI 参考：[docs/cli-reference.md](docs/cli-reference.md)
- 运维手册：[docs/operations.md](docs/operations.md)
- 真实场景回归手册：[docs/real-scenario-regression.md](docs/real-scenario-regression.md)
- REST API 契约：[api/openapi.yaml](api/openapi.yaml)
- Web 托管边界：[docs/web-hosting-boundaries.md](docs/web-hosting-boundaries.md)
- 当前阶段发布说明：[docs/release-v0.5.0.md](docs/release-v0.5.0.md)
- 成熟度检查清单：[docs/maturity-audit.md](docs/maturity-audit.md)
- 后续重构计划：[docs/refactor-plan.md](docs/refactor-plan.md)、[docs/tomorrow-plan.md](docs/tomorrow-plan.md)

## 核心概念

| 概念 | 含义 |
| --- | --- |
| Project | 最基础的静态对象命名空间，对外路径是 `/o/{project}/{path}`。 |
| Asset bucket | 面向用户的 CDN 资源桶，对外路径是 `/a/{bucket}/{logical_path}`。 |
| Site | 带域名、部署记录、托管目标和路由配置的网站。 |
| Deployment | 一次不可变的网站发布。生产环境只是指向某个 active deployment，而不是覆盖旧文件。 |
| Storage target | 真实存储后端，例如本地磁盘、R2、AList/OpenList、Pinata/IPFS。 |
| Resource library | 逻辑资源线，可包装某个存储或挂载路径，例如国内 AList 线路。 |
| Route profile | 上传/部署策略，包括主存储、备份、缓存行为和复制策略。 |
| Deployment target | 网站托管形态：`cloudflare_static`、`hybrid_edge` 或 `origin_assisted`。 |
| Routing policy | 显式的智能路由、负载均衡或失败切换规则，要求多个就绪来源。 |

## 典型使用场景

| 目标 | 推荐命令 | 说明 |
| --- | --- | --- |
| 检查当前环境 | `doctor` | 先确认认证、数据库、存储、路由配置和资源线状态。 |
| 创建海外对象 CDN | `create-cdn-bucket` | 通常用于 R2/Cloudflare 上的图片、视频、压缩包和下载资源。 |
| 创建国内 CDN 桶 | `create-domestic-cdn-bucket -line mobile|telecom|unicom|all` | 面向国内访问，底层通常接 AList/OpenList 资源库。 |
| 创建 IPFS 归档桶 | `create-ipfs-bucket` | 适合不可变、CID 优先、需要网关读取的资源。 |
| 上传单个资源 | `upload-bucket` | 返回稳定的 Super CDN URL 和可用时的直连 CDN/storage URL。 |
| 批量上传目录 | `upload-bucket-dir` | 支持 dry-run、跳过已存在文件、失败重试和报告文件。 |
| 发布普通网站 | `deploy-site -target cloudflare_static` | 默认推荐的海外静态网站发布路径。 |
| 发布混合网站 | `deploy-site -target hybrid_edge` | 入口留在 Cloudflare，资源走 Worker/KV 路由到资源库。 |
| 诊断资源桶 | `cdn-doctor` | 查看对象、公开 URL、副本、IPFS 元数据和路由候选。 |
| 诊断网站 | `site-doctor`、`probe-site`、`route-explain` | 检查生产部署、边缘头、资源路由、MIME、SPA fallback。 |

## 团队登录与权限

管理员 token 是根级别的 break-glass 凭证。团队使用时，建议由 root 或 owner 创建邀请，然后每个用户登录到自己的本地 CLI profile：

```powershell
go run .\cmd\supercdnctl -- -token <root-token> invite-user -name alice -role maintainer
go run .\cmd\supercdnctl -- -server https://qwk.ccwu.cc -profile alice login -invite-token sci_xxx
go run .\cmd\supercdnctl -- -profile alice whoami
go run .\cmd\supercdnctl -- -profile alice doctor
```

用户 token 保存在本地 `supercdn/cli.json` profile 中，并按 workspace 生效。Owner 可以管理邀请和 token；maintainer 可以创建资源桶和部署网站；viewer 只读。Cloudflare、R2、AList 等底层配置命令仍保持 root-only。

## 运维检查

架构变更前先跑本地基线：

```powershell
.\scripts\foundation-check.ps1
```

这个脚本会检查 Go 格式、测试、vet、Windows/Linux 构建、Worker 测试与类型检查，并在存在 `configs/config.local.json` 时启动本地服务检查 `/healthz`。

如果需要覆盖真实 Cloudflare/R2 凭证，再运行 full 模式：

```powershell
.\scripts\foundation-check.ps1 -Full -LiveSiteUrl "https://cyberstream.sites.qwk.ccwu.cc/?v=dpl-di49qyrhf5y0" -SpaPath /movie/123
```

真实发布后，常用只读验证命令是：

```powershell
go run .\cmd\supercdnctl -- site-doctor -site cyberstream -path /assets/app.js
go run .\cmd\supercdnctl -- probe-site -site cyberstream -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
go run .\cmd\supercdnctl -- route-explain -site cyberstream -path /assets/app.js -country CN
```

## 当前状态

当前稳定基线是 `v0.5.0`。它是阶段性成熟版本，重点补齐了 CI、OpenAPI lint、迁移记录、审计事件、服务/CLI 结构拆分、运维文档、命令文档和只读真实场景回归。

这不等于宽泛的公开 GA 承诺。后续如果改到 Cloudflare 写入、DNS、Worker/KV 发布、AList/OpenList 可见性、R2 provisioning/CORS、IPFS/Pinata 上传、手动切换或回滚写行为，仍需要增加真实 provider canary。

## 安全边界

- 默认把 `cloudflare_static` 作为普通网站发布路径。
- `hybrid_edge` 用于入口 HTML 留在 Cloudflare、非入口资源走资源库的场景。
- `origin_assisted` 主要用于本地测试、集成测试和旧流程兼容，不应作为新生产网站首选运行时。
- 静态资源失败切换必须显式打开 `-resource-failover`，并且要求多个就绪资源来源。
- `switch-plan` 只读；`switch-apply` 只做受支持对象/路径的一次主目标切换，不修改 route profile、routing policy、Worker 代码或 KV manifest。
- Cloudflare Static 不支持只改 Super CDN 元数据的安全回滚。涉及 Worker assets 或 KV manifest 时，应使用 provider-aware 的 redeploy、rollback 或 recovery 命令。
- 删除、回滚、GC、R2 同步、Cloudflare 修复等操作，优先 dry-run，看清计划后再 apply。
