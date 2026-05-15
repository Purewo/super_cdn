# Super CDN v0.5.0

[English](release-v0.5.0.md) | 简体中文

发布日期：2026-05-15

状态：阶段性成熟里程碑。

v0.5.0 关闭了 post-`v0.4.0` 成熟度周期。它保持现有产品表面不变，把代码结构、运维路径、文档和回归证据提升到可以继续长期开发的稳定基线。

## 包含内容

- Release gate 和 CI：Go format/test/race/vet/govulncheck、命令构建、OpenAPI lint、Worker test/typecheck/audit。
- Windows 本地 `scripts/foundation-check.ps1` 与 CI 对齐，支持 race、actionlint、govulncheck、Redocly lint 和 Worker audit。
- `scripts/github-actions-status.ps1` 可记录 pushed branch/head SHA、run 和 job 证据。
- `api/openapi.yaml` 描述稳定控制面 API，并进入 CI lint。
- SQLite schema 变更通过 `schema_migrations` 追踪。
- 代表性安全和运维路径写入 mutation audit events，并可用 `GET /api/v1/audit-events` / `supercdnctl audit-log` 查询。
- `internal/server/server.go` 收缩为构造、生命周期和 route 注册骨架。
- `cmd/supercdnctl/main.go` 收缩为 dispatcher/global-flag 入口。
- `cdn-doctor`、`site-doctor`、`switch-plan`、`switch-apply`、rollback/recovery/reconcile 等运维路径更明确。
- README、命令大全、运维手册、真实场景回归、成熟度审计和文档链接测试成型。
- 只读真实场景回归通过 `https://cyberstream-ipfs-0501.qwk.ccwu.cc/`。

## 验证

本地 release gate：

```powershell
$gcc = "E:\Tools\winlibs-x86_64-posix-seh-gcc-16.1.0-mingw-w64ucrt-14.0.0-r1\mingw64\bin"
$env:Path = "$gcc;$env:Path"
$env:CGO_ENABLED = "1"
$env:GOPROXY = "https://goproxy.cn,direct"
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
```

已观察结果：

- Go format、PowerShell syntax、GitHub Actions workflow lint 通过；
- `go test ./...`、`go test -race ./...`、`go vet ./...` 通过；
- `govulncheck ./...` 无漏洞；
- Windows server/CLI build 通过；
- OpenAPI lint 通过；
- Worker test/typecheck/audit 通过；
- 本地服务 `/healthz` 通过。

GitHub Actions release commit gate 通过，release body 中记录最终 run id。

只读真实场景证据：

- 状态：`ok`；
- Public URL：`https://cyberstream-ipfs-0501.qwk.ccwu.cc/`；
- HTML 和 SPA fallback 来源：`cloudflare_static`；
- JS asset 来源：`ipfs_gateway`；
- Edge manifest routing：`route`；
- failed steps：`0`。

## 已知边界

- 这是阶段性稳定/成熟里程碑，不是公开 GA。
- policy-level CDN 切换仍由 operator 控制；`switch-apply` 不修改 route profile、routing policy、Worker code 或 KV manifest。
- Cloudflare-backed metadata delete 不删除 Worker version、自定义域名或 KV。
- Cloudflare provider 写入具有最终一致性；恢复路径依赖证据、严格 probe 和 audit event。
- 后续涉及 Cloudflare 写入、DNS、Worker/KV、AList/OpenList、R2、IPFS/Pinata、手动切换或回滚写行为时，需要新增真实 provider canary。
