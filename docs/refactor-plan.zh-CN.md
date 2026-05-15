# Super CDN 重构计划

[English](refactor-plan.md) | 简体中文

最后更新：2026-05-15 Asia/Shanghai。

基线：`v0.5.0` 是当前阶段性成熟版本。它冻结了 post-`v0.4.0` 的结构、运维、文档和只读真实场景回归成熟度周期，后续产品功能应从这个基线继续。

## main 上的进展

`v0.4.0` 后已经完成：

- Phase 0：GitHub Actions CI 和 `docs/release-checklist.md`。
- Phase 1/2：diagnostics、GC、asset buckets、site deployments、edge manifests 和多个 CLI 命令组从最大文件中拆出。
- Phase 3：新增并链接 `api/openapi.yaml`。
- Phase 4：新增 `schema_migrations`，把旧 additive columns 转为命名 migration，并增加旧库升级测试。
- Phase 5：代表性安全和运维 mutation 写入 `audit_events`，并增加审计查询 API 与 `supercdnctl audit-log`。
- Phase 6：`internal/server/server.go` 收缩为 server skeleton，`cmd/supercdnctl/main.go` 收缩为 dispatcher。
- 后续 package boundary：`internal/deploymenttarget`、`internal/deploymentevidence`、`internal/edgeheaders`、`internal/cloudflarestatic`、`internal/urlredact` 和 server audit action constants。
- 运维成熟度：`cdn-doctor`、`site-doctor`、`switch-plan`、`switch-apply`、`rollback-plan`、`rollback-apply`、recovery/writeback/reconcile 路径形成证据化边界。
- 文档成熟度：命令覆盖测试、文档本地链接测试、运维手册、真实场景回归手册已经纳入验证。

下一次重构入口：

- 用 [maturity-audit.zh-CN.md](maturity-audit.zh-CN.md) 做当前证据清单；
- policy-level switch 写入前先看 [policy-switching-boundary.zh-CN.md](policy-switching-boundary.zh-CN.md)；
- Cloudflare rollback 写行为前先看 [cloudflare-rollback-boundary.zh-CN.md](cloudflare-rollback-boundary.zh-CN.md)；
- Phase 6 大文件缩减已经基本完成，后续只做边界明确的窄 package-boundary 工作；
- 不要为了行数继续搬代码；下一步应是 focused tests、docs alignment，或 ownership 清晰的 package extraction；
- 不要从 CI/OpenAPI/migrations/audit 重新开始，除非发现回归。

## 目标

把内部稳定代码库变成适合长期维护的产品代码库，同时不改变已经可用的产品表面。

重构应保持 `v0.4.0` 行为，提升：

- 小模块替代超大控制面和 CLI 文件；
- 可重复 CI 与 release gate；
- 明确 API 契约；
- 版本化数据库迁移；
- 可用审计链路；
- 控制面、存储、路由、Web 托管和 Cloudflare 操作之间的清晰所有权边界。

## 非目标

- 不启动新 UI。
- 不改变推荐 Web 托管模型。
- 不在保护当前行为前重设计路由语义。
- 不删除旧兼容路径，除非测试和文档证明有等价迁移路径。
- 不做大范围纯格式化改写。

## 原始痛点

- `internal/server/server.go` 曾混合 routing、handler、service logic、diagnostics、Cloudflare orchestration、storage coordination 和 response shaping。
- `cmd/supercdnctl/main.go` 曾混合 command parsing、HTTP transport、output formatting、local packaging、Cloudflare Static publishing 和 operator workflow。
- 仓库曾缺少 CI、正式 OpenAPI、版本化 migration 和一致 audit writes。

## 重构顺序

英文原文保留完整 phase 细节：[refactor-plan.md](refactor-plan.md)。中文执行摘要如下：

1. 安全网先行：CI、本地 foundation check、release checklist。
2. 按产品表面拆 server handler/service。
3. 按命令组拆 CLI。
4. 建立 OpenAPI 契约。
5. 建立 schema migration 记录。
6. 把 mutation audit 写入重要路径。
7. 继续做窄 package-boundary 提取，只在边界清晰时移动代码。

## 验证命令

窄切片后至少运行：

```powershell
go test ./...
go test ./cmd/supercdnctl
go test ./internal/docscheck
```

发布或影响共享行为时运行：

```powershell
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
.\scripts\github-actions-status.ps1 -Wait -IncludeJobs
```

Web/provider/rollback/switching/DNS/storage 行为变化后，运行只读真实场景回归，并在需要写 provider 前请求 mutating canary。

## 停止条件

出现这些情况时先停下来：

- 需要改变产品语义才能继续搬代码；
- 测试无法证明行为未变；
- OpenAPI、文档或 audit 与代码不同步；
- Cloudflare 真实流量边界无法验证；
- 变更变成大范围格式化或行数导向重排。

## 下次会话入口

从 `v0.5.0` 开始。先读：

- [maturity-audit.zh-CN.md](maturity-audit.zh-CN.md)；
- [operations.zh-CN.md](operations.zh-CN.md)；
- [policy-switching-boundary.zh-CN.md](policy-switching-boundary.zh-CN.md)；
- [cloudflare-rollback-boundary.zh-CN.md](cloudflare-rollback-boundary.zh-CN.md)；
- [cloudflare-writeback-recovery-boundary.zh-CN.md](cloudflare-writeback-recovery-boundary.zh-CN.md)。

不要从 Phase 0 重来。
