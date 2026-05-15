# Super CDN 维护状态

[English](maintenance-status.md) | 简体中文

最后更新：2026-05-16 Asia/Shanghai。

这是当前项目的第一入口交接文档。Super CDN 已进入维护维稳状态：下一步是用户小范围测试，不是继续开启大功能或大重构周期。

## 当前基线

- 稳定基线：`v0.5.0`。
- 最新 post-release 代码基线：commit `8dd3b16`（`add user upload quota workflow`）。
- 当前策略：保护已记录的行为，收集真实用户反馈，在小范围测试结束前只做定向修复。
- 暂缓事项：大范围重构、新产品表面、自动 CDN 切换、provider 写行为变更；除非测试结果暴露了具体缺陷，否则不要提前推进。

## 维稳期可以改什么

默认可以做：

- 文档、示例、runbook 修正；
- 有复现路径的 bug fix，且变更范围尽量小；
- 针对确认回归补测试；
- 保持当前版本可用所需的部署/配置修正；
- 不改变产品行为的 CI、安全扫描或依赖维护。

需要先明确评审：

- 新 CLI 命令或 API mutation 路由；
- 配额、认证、workspace scope、审计行为变更；
- Cloudflare Worker/KV、DNS、R2、AList/OpenList、IPFS/Pinata 或回滚写行为；
- routing policy、resource failover 语义；
- 不针对已确认问题、只是继续搬代码的 package-boundary 重构。

## 小范围测试清单

下一轮面向真实用户的验证按这个顺序走：

1. 团队认证：`invite-user`、`login`、`whoami`、`logout`、本地 profile、错误 token 行为。
2. 配额流程：`quota`、`request-quota`、root `quota-requests`、`approve-quota`、`reject-quota`、`set-user-quota`，以及一次低配额上传拒绝。
3. 资源桶流程：创建 CDN 桶，上传单文件，运行 `upload-bucket-dir`，列对象，再跑 `cdn-doctor`。
4. 静态网站流程：`create-site`、`deploy-site`、`update-site`、`probe-site`、`site-doctor`、`reconcile-deployment`。
5. 运维证据：`doctor`、`audit-log`、dry-run `switch-plan`，以及在真实站点证据足够时运行 dry-run rollback/recovery 命令。
6. 失败可见性：过期 token、对象不存在、provider timeout、配额超限、不支持的切换/回滚，都应返回可行动错误，而不是静默成功。

记录失败时带上命令、server URL、workspace/profile、脱敏输出，以及操作是只读还是写入。

## 后续变更验证门槛

纯文档变更：

```powershell
go test ./internal/docscheck
git diff --check
```

CLI/API/用户可见行为变更：

```powershell
go test ./...
go vet ./...
go build ./cmd/supercdn ./cmd/supercdnctl
go test ./cmd/supercdnctl
go test ./internal/docscheck
git diff --check
```

发布、重构或 provider 相关变更，按 [release-checklist.zh-CN.md](release-checklist.zh-CN.md) 的完整门槛走，并在 push 后检查 GitHub Actions：

```powershell
.\scripts\github-actions-status.ps1 -Wait -IncludeJobs
```

如果改到 Web delivery、storage provider、switching、rollback、DNS 或 Cloudflare 行为，先跑 [real-scenario-regression.zh-CN.md](real-scenario-regression.zh-CN.md)，再决定是否申请真实 provider canary。

## 下次阅读顺序

回到项目时按这个顺序读：

1. 本文档。
2. [README.md](../README.md) 或 [README.zh-CN.md](../README.zh-CN.md)。
3. [operations.zh-CN.md](operations.zh-CN.md)：运维排查入口。
4. [maturity-audit.zh-CN.md](maturity-audit.zh-CN.md)：当前成熟度证据清单。
5. [commands.zh-CN.md](commands.zh-CN.md) 和 [cli-reference.zh-CN.md](cli-reference.zh-CN.md)：高阶 CLI 用法。
6. [refactor-plan.zh-CN.md](refactor-plan.zh-CN.md) 和 [tomorrow-plan.zh-CN.md](tomorrow-plan.zh-CN.md)：只在维稳闸门解除后，或测试反馈要求定向重构时再进入。
