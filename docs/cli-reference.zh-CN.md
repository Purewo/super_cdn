# Super CDN CLI 参数参考

[English](cli-reference.md) | 简体中文

这是 `supercdnctl` 的中文参数级参考入口。完整命令表、HTTP API 映射、请求形状、重要参数和返回字段保留在 [cli-reference.md](cli-reference.md)；该文件本身已经包含大量中文字段说明。本页用于中文用户快速定位常用部分，并把高频工作流链接到中文文档。

工作流优先请看：

- 中文命令大全：[commands.zh-CN.md](commands.zh-CN.md)
- 新用户上手：[onboarding.zh-CN.md](onboarding.zh-CN.md)
- 运维手册：[operations.zh-CN.md](operations.zh-CN.md)
- REST API 契约：[../api/openapi.yaml](../api/openapi.yaml)

## 全局约定

所有 `supercdnctl` 命令都调用 Super CDN HTTP API。控制面 API 使用 Bearer Token。团队使用建议通过 invite 登录并保存本地 profile：

```powershell
.\bin\supercdnctl.exe -server https://qwk.ccwu.cc -profile alice login -invite-token sci_xxx
.\bin\supercdnctl.exe -profile alice whoami
.\bin\supercdnctl.exe -profile alice doctor
```

本地一次性测试可以使用：

```powershell
$env:SUPERCDN_TOKEN = "change-me"
.\bin\supercdnctl.exe -server http://127.0.0.1:8080 doctor
```

全局参数：

| 参数 | 环境变量 | 说明 |
| --- | --- | --- |
| `-server` | `SUPERCDN_URL` | 服务地址，登录后可由 profile 保存 |
| `-token` | `SUPERCDN_TOKEN` | 管理员或用户 token，显式传入时覆盖 profile |
| `-profile` | `SUPERCDN_PROFILE` | 本地 profile 名称 |
| `SUPERCDN_CONFIG` | `SUPERCDN_CONFIG` | CLI profile 文件路径 |

## 中文快速索引

| 主题 | 完整参考位置 | 推荐先读 |
| --- | --- | --- |
| 团队登录与权限 | [cli-reference.md](cli-reference.md) 的“团队登录与用户隔离” | [onboarding.zh-CN.md](onboarding.zh-CN.md) |
| 普通静态资源 | [cli-reference.md](cli-reference.md) 的“普通静态资源” | [commands.zh-CN.md](commands.zh-CN.md) |
| 静态站点 | [cli-reference.md](cli-reference.md) 的“静态站点” | [web-hosting-boundaries.zh-CN.md](web-hosting-boundaries.zh-CN.md) |
| 资源库运维 | [cli-reference.md](cli-reference.md) 的“资源库运维” | [operations.zh-CN.md](operations.zh-CN.md) |
| 静态资源桶 | [cli-reference.md](cli-reference.md) 的“静态资源桶” | [onboarding.zh-CN.md](onboarding.zh-CN.md) |
| 任务和副本 | [cli-reference.md](cli-reference.md) 的“任务和副本” | [commands.zh-CN.md](commands.zh-CN.md) |
| IPFS | [cli-reference.md](cli-reference.md) 的“IPFS” | [commands.zh-CN.md](commands.zh-CN.md) |
| Cloudflare | [cli-reference.md](cli-reference.md) 的“Cloudflare” | [cloudflare-writeback-recovery-boundary.zh-CN.md](cloudflare-writeback-recovery-boundary.zh-CN.md) |

## 最常用命令

```powershell
.\bin\supercdnctl.exe doctor
.\bin\supercdnctl.exe create-cdn-bucket -slug downloads -name downloads -types archive
.\bin\supercdnctl.exe upload-bucket -bucket downloads -file .\app.zip -path release/v1/app.zip -asset-type archive -warmup
.\bin\supercdnctl.exe upload-bucket-dir -bucket downloads -dir .\release -prefix release/v1 -skip-existing -retry 2 -report-file .\upload-report.json
.\bin\supercdnctl.exe create-site -site blog -profile overseas -target cloudflare_static -domains blog.example.com
.\bin\supercdnctl.exe deploy-site -site blog -dir .\dist -target cloudflare_static -domains blog.example.com -static-spa
.\bin\supercdnctl.exe probe-site -site blog -spa-path /movie/123 -require-edge-static-html
```

## 维护说明

`cli-reference.md` 是参数级完整来源；本页是中文阅读入口和导航。新增命令时必须同时更新：

- [commands.md](commands.md) / [commands.zh-CN.md](commands.zh-CN.md)；
- [cli-reference.md](cli-reference.md)；
- 内置 `usage()` 输出；
- 必要时更新 OpenAPI 和运维文档。
