# Super CDN Command Book

This is the workflow-oriented command book for advanced `supercdnctl` users. It answers "which command should I run next?" and links back to the parameter-level reference when you need exact request and response shapes.

Deep reference:

- Parameter details: [cli-reference.md](cli-reference.md)
- New-user flow: [onboarding.md](onboarding.md)
- Operations runbook: [operations.md](operations.md)
- API contract: [../api/openapi.yaml](../api/openapi.yaml)

## Conventions

Examples use the local Go runner:

```powershell
go run .\cmd\supercdnctl -- <command> ...
```

Installed binaries use the same arguments:

```powershell
.\bin\supercdnctl.exe <command> ...
```

Global flags and environment:

| Flag | Environment | Purpose |
| --- | --- | --- |
| `-server` | `SUPERCDN_URL` | Super CDN server URL. Saved by `login` when omitted later. |
| `-token` | `SUPERCDN_TOKEN` | Admin or user API token. Overrides the saved profile. |
| `-profile` | `SUPERCDN_PROFILE` | Local profile name. Use one profile per server/user. |
| `SUPERCDN_CONFIG` | `SUPERCDN_CONFIG` | Optional path for the local CLI profile file. |

Run `doctor` first after changing server, profile, token, storage config or DNS:

```powershell
go run .\cmd\supercdnctl -- doctor
```

## First Commands

| Goal | Command |
| --- | --- |
| Accept an invite and save a profile | `login -invite-token sci_xxx` |
| Check current identity | `whoami` |
| Produce a support-safe environment report | `doctor` |
| Create a reusable overseas CDN bucket | `create-cdn-bucket -slug overseas-assets -types image,archive` |
| Upload one bucket object | `upload-bucket -bucket overseas-assets -file .\poster.jpg -path images/v1/poster.jpg -warmup` |
| Publish an ordinary static site | `deploy-site -site demo -dir .\dist -target cloudflare_static -static-spa` |
| Publish a hybrid edge site | `deploy-site -site demo -dir .\dist -target hybrid_edge -profile china_mobile -static-spa` |
| Diagnose a bucket object | `cdn-doctor -bucket overseas-assets -path images/v1/poster.jpg` |
| Diagnose a site route | `site-doctor -site demo -path /assets/app.js` |
| Probe public Web delivery | `probe-site -site demo -spa-path /movie/123` |

## Team And Profiles

| Command | Use when |
| --- | --- |
| `login` | Accept an invite and store a local CLI profile token. |
| `logout` | Remove a saved local profile. |
| `whoami` | Confirm server, workspace, user id and role. |
| `doctor` | Package auth, database, storage, route profile and policy status. |
| `audit-log` | Review mutation audit events by workspace, action or resource. |
| `invite-user` | Create a one-time invite for an owner, maintainer or viewer. |
| `list-users` | List users in the current workspace. |
| `revoke-token` | Revoke a user API token. |

Typical flow:

```powershell
go run .\cmd\supercdnctl -- -token <root-token> invite-user -name alice -role maintainer
go run .\cmd\supercdnctl -- -server https://qwk.ccwu.cc -profile alice login -invite-token sci_xxx
go run .\cmd\supercdnctl -- -profile alice whoami
go run .\cmd\supercdnctl -- -profile alice doctor
```

## Static Object Projects

Use projects for simple `/o/{project}/{path}` objects. Use asset buckets when you need logical CDN buckets, warmup, purge, diagnostics and typed asset policy.

| Command | Use when |
| --- | --- |
| `create-project` | Create a simple static-object namespace. |
| `upload` | Upload one object into a project after preflight. |

Example:

```powershell
go run .\cmd\supercdnctl -- create-project -id assets
go run .\cmd\supercdnctl -- upload -project assets -file .\README.md -path docs/readme.txt -profile overseas
```

## Asset Buckets

Use buckets for reusable CDN assets served from `/a/{bucket}/{logical_path}`.

| Command | Use when |
| --- | --- |
| `create-bucket` | Create a bucket with an explicit route profile. |
| `create-cdn-bucket` | Create an overseas object CDN bucket, usually R2/Cloudflare-backed. |
| `create-domestic-cdn-bucket` | Create an AList/OpenList domestic CDN bucket. |
| `create-mobile-cdn-bucket` | Shortcut for the mobile domestic line. |
| `create-ipfs-bucket` | Create a durable IPFS/Pinata bucket. |
| `init-bucket` | Initialize bucket directory structure. |
| `upload-bucket` | Upload one object and return public/direct URLs. |
| `upload-bucket-dir` | Upload a local directory with dry-run, retries and a report file. |
| `list-bucket` | List bucket objects and public URL metadata. |
| `cdn-doctor` | Diagnose bucket state, object route, replicas and candidate URLs. |
| `purge-bucket` | Purge Cloudflare cache for selected bucket URLs. |
| `warmup-bucket` | Probe or warm selected bucket URLs. |
| `delete-bucket-object` | Delete one path, multiple paths, a prefix or all bucket objects. |
| `delete-bucket` | Delete the bucket metadata after remote cleanup. |

One-file upload:

```powershell
go run .\cmd\supercdnctl -- create-cdn-bucket -slug downloads -name downloads -types archive
go run .\cmd\supercdnctl -- upload-bucket -bucket downloads -file .\app.zip -path release/v1/app.zip -asset-type archive -warmup
go run .\cmd\supercdnctl -- cdn-doctor -bucket downloads -path release/v1/app.zip
```

Batch upload with an auditable report:

```powershell
go run .\cmd\supercdnctl -- upload-bucket-dir -bucket downloads -dir .\release -prefix release/v1 -dry-run -report-file .\upload-plan.json
go run .\cmd\supercdnctl -- upload-bucket-dir -bucket downloads -dir .\release -prefix release/v1 -skip-existing -retry 2 -report-file .\upload-report.json
```

Purge, warm and delete:

```powershell
go run .\cmd\supercdnctl -- purge-bucket -bucket downloads -prefix release/v1/ -dry-run
go run .\cmd\supercdnctl -- warmup-bucket -bucket downloads -path release/v1/app.zip -method GET
go run .\cmd\supercdnctl -- delete-bucket-object -bucket downloads -path release/v1/app.zip
```

## Sites And Deployments

Use sites for Web properties with domains, deployments, readiness probes and rollback/recovery evidence.

| Command | Use when |
| --- | --- |
| `create-site` | Create a site with route profile, target and domains. |
| `list-sites` | List sites visible to the current workspace. |
| `bind-domain` | Add or replace site domains. |
| `domain-status` | Check DNS, binding and certificate state. |
| `deploy-site` | Publish a new immutable deployment from a directory or zip. |
| `update-site` | Update an existing site using its current target/domains by default. |
| `inspect-site` | Inspect a local bundle before upload. |
| `probe-site` | Probe HTML, assets, MIME/CORS, redirects and SPA fallback. |
| `list-deployments` | List deployment history for a site. |
| `deployment` | Fetch one deployment record. |
| `promote-deployment` | Promote a compatible older deployment to production. |
| `delete-deployment` | Delete an inactive and unpinned deployment. |
| `offline-site` | Take a site offline without destroying deployments. |
| `online-site` | Restore an offline site. |
| `delete-site` | Destructively delete a site and tracked objects. |
| `purge-site` | Purge URLs planned from a site deployment manifest. |
| `gc-site` | Clean stale site content according to the site cleanup path. |

Ordinary Cloudflare-native static site:

```powershell
go run .\cmd\supercdnctl -- create-site -site blog -profile overseas -target cloudflare_static -domains blog.example.com
go run .\cmd\supercdnctl -- deploy-site -site blog -dir .\dist -target cloudflare_static -domains blog.example.com -static-spa
go run .\cmd\supercdnctl -- probe-site -site blog -spa-path /movie/123 -require-edge-static-html -require-immutable-assets
```

Hybrid edge site:

```powershell
go run .\cmd\supercdnctl -- create-site -site media -profile china_mobile -target hybrid_edge -domains media.example.com
go run .\cmd\supercdnctl -- deploy-site -site media -dir .\dist -target hybrid_edge -profile china_mobile -domains media.example.com -static-spa -resource-failover
go run .\cmd\supercdnctl -- probe-site -site media -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
```

Use `origin_assisted` only for local tests, integration and legacy compatibility:

```powershell
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist -target origin_assisted -profile overseas
```

## Edge Manifest And Hybrid Routing

| Command | Use when |
| --- | --- |
| `export-edge-manifest` | Export a deployment manifest without changing live traffic. |
| `publish-edge-manifest` | Publish a deployment manifest to Workers KV. |
| `refresh-edge-manifest` | Rebuild and republish the active manifest after locator or health changes. |
| `route-explain` | Explain one site path's route, candidates and selection reason. |
| `site-doctor` | Package site, deployment, route and expected edge header diagnostics. |
| `switch-plan` | Generate a read-only manual line switch plan for a bucket object or site file. |
| `switch-apply` | Apply a confirmed primary-target switch for one supported object/path. |

Useful sequence:

```powershell
go run .\cmd\supercdnctl -- route-explain -site media -path /assets/app.js -country CN -client-ip 203.0.113.10
go run .\cmd\supercdnctl -- switch-plan -site media -path /assets/app.js -country CN
go run .\cmd\supercdnctl -- switch-apply -site media -path /assets/app.js -target repo_backup -dry-run=false -confirm switch
```

Important boundaries:

- `switch-plan` is read-only.
- `switch-apply` changes one object/file primary target. It does not edit route profiles, routing policies, Worker code or KV manifests.
- Paths controlled by `routing_policy` or `resource_failover` should be diagnosed with `route-explain`, `routing-policy-status` and `refresh-edge-manifest`, not forced with `switch-apply`.
- Static-resource failover requires ready replicas. It does not fall back to the Go origin.

## Rollback And Recovery

| Command | Use when |
| --- | --- |
| `rollback-plan` | Generate a safe rollback plan for one deployment. |
| `rollback-apply` | Execute a confirmed rollback plan. Dry-run by default. |
| `reconcile-deployment` | Compare Super CDN deployment metadata with provider reality after readiness timeouts. |
| `recover-cloudflare-static` | Validate unrecorded Cloudflare Static provider writes. |
| `recover-hybrid-edge` | Validate or write back hybrid edge evidence after provider success but missing metadata. |
| `activate-cloudflare-static` | Activate a recovered Cloudflare Static deployment after evidence checks. |

Rollback examples:

```powershell
go run .\cmd\supercdnctl -- rollback-plan -site blog -deployment dpl-old
go run .\cmd\supercdnctl -- rollback-apply -site blog -deployment dpl-old -dir .\dist-rollback -dry-run=false -confirm rollback
```

Cloudflare Static and `hybrid_edge` rollback are provider-aware. Do not use metadata-only promote when Worker assets or KV manifest state must be republished.

## Resource Libraries And Routing Policies

| Command | Use when |
| --- | --- |
| `init-libraries` | Initialize resource-library directory layout and markers. |
| `init-job` | Check a resource-library initialization job. |
| `resource-status` | Check configured library state and cached health. |
| `routing-policy-status` | Check smart-routing policy definition and source readiness. |
| `health-check` | Run passive or explicit resource-library health checks. |
| `e2e-probe` | Upload, read and cleanup through a real route profile primary. |

Examples:

```powershell
go run .\cmd\supercdnctl -- init-libraries -dry-run
go run .\cmd\supercdnctl -- init-libraries
go run .\cmd\supercdnctl -- init-job -id 1
go run .\cmd\supercdnctl -- resource-status -library repo_china_all
go run .\cmd\supercdnctl -- routing-policy-status -policy global_smart
go run .\cmd\supercdnctl -- health-check -libraries repo_china_all
go run .\cmd\supercdnctl -- e2e-probe -profile china_all
```

## Jobs And Replicas

| Command | Use when |
| --- | --- |
| `job` | Check one async job. |
| `replicas` | Inspect replicas for an object id. |
| `refresh-replicas` | Recheck remote visibility, signed locators and IPFS metadata. |
| `repair-replicas` | Requeue missing, failed or selected replicas. |
| `gc` | Clean stale local staging files. Dry-run by default. |

Examples:

```powershell
go run .\cmd\supercdnctl -- job -id 1
go run .\cmd\supercdnctl -- replicas -object-id 1
go run .\cmd\supercdnctl -- refresh-replicas -bucket downloads -prefix release/v1/
go run .\cmd\supercdnctl -- repair-replicas -object-id 1 -target repo_backup
go run .\cmd\supercdnctl -- gc -dry-run -older-than 1h
```

## IPFS

| Command | Use when |
| --- | --- |
| `ipfs-status` | Check Pinata/IPFS token and gateway readiness without uploading. |
| `ipfs-smoke` | Upload a test asset, refresh metadata and probe gateway reads. |
| `ipfs-web-smoke` | Exercise the IPFS-backed Web deployment path. |
| `refresh-ipfs-pins` | Refresh known Pinata/IPFS pin state for an object. |

Examples:

```powershell
go run .\cmd\supercdnctl -- ipfs-status
go run .\cmd\supercdnctl -- ipfs-smoke -file .\poster.jpg -download-runs 3
go run .\cmd\supercdnctl -- ipfs-web-smoke -file .\poster.jpg -cleanup
go run .\cmd\supercdnctl -- refresh-ipfs-pins -object-id 123
```

## Cloudflare Operations

| Command | Use when |
| --- | --- |
| `cloudflare-status` | Read Cloudflare account, zone, DNS, Worker and R2 status. |
| `publish-cloudflare-static` | Low-level Cloudflare Workers Static Assets publish without recording a Super CDN deployment. |
| `sync-site-dns` | Create or verify managed site DNS records. |
| `sync-worker-routes` | Create or verify Worker route patterns. |
| `purge-site` | Purge exact site URLs from deployment manifest planning. |
| `sync-cloudflare-r2` | Sync R2 CORS and public domain configuration. |
| `provision-cloudflare-r2` | Plan or create an R2 bucket and attach CORS/domain settings. |
| `create-r2-credentials` | Create R2 S3 credentials and optionally write local config. |
| `set-r2-credentials` | Import existing R2 S3 credentials into local config. |
| `purge` | Purge explicit Cloudflare URLs. |

Examples:

```powershell
go run .\cmd\supercdnctl -- cloudflare-status -all
go run .\cmd\supercdnctl -- sync-site-dns -site blog -dry-run
go run .\cmd\supercdnctl -- sync-worker-routes -site blog -dry-run
go run .\cmd\supercdnctl -- purge-site -site blog -dry-run
go run .\cmd\supercdnctl -- provision-cloudflare-r2 -cloudflare-library overseas_accel -dry-run
go run .\cmd\supercdnctl -- sync-cloudflare-r2 -cloudflare-library overseas_accel
go run .\cmd\supercdnctl -- purge -urls https://example.com/a.css
```

## Safety Rules For Operators

- Prefer `doctor`, `cdn-doctor`, `site-doctor`, `probe-site` and `route-explain` before changing state.
- Keep `cloudflare_static` as the default ordinary Web-hosting path.
- Use `hybrid_edge` when entry HTML should stay on Cloudflare and non-entry resources should route through resource libraries.
- Treat `origin_assisted` as testing or compatibility, not the preferred production runtime.
- Keep static-resource failover explicit with `-resource-failover`; it requires multiple ready resource sources.
- Do not rely on Cloudflare Static metadata-only promotion. Roll back by provider-aware redeploy or recovery commands.
- Run destructive operations with their default dry-run first when the command supports it.
