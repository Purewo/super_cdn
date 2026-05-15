# Super CDN 新用户上手指南

[English](onboarding.md) | 简体中文

这份指南给真实用户提供最短可支持路径：连接服务、创建 CDN 资源桶、上传文件、发布网站，并在失败时收集有用诊断。

## 1. 连接并运行 Doctor

优先使用本地 profile：

```powershell
.\bin\supercdnctl.exe -server https://qwk.ccwu.cc -profile alice login -invite-token sci_xxx
.\bin\supercdnctl.exe -profile alice whoami
.\bin\supercdnctl.exe -profile alice quota
.\bin\supercdnctl.exe -profile alice doctor
```

一次性本地测试可以直接设置 token：

```powershell
$env:SUPERCDN_TOKEN = "sct_xxx"
.\bin\supercdnctl.exe -server http://127.0.0.1:8080 doctor
```

`doctor` 是第一份支持报告。它检查认证、数据库、存储目标、route profile、staging 存储、资源库状态和路由策略状态，不会输出 token 或 secret。

非 root 用户默认累计上传配额为 10 GiB。大文件上传前先运行 `quota`；如果剩余额度不够，运行 `request-quota -max-gb 20 -reason "release test"` 创建申请，再让 root 管理员用 `approve-quota` 审批。

## 2. 选择资源线路

| 目标 | 命令 | 适合场景 |
| --- | --- | --- |
| 海外对象 CDN | `create-cdn-bucket` | R2/Cloudflare 上的图片、视频、压缩包和公开下载 |
| 国内 CDN 桶 | `create-domestic-cdn-bucket -line mobile|telecom|unicom|all` | AList/OpenList 资源库承载的国内静态资源 |
| IPFS 持久桶 | `create-ipfs-bucket` | 适合 CID/gateway 交付的不可变资源 |

```powershell
.\bin\supercdnctl.exe create-cdn-bucket -slug overseas-assets -name overseas-assets -types image,archive
.\bin\supercdnctl.exe create-domestic-cdn-bucket -slug mobile-assets -line mobile -types image,document
.\bin\supercdnctl.exe create-ipfs-bucket -slug durable-assets -types image,archive
```

不可变 CDN 文件建议使用带版本的逻辑路径，例如 `images/v1/poster.jpg`。

## 3. 上传单个文件

```powershell
.\bin\supercdnctl.exe upload-bucket -bucket overseas-assets -file .\poster.jpg -path images/v1/poster.jpg -asset-type image -warmup
.\bin\supercdnctl.exe cdn-doctor -bucket overseas-assets -path images/v1/poster.jpg
```

上传输出里最常用的字段：

| 字段 | 含义 |
| --- | --- |
| `copy_urls.public_url` | 可分享或嵌入的稳定 Super CDN URL |
| `copy_urls.cdn_url` | 后端提供时的直接 CDN/storage URL |
| `copy_urls.storage_url` | 同一个直接存储 URL 的明确命名 |
| `next_commands` | 下一步可运行或贴给支持人员的诊断命令 |

上传失败时，CLI 错误会给出同一个 bucket/path 的 `cdn-doctor` 命令。

## 4. 批量上传目录

先 dry-run：

```powershell
.\bin\supercdnctl.exe upload-bucket-dir -bucket overseas-assets -dir .\release -prefix release/v1 -dry-run -report-file .\upload-plan.json
```

再上传并保留报告：

```powershell
.\bin\supercdnctl.exe upload-bucket-dir -bucket overseas-assets -dir .\release -prefix release/v1 -skip-existing -retry 2 -report-file .\upload-report.json
```

批量报告包含 `summary`、总数、成功/跳过/失败数量、逐文件 `results`、`report_saved_to` 和 `next_commands`。即使部分失败，命令也会跑完整个批次，报告就是排查依据。

## 5. 发布静态网站

普通 Cloudflare 原生静态网站：

```powershell
.\bin\supercdnctl.exe create-site -site blog -profile overseas -target cloudflare_static -domains blog.qwk.ccwu.cc
.\bin\supercdnctl.exe deploy-site -site blog -dir .\dist -target cloudflare_static -domains blog.qwk.ccwu.cc -static-spa
.\bin\supercdnctl.exe probe-site -site blog -spa-path /movie/123 -require-edge-static-html -require-immutable-assets
```

混合网站：入口 HTML 留在 Cloudflare Static，非入口资源通过 Worker manifest 走资源库：

```powershell
.\bin\supercdnctl.exe create-site -site cyberstream -profile china_mobile -target hybrid_edge -domains cyberstream.qwk.ccwu.cc
.\bin\supercdnctl.exe deploy-site -site cyberstream -dir .\dist -target hybrid_edge -profile china_mobile -domains cyberstream.qwk.ccwu.cc -static-spa -resource-failover
.\bin\supercdnctl.exe probe-site -site cyberstream -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
```

只有当 route profile 有主资源线和至少一个就绪备用资源线时，才使用 `-resource-failover`。

## 6. Web 诊断

页面打不开、白屏或资源异常时，按顺序收集：

```powershell
.\bin\supercdnctl.exe site-doctor -site cyberstream
.\bin\supercdnctl.exe site-doctor -site cyberstream -path /assets/app.js -country CN
.\bin\supercdnctl.exe probe-site -site cyberstream -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
.\bin\supercdnctl.exe route-explain -site cyberstream -path /assets/app.js -country CN
```

`site-doctor` 会打包 active deployment、托管目标、路由解释、已选择候选、被跳过候选、脱敏存储 URL 和预期边缘响应头。

## 7. 常见失败排查

| 现象 | 第一个命令 | 关注点 |
| --- | --- | --- |
| token 缺失或被拒绝 | `doctor` | profile/server 不匹配或邀请过期 |
| 桶上传失败 | `cdn-doctor -bucket <slug> -path <path>` | bucket 状态、route profile、存储目标、策略或约束错误 |
| 批量上传部分失败 | 查看 `upload-report.json` | 失败路径、尝试次数、`next_commands` 里的重试命令 |
| public URL 正常但直连 storage URL 失败 | `cdn-doctor` | 签名 URL 过期、副本状态、AList/OpenList HEAD 与 GET 行为差异 |
| 网站白屏 | `probe-site -browser-render` 后接 `site-doctor -path <asset>` | HTML 状态、JS/CSS MIME、CORS、manifest 头、截图白屏检查 |
| hybrid 部署等待或失败 | `routing-policy-status` | 新 smart/failover 路由要求至少两个就绪候选 |

## 8. 清理

先对本地 staging 过期文件做保守 dry-run：

```powershell
.\bin\supercdnctl.exe gc -dry-run -older-than 1h
.\bin\supercdnctl.exe gc -dry-run=false -older-than 1h
```

第一版 GC 不删除远端对象，可重复运行，并会输出逐项状态。
