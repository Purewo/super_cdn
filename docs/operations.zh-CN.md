# 运维手册

[English](operations.md) | 简体中文

这是已部署 Super CDN 控制平面的短路径运维手册。网站、资源桶、资源线、回滚或发布检查需要快速重复操作时，从这里开始。

深入参考：

- 命令大全：[commands.zh-CN.md](commands.zh-CN.md)
- 参数参考：[cli-reference.zh-CN.md](cli-reference.zh-CN.md)
- 真实场景回归：[real-scenario-regression.zh-CN.md](real-scenario-regression.zh-CN.md)
- 发布检查清单：[release-checklist.zh-CN.md](release-checklist.zh-CN.md)
- 成熟度审计：[maturity-audit.zh-CN.md](maturity-audit.zh-CN.md)

## 第一组检查

改状态前先跑：

```powershell
supercdnctl doctor
supercdnctl resource-status -library <library>
supercdnctl routing-policy-status -policy <policy>
supercdnctl cloudflare-status -all
supercdnctl ipfs-status
```

规则：

- 修改 server URL、profile、token、配置、DNS 或存储凭据后，先跑 `doctor`。
- 只有需要 root-only provider 细节时才用 root；普通用户也应拿到有用的范围内诊断。
- 证据缺失、过期或不完整时，应进入调查路径，不要强制写入。

## 网站排查

Web 交付、SPA fallback、白屏、CORS、MIME、过期签名路由或错误资源线，按顺序运行：

```powershell
supercdnctl probe-site -site <site> -spa-path /movie/123
supercdnctl probe-site -site <site> -spa-path /movie/123 -browser-render
supercdnctl site-doctor -site <site> -path /assets/app.js
supercdnctl route-explain -site <site> -path /assets/app.js -country CN
```

Cloudflare entry 模式下的生产路径检查：

```powershell
supercdnctl probe-site -site <site> -production -require-edge-static-html
supercdnctl probe-site -site <site> -production -require-edge-static-html -require-edge-manifest-assets
```

如果 AList/OpenList 签名 locator 或资源库健康状态变化，先看诊断，再刷新 manifest：

```powershell
supercdnctl refresh-edge-manifest -site <site> -deployment <deployment>
```

## 资源桶排查

可复用 CDN 对象按顺序检查：

```powershell
supercdnctl cdn-doctor -bucket <bucket> -path <logical_path>
supercdnctl replicas -object-id <object_id>
supercdnctl refresh-replicas -object-id <object_id>
supercdnctl repair-replicas -object-id <object_id> -target <library>
```

缓存操作优先 dry-run：

```powershell
supercdnctl purge-bucket -bucket <bucket> -prefix <prefix> -dry-run
supercdnctl warmup-bucket -bucket <bucket> -path <logical_path> -dry-run
```

## 手动切换

切换必须显式，永远先 plan：

```powershell
supercdnctl switch-plan -bucket <bucket> -path <logical_path> -country CN
supercdnctl switch-plan -site <site> -path /assets/app.js -country CN
```

只有当计划返回 `safe_to_switch=true` 且 `apply_supported=true` 时才执行：

```powershell
supercdnctl switch-apply -bucket <bucket> -path <logical_path> -target <library> -dry-run=false -confirm switch
supercdnctl switch-apply -site <site> -path /assets/app.js -target <library> -dry-run=false -confirm switch
```

不要把 `switch-apply` 用在 `routing_policy`、`resource_failover`、Cloudflare Static，或任何元数据不能真实控制流量的场景。

## 回滚与恢复

恢复前先生成计划：

```powershell
supercdnctl rollback-plan -site <site> -deployment <deployment>
```

计划提示 Worker assets 或 KV manifest 必须移动时，用 provider-aware 回滚：

```powershell
supercdnctl rollback-apply -site <site> -deployment <deployment> -dir <historical_dist> -dry-run=false -confirm rollback
supercdnctl reconcile-deployment -site <site> -deployment <deployment>
```

provider 写入成功但元数据或证据未落库时：

```powershell
supercdnctl recover-cloudflare-static -site <site> -dir <dist> -domains <domain> -worker-name <worker> -version-id <version>
supercdnctl activate-cloudflare-static -site <site> -deployment <deployment> -dir <dist> -dry-run=false -confirm activate
supercdnctl recover-hybrid-edge -site <site> -deployment <deployment> -dir <dist> -domains <domain>
```

把 dry-run 输出保存在事故记录里。确认写入后应留下 audit event，并通过严格 probe。

## 清理

删除前先 dry-run：

```powershell
supercdnctl gc -dry-run -older-than 1h
supercdnctl delete-deployment -site <site> -deployment <deployment> -dry-run
supercdnctl delete-bucket-object -bucket <bucket> -prefix <prefix> -force -delete-remote=false
```

Cloudflare-backed deployment 的元数据删除不会删除 Worker 版本、自定义域名或 KV 条目；provider 清理应单独、明确执行。

## 发布或重构检查

声称重构或发布稳定前运行：

```powershell
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
.\scripts\github-actions-status.ps1 -Wait -IncludeJobs
```

Web 交付、provider 证据、回滚、切换、DNS、存储 provider 或浏览器渲染变化后，运行只读真实场景回归：

```powershell
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -PublicUrl https://example.com/ `
  -SpaPath /movie/123 `
  -RequireEdgeStaticHtml `
  -RequireEdgeManifestAssets `
  -OutputPath .\data\real-regression-public.json
```

只有本地 gate、CI 和只读探测通过后，才考虑真实 provider 写入 canary。
