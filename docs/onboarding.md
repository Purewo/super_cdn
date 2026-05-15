# Super CDN Onboarding Guide

[English](onboarding.md) | [简体中文](onboarding.zh-CN.md)

This guide is the shortest supported path for a real user to create a CDN bucket, upload files, publish a Web site, and send useful diagnostics when something fails.

## 1. Connect And Run Doctor

Use a saved profile when possible:

```powershell
.\bin\supercdnctl.exe -server https://qwk.ccwu.cc -profile alice login -invite-token sci_xxx
.\bin\supercdnctl.exe -profile alice whoami
.\bin\supercdnctl.exe -profile alice quota
.\bin\supercdnctl.exe -profile alice doctor
```

For one-off local testing, set the token in the shell:

```powershell
$env:SUPERCDN_TOKEN = "sct_xxx"
.\bin\supercdnctl.exe -server http://127.0.0.1:8080 doctor
```

`doctor` is the first support report. It checks authentication, database reachability, storage targets, route profiles, staging storage, resource-library status and routing-policy status without printing tokens or secrets.

Non-root users start with a cumulative 10 GiB upload quota. Run `quota` before a large upload. If the remaining quota is not enough, run `request-quota -max-gb 20 -reason "release test"` and ask a root admin to approve it with `approve-quota`.

## 2. Choose A Resource Line

Use the shortcut that matches the asset type and audience:

| Goal | Command | Best For |
| --- | --- | --- |
| Overseas object CDN | `create-cdn-bucket` | images, video, archives and public downloads on R2/Cloudflare |
| Domestic CDN bucket | `create-domestic-cdn-bucket -line mobile|telecom|unicom|all` | China-facing static assets on AList/OpenList-backed libraries |
| Durable IPFS bucket | `create-ipfs-bucket` | immutable assets where CID/gateway delivery is useful |

Examples:

```powershell
.\bin\supercdnctl.exe create-cdn-bucket -slug overseas-assets -name overseas-assets -types image,archive
.\bin\supercdnctl.exe create-domestic-cdn-bucket -slug mobile-assets -line mobile -types image,document
.\bin\supercdnctl.exe create-ipfs-bucket -slug durable-assets -types image,archive
```

Use versioned logical paths for immutable CDN files, for example `images/v1/poster.jpg`.

## 3. Upload One File

```powershell
.\bin\supercdnctl.exe upload-bucket -bucket overseas-assets -file .\poster.jpg -path images/v1/poster.jpg -asset-type image -warmup
.\bin\supercdnctl.exe cdn-doctor -bucket overseas-assets -path images/v1/poster.jpg
```

The upload output keeps the server response fields and adds support-friendly fields:

| Field | Meaning |
| --- | --- |
| `copy_urls.public_url` | Stable Super CDN URL to share or embed |
| `copy_urls.cdn_url` | Direct CDN/storage URL when the backend exposes one |
| `copy_urls.storage_url` | Same direct storage URL with explicit naming |
| `next_commands` | Diagnostic command to run or paste into support notes |

If upload fails, the CLI error includes a `cdn-doctor` command for the same bucket/path.

## 4. Batch Upload A Folder

Plan first:

```powershell
.\bin\supercdnctl.exe upload-bucket-dir -bucket overseas-assets -dir .\release -prefix release/v1 -dry-run -report-file .\upload-plan.json
```

Upload with a persistent report:

```powershell
.\bin\supercdnctl.exe upload-bucket-dir -bucket overseas-assets -dir .\release -prefix release/v1 -skip-existing -retry 2 -report-file .\upload-report.json
```

The batch report includes `summary`, `total`, `succeeded`, `skipped`, `failed`, per-file `results`, `report_saved_to` and `next_commands`. The command finishes the whole batch before returning a nonzero status, so the report is the support artifact even for partial failure.

For a partial failure, rerun with:

```powershell
.\bin\supercdnctl.exe upload-bucket-dir -bucket overseas-assets -dir .\release -prefix release/v1 -skip-existing -retry 2 -report-file .\upload-report.json
```

## 5. Publish A Static Web Site

For an ordinary Cloudflare-native static site:

```powershell
.\bin\supercdnctl.exe create-site -site blog -profile overseas -target cloudflare_static -domains blog.qwk.ccwu.cc
.\bin\supercdnctl.exe deploy-site -site blog -dir .\dist -target cloudflare_static -domains blog.qwk.ccwu.cc -static-spa
.\bin\supercdnctl.exe probe-site -site blog -spa-path /movie/123 -require-edge-static-html -require-immutable-assets
```

For a hybrid site where entry HTML stays on Cloudflare Static and non-entry resources use resource libraries through the Worker manifest:

```powershell
.\bin\supercdnctl.exe create-site -site cyberstream -profile china_mobile -target hybrid_edge -domains cyberstream.qwk.ccwu.cc
.\bin\supercdnctl.exe deploy-site -site cyberstream -dir .\dist -target hybrid_edge -profile china_mobile -domains cyberstream.qwk.ccwu.cc -static-spa -resource-failover
.\bin\supercdnctl.exe probe-site -site cyberstream -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
```

Only use `-resource-failover` when the selected route profile has a primary plus at least one backup target. New smart-routing or failover deployments wait for ready candidates before publishing the active edge manifest.

## 6. Web Diagnostics

When a published site fails or renders blank, collect these in order:

```powershell
.\bin\supercdnctl.exe site-doctor -site cyberstream
.\bin\supercdnctl.exe site-doctor -site cyberstream -path /assets/app.js -country CN
.\bin\supercdnctl.exe probe-site -site cyberstream -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
.\bin\supercdnctl.exe route-explain -site cyberstream -path /assets/app.js -country CN
```

`site-doctor` packages the active deployment, hosting target, route explanation, selected candidate, skipped candidates, redacted storage URLs and expected edge headers.

## 7. Common Failure Checklist

| Symptom | First Command | What To Look For |
| --- | --- | --- |
| CLI says token is missing or rejected | `doctor` | Profile/server mismatch or expired invite token |
| Bucket upload fails | `cdn-doctor -bucket <slug> -path <path>` | Bucket state, route profile, storage target, policy/constraint error |
| Batch upload partly fails | read `upload-report.json` | Failed paths, attempt counts, retry command in `next_commands` |
| Public URL works but direct storage URL fails | `cdn-doctor` | Signed URL expiry, replica state, AList/OpenList HEAD-vs-GET behavior |
| Site has a blank screen | `probe-site -browser-render` then `site-doctor -path <asset>` | HTML status, JS/CSS MIME, CORS, manifest headers, selected route and headless screenshot blank-page check |
| Hybrid deploy waits or fails before publishing | `routing-policy-status` | At least two ready candidates are required for new smart/failover routes |

## 8. Cleanup

Use conservative GC for stale local staging files:

```powershell
.\bin\supercdnctl.exe gc -dry-run -older-than 1h
.\bin\supercdnctl.exe gc -dry-run=false -older-than 1h
```

This first cleanup pass does not delete remote objects. It is safe to run repeatedly and reports per-item status.
