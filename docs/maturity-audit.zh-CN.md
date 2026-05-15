# Super CDN 成熟度审计

[English](maturity-audit.md) | 简体中文

最后更新：2026-05-16 Asia/Shanghai。

本文跟踪 `v0.4.0` 阶段发布后的成熟度工作。它只接受证据，不因为一个大测试命令通过就笼统宣称项目成熟。

## 目标

让 Super CDN 足够适合长期开发和 operator 使用，同时不隐藏剩余风险。

具体成功标准：

- 本地和 CI release gate 可重复；
- API 与 CLI 行为有明确契约；
- 数据库变更有版本记录；
- 安全和运维变更有可用审计链路；
- 大型 server 和 CLI 文件有清晰所有权边界；
- 诊断输出能说明下一步，而不只是报错；
- 手动 CDN 线路切换必须显式、确认、可审计；
- 回滚路径不能在 metadata、Worker assets、KV manifest 没一起移动时声称真实流量已移动；
- 不支持的路径要明确记录，不能表现成安全 apply 命令。

## 证据清单

核心证据包括：

| 要求 | 当前产物 | 验证方式 |
| --- | --- | --- |
| 本地 release gate | `scripts/foundation-check.ps1` | gofmt、PowerShell syntax、actionlint、Go test/vet/vuln/build、可选 Windows race、OpenAPI lint、Worker test/typecheck/audit、service healthz |
| CI release gate | `.github/workflows/ci.yml`、`scripts/github-actions-status.ps1` | Go format/test/race/vet/vuln/build、OpenAPI lint、Worker test/typecheck/audit，并按 branch/head SHA 查询 run |
| API 契约 | `api/openapi.yaml` | Redocly lint 通过，核心 operator response 不再依赖大块 `AnyObject` |
| CLI 文档覆盖 | `docs/commands.md`、`cmd/supercdnctl/command_docs_test.go` | 缺命令文档或 `usage()` 输出会让测试失败 |
| 文档链接完整性 | `internal/docscheck/markdown_links_test.go` | README 和 `docs/*.md` 的本地链接必须可解析 |
| 运维手册 | `docs/operations.md` | first checks、site/bucket triage、switching、rollback/recovery、cleanup、release/refactor check |
| 版本化迁移 | `internal/db` | DB 测试覆盖旧库升级和 migration history |
| 审计查询 | `GET /api/v1/audit-events`、`supercdnctl audit-log` | workspace scoped 查询、viewer 拒绝、过滤参数 |
| Server 边界 | `internal/server/server.go` 与拆分文件 | `server.go` 仅保留构造、生命周期和 route plumbing |
| CLI 边界 | `cmd/supercdnctl/main.go` 与命令文件 | `main.go` 仅保留 dispatcher/global flag plumbing |
| 手动切换 | `switch-plan`、`switch-apply` | 先 plan，写入默认 dry-run，确认后审计，policy/failover 不支持 metadata-only apply |
| 回滚安全 | `rollback-plan`、`rollback-apply`、recovery 命令 | Cloudflare-backed 目标不能 metadata-only rollback |
| 真实回归 | `scripts/real-scenario-regression.ps1` | 只读收集 doctor、cdn-doctor、site-doctor、probe-site、reconcile-deployment 证据 |
| 维护维稳状态 | `docs/maintenance-status.zh-CN.md` | `main` 已在 `v0.5.0` 和 commit `8dd3b16` 后进入维护维稳基线。后续先等小范围用户测试反馈，或只做文档、运维修正和有证据的定向修复。 |

英文原文保留完整长表和具体 commit/run/canary 证据：[maturity-audit.md](maturity-audit.md)。

## 已接受边界

这些是有意的产品边界，不是隐藏缺陷：

- Policy-level CDN 切换由 operator 控制。`switch-plan` 报告 readiness，`switch-apply` 拒绝 routing policy / resource failover 的 metadata-only 写入，并记录拒绝审计。
- Metadata delete 不清理 Cloudflare Worker versions、自定义域名或 KV 条目。dry-run 应暴露 blocker 和 provider evidence。
- Cloudflare provider 写入是最终一致的，不是与 Super CDN metadata 完全事务化。成熟边界是显式 evidence repair：`reconcile-deployment`、`recover-cloudflare-static`、`activate-cloudflare-static`、`rollback-apply`、`recover-hybrid-edge` 都要求 source/provider evidence、严格 live probe 和 audit event。

## 剩余缺口

当前清单没有明确 blocker。以后任何成熟度声明依赖未验证的本地、CI、API 或 live-provider 边界时，应先把缺口写到这里。

## 下一步

1. 先等小范围用户测试，拿到具体失败或使用摩擦后再改产品行为。
2. 维稳期只做文档、运维修正、CI/安全维护，或有复现证据的定向修复。
3. 修改共享 Go 行为时，继续用 Windows race gate：临时把 WinLibs GCC 加到 `PATH`，设置 `CGO_ENABLED=1`，运行 `.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race`。
4. package-boundary 清理等维稳闸门解除后再恢复；如果测试问题要求定向重构，则按最小切片处理。
