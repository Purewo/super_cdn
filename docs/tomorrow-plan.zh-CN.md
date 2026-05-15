# Super CDN 交接文档

[English](tomorrow-plan.md) | 简体中文

最后更新：2026-05-15 Asia/Shanghai。

这是中文交接版。英文 [tomorrow-plan.md](tomorrow-plan.md) 保留完整历史记录、canary 域名、commit、CI run 和生产备份路径；中文版本记录当前可执行入口，避免下次会话重新摸底。

## 当前状态

服务仍处于开发模式，不需要维护旧静态站点部署流程的兼容性。

`v0.5.0` 是当前阶段性成熟里程碑。它关闭了 post-`v0.4.0` 的重构、运维、文档和只读真实场景回归周期。后续工作从 [refactor-plan.zh-CN.md](refactor-plan.zh-CN.md)、[maturity-audit.zh-CN.md](maturity-audit.zh-CN.md)、[operations.zh-CN.md](operations.zh-CN.md) 和相关边界文档开始，不要重新做大范围 cleanup。

v0.1 和 v0.2.0 已作为稳定内部里程碑关闭。`v0.1.x` feature-freeze，`v0.2.x` 只做 bugfix、文档、运维硬化和 IPFS/Web hosting 回归。新产品工作从 [v0.3-roadmap.zh-CN.md](v0.3-roadmap.zh-CN.md) 及后续计划开始。

## 下次优先级

1. 不要从 Phase 0 重启。
2. 先看 `v0.5.0` 后的最新工作区状态和 CI。
3. 若继续重构，只做边界明确的窄 package-boundary extraction。
4. 修改 API mutation 时，同 patch 更新 OpenAPI、审计和文档。
5. 涉及 Web delivery、provider evidence、rollback、switching、DNS 或 storage provider 行为时，跑只读真实场景回归。
6. 本地 gate 和只读 probe 通过后，才请求 mutating real-provider canary。

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
