# Real Scenario Regression

[English](real-scenario-regression.md) | [简体中文](real-scenario-regression.zh-CN.md)

This runbook is for read-only regression checks against a real Super CDN server or public site after refactor, release, deployment or provider-recovery work.

The default path is intentionally non-mutating. It collects evidence from existing operator commands and fails when any required probe fails. Use separate canary commands for mutating flows such as bucket uploads or rollback writes.

## Script

Use `scripts/real-scenario-regression.ps1` from the repository root.

The script can call either `bin\supercdnctl.exe` when it exists, or `go run .\cmd\supercdnctl --` with `-UseGoRun`. It writes a JSON report to stdout and optionally to `-OutputPath`.

Failed steps retry once by default because DNS, provider gateways and Cloudflare custom domains can be briefly inconsistent during read-only checks. Use `-Retries 0` when you need a single attempt.

Read-only public site probe:

```powershell
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -PublicUrl https://example.com/ `
  -SpaPath /movie/123 `
  -RequireEdgeStaticHtml `
  -RequireEdgeManifestAssets `
  -OutputPath .\data\real-regression-public.json
```

Authenticated site probe:

```powershell
$env:SUPERCDN_TOKEN = "sct_xxx"
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -Server https://qwk.ccwu.cc `
  -Site cyberstream `
  -SitePath /assets/app.js `
  -Deployment dpl_xxx `
  -SpaPath /movie/123 `
  -RequireEdgeStaticHtml `
  -RequireEdgeManifestAssets `
  -OutputPath .\data\real-regression-site.json
```

Authenticated bucket probe:

```powershell
$env:SUPERCDN_TOKEN = "sct_xxx"
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -Server https://qwk.ccwu.cc `
  -Bucket downloads `
  -BucketPath release/v1/app.zip `
  -OutputPath .\data\real-regression-bucket.json
```

Add `-RequireBrowserRender` only when Chrome or Edge is installed on the machine running the script. The browser render step catches blank pages that pass HTTP, MIME and edge-header checks.

## What The Script Runs

Depending on the arguments, the script runs:

| Input | Commands |
| --- | --- |
| Auth context without `-SkipDoctor` | `doctor` |
| `-Bucket` | `cdn-doctor -bucket ... [-path ...]` |
| `-Site` | `site-doctor -site ... [-path ...]` and `probe-site -site ...` |
| `-Site -Deployment` | `reconcile-deployment -site ... -deployment ...` |
| `-PublicUrl` | `probe-site -url ...` |

All steps are read-only. Skipped steps are recorded in the JSON report, for example when a token or profile is required but not present.

## Required Real Scenarios

Run these before claiming a refactor or release is mature enough for production operators:

| Scenario | Minimum evidence |
| --- | --- |
| Ordinary Cloudflare Static site | `probe-site` or script report with `-RequireEdgeStaticHtml`, HTML cache revalidation and immutable JS/CSS asset cache when generated headers are expected. |
| Hybrid edge site | `probe-site` or script report with `-RequireEdgeStaticHtml -RequireEdgeManifestAssets`; at least one JS/CSS asset must show manifest routing. |
| AList/OpenList resource line | A real upload or deployment that routes through the target library, followed by `cdn-doctor` or `site-doctor` and a successful public probe. |
| R2/object CDN bucket | `create-cdn-bucket`, `upload-bucket -warmup`, `cdn-doctor`, direct public URL or Super CDN URL warmup evidence. |
| IPFS/Pinata line | `ipfs-status` plus `ipfs-smoke` or an IPFS-backed `hybrid_edge` site probe showing `ipfs_gateway`. |
| Cloudflare Static rollback | A -> B -> rollback A with `rollback-plan`, `rollback-apply` or equivalent provider redeploy, `reconcile-deployment`, strict probe and audit event. |
| Hybrid edge rollback | A -> B -> rollback A with Worker assets and active KV manifest republished together, strict probe and audit event. |
| Provider writeback/recovery | `reconcile-deployment`, recovery dry-run, confirmed recovery/writeback only when evidence matches, then strict probe and audit event. |
| Manual line switch | `switch-plan` first; `switch-apply` only when `safe_to_switch=true` and `apply_supported=true`, then `cdn-doctor`/`site-doctor` and the rollback command evidence. |

## When More Real Environment Testing Is Needed

Ask for a real environment test when a change touches any of these boundaries:

- Cloudflare Worker publishing, custom domains, DNS, Worker routes, KV manifest writes or Cloudflare Static headers.
- AList/OpenList visibility, signed direct URLs, mount paths or provider health behavior.
- R2 bucket provisioning, public domains, CORS, purge or direct object delivery.
- Pinata/IPFS upload, pin refresh, gateway probing or CID metadata.
- Rollback, recovery, activation, provider writeback or manual switching.
- Browser rendering behavior for complex SPAs.

Local CI can prove code health and contracts, but it cannot prove provider propagation, DNS, signed URLs, custom domains, or real browser rendering on a public site.

## Report Handling

Keep the JSON report with the release or refactor evidence. It should include:

- the exact command line for every step;
- exit code and duration;
- parsed JSON output when the command returned JSON;
- skipped steps and the reason;
- failed steps with stderr.

The operator commands already redact signed URL query values by default. Do not rerun probes with `-redact-urls=false` unless the report stays local and the signed URLs are allowed to be exposed.
