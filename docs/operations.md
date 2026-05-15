# Operations Runbook

[English](operations.md) | [简体中文](operations.zh-CN.md)

This runbook is the short operator path for a deployed Super CDN control plane. Use it when a site, bucket, resource line, rollback or release check needs a quick and repeatable sequence.

Deep references:

- Command book: [commands.md](commands.md)
- Parameter reference: [cli-reference.md](cli-reference.md)
- Real scenario regression: [real-scenario-regression.md](real-scenario-regression.md)
- Maintenance status: [maintenance-status.md](maintenance-status.md)
- Release checklist: [release-checklist.md](release-checklist.md)
- Maturity audit: [maturity-audit.md](maturity-audit.md)

## First Checks

Run these before changing state:

```powershell
supercdnctl doctor
supercdnctl resource-status -library <library>
supercdnctl routing-policy-status -policy <policy>
supercdnctl cloudflare-status -all
supercdnctl ipfs-status
```

Rules:

- Start with `doctor` after changing server URL, profile, token, config, DNS or storage credentials.
- Use root only when root-only provider details are needed. Non-root users should still get useful scoped diagnostics.
- Treat missing, stale or partial evidence as an investigation path, not as a reason to force a write.

## Site Triage

Use this sequence for Web delivery, SPA fallback, blank pages, CORS, MIME, stale signed routes or wrong provider line:

```powershell
supercdnctl probe-site -site <site> -spa-path /movie/123
supercdnctl probe-site -site <site> -spa-path /movie/123 -browser-render
supercdnctl site-doctor -site <site> -path /assets/app.js
supercdnctl route-explain -site <site> -path /assets/app.js -country CN
```

For preferred Cloudflare entry delivery:

```powershell
supercdnctl probe-site -site <site> -production -require-edge-static-html
supercdnctl probe-site -site <site> -production -require-edge-static-html -require-edge-manifest-assets
```

If signed AList/OpenList locators or resource-library health changed, refresh only after reviewing diagnostics:

```powershell
supercdnctl refresh-edge-manifest -site <site> -deployment <deployment>
```

## Bucket Triage

Use this sequence for reusable CDN objects:

```powershell
supercdnctl cdn-doctor -bucket <bucket> -path <logical_path>
supercdnctl replicas -object-id <object_id>
supercdnctl refresh-replicas -object-id <object_id>
supercdnctl repair-replicas -object-id <object_id> -target <library>
```

For cache operations, dry-run first when available:

```powershell
supercdnctl purge-bucket -bucket <bucket> -prefix <prefix> -dry-run
supercdnctl warmup-bucket -bucket <bucket> -path <logical_path> -dry-run
```

## Switching

Manual switching is explicit. Always plan first:

```powershell
supercdnctl switch-plan -bucket <bucket> -path <logical_path> -country CN
supercdnctl switch-plan -site <site> -path /assets/app.js -country CN
```

Apply only when the plan reports `safe_to_switch=true` and `apply_supported=true`:

```powershell
supercdnctl switch-apply -bucket <bucket> -path <logical_path> -target <library> -dry-run=false -confirm switch
supercdnctl switch-apply -site <site> -path /assets/app.js -target <library> -dry-run=false -confirm switch
```

Do not use `switch-apply` for `routing_policy`, `resource_failover`, Cloudflare Static or any case where metadata alone does not control real traffic.

## Rollback And Recovery

Always plan before recovery:

```powershell
supercdnctl rollback-plan -site <site> -deployment <deployment>
```

Use provider-aware rollback when the plan says Worker assets or KV manifests must move:

```powershell
supercdnctl rollback-apply -site <site> -deployment <deployment> -dir <historical_dist> -dry-run=false -confirm rollback
supercdnctl reconcile-deployment -site <site> -deployment <deployment>
```

For provider writes that succeeded but metadata or evidence did not settle:

```powershell
supercdnctl recover-cloudflare-static -site <site> -dir <dist> -domains <domain> -worker-name <worker> -version-id <version>
supercdnctl activate-cloudflare-static -site <site> -deployment <deployment> -dir <dist> -dry-run=false -confirm activate
supercdnctl recover-hybrid-edge -site <site> -deployment <deployment> -dir <dist> -domains <domain>
```

Keep recovery dry-run output with the incident notes. Confirmed writes should leave audit events and a passing strict probe.

## Cleanup

Use dry-run cleanup before deleting:

```powershell
supercdnctl gc -dry-run -older-than 1h
supercdnctl delete-deployment -site <site> -deployment <deployment> -dry-run
supercdnctl delete-bucket-object -bucket <bucket> -prefix <prefix> -force -delete-remote=false
```

Cloudflare-backed deployment metadata deletion does not remove Worker versions, custom domains or KV entries. Treat provider cleanup as a separate, deliberate operation.

## Release Or Refactor Check

Before claiming a refactor or release is stable:

```powershell
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
.\scripts\github-actions-status.ps1 -Wait -IncludeJobs
```

Run the read-only real-scenario regression when Web delivery, provider evidence, rollback, switching, DNS, storage providers or browser rendering changed:

```powershell
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -PublicUrl https://example.com/ `
  -SpaPath /movie/123 `
  -RequireEdgeStaticHtml `
  -RequireEdgeManifestAssets `
  -OutputPath .\data\real-regression-public.json
```

Ask for a mutating real-provider canary only after local gates, CI and read-only probes pass.
