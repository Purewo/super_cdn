# Super CDN 交接文档

[English](tomorrow-plan.md) | 简体中文

最后更新：2026-05-16 Asia/Shanghai。

这是中文交接版。英文 [tomorrow-plan.md](tomorrow-plan.md) 保留完整历史记录、canary 域名、commit、CI run 和生产备份路径；中文版本记录当前可执行入口，避免下次会话重新摸底。

## 当前状态

项目已经进入维护维稳状态。当前要保护 `v0.5.0` 记录的行为，以及 post-release 用户上传配额流程 commit `8dd3b16`，等待下一轮小范围用户测试反馈后再决定是否继续改产品行为。

当前第一入口是 [maintenance-status.zh-CN.md](maintenance-status.zh-CN.md)。`v0.5.0` 仍是阶段性成熟里程碑；[maturity-audit.zh-CN.md](maturity-audit.zh-CN.md)、[operations.zh-CN.md](operations.zh-CN.md) 和相关边界文档继续作为证据和运维参考。

v0.1 和 v0.2.0 已作为稳定内部里程碑关闭。`v0.1.x` feature-freeze，`v0.2.x` 只做 bugfix、文档、运维硬化和 IPFS/Web hosting 回归。历史 roadmap 仍可作为上下文，但维稳闸门打开前不要启动新的产品周期。

## 下次优先级

1. 先读 [maintenance-status.zh-CN.md](maintenance-status.zh-CN.md)。
2. 查看用户小范围测试反馈、当前工作区和最新 CI。
3. 如果用户报告 bug，先复现，再做最小定向修复。
4. 如果行为变化，同 patch 更新 OpenAPI、命令文档、README、中文文档和审计覆盖。
5. 如果没有具体问题，只做文档、runbook、CI/安全维护和运维修正。
6. 不要从 Phase 0 重启，也不要主动开启 UI、路由重设计、自动 CDN 切换或大范围 package-boundary work。

## 当前关键边界

- Web hosting：推荐模式是 Cloudflare entry 加非入口资源库。Go entry 用于测试/集成/兼容；R2 是 CDN/object acceleration，R2-backed Web hosting 是 legacy compatibility。
- 静态资源 failover：不能回退到 Go origin，只能在已包含对象的 ready 资源库之间切换。
- Policy switching：`switch-plan` 只读；`switch-apply` 只支持非 policy、非 resource-failover、非 Cloudflare Static 的单对象/路径 primary-target 切换。
- Rollback：Cloudflare Static 和 `hybrid_edge` 不能 metadata-only rollback；必须 provider-aware redeploy、manifest 写入和 strict probe。
- Provider writeback/recovery：必须有 source/provider evidence、真实域名 probe 和 audit event。
- GitHub 网络：本机 GitHub 操作不稳定时，用一次性 `127.0.0.1:10808` 代理，不要设置全局 Git proxy。

## 验证命令

常规本地检查：

```powershell
go test ./...
go test ./cmd/supercdnctl
go test ./internal/docscheck
```

发布或共享行为检查：

```powershell
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
.\scripts\github-actions-status.ps1 -Wait -IncludeJobs
```

只读真实场景回归：

```powershell
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -PublicUrl https://cyberstream-ipfs-0501.qwk.ccwu.cc/ `
  -SpaPath /movie/123 `
  -RequireEdgeStaticHtml `
  -RequireEdgeManifestAssets `
  -OutputPath .\data\real-regression-public.json
```

## 历史上下文

英文交接文档包含大量历史证据：

- post-`v0.4.0` refactor 进度；
- Web hosting、smart failover、operator maturity、rollback safety、API contract、release gate、audit、Cloudflare live validation；
- canary 域名、deployment id、commit、CI run 和生产备份路径；
- 旧 cycle 的 onboarding hardening 计划。

这些历史仍然有价值，但下一次开发不要从历史段落重新规划；先看当前状态和边界文档。
