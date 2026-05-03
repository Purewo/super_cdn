# Super CDN

Windows-first MVP for static asset acceleration and static site hosting.

## Product target

Super CDN is a website hosting and CDN orchestration platform. It should use Cloudflare's native static-site surfaces where they already fit, instead of rebuilding that layer from R2 and KV alone.

The target architecture is:

- Website hosting has two supported shapes: Go entry delivery for tests/integration, and the preferred Cloudflare entry delivery for production Web hosting.
- Preferred Web hosting keeps homepage/SPAs on Workers Static Assets or Pages, then routes non-entry resources to AList/OpenList, Cloudflare-native static assets, or IPFS/Pinata.
- Overseas object acceleration: Cloudflare R2 for large objects such as video, images, archives and other reusable downloads, plus account-isolated overseas acceleration nodes.
- Domestic acceleration: AList/OpenList-backed resource libraries for China-facing static resources.
- R2-backed website delivery is legacy compatibility only. Keep it available for old deployments and diagnostics, but do not expand it as the mainstream website path.
- Edge routing: a Worker reads a KV-backed edge manifest and chooses whether a path should stay on Cloudflare-native hosting, redirect/proxy to AList/OpenList, or fetch IPFS gateway routes. R2 stays in object/CDN paths or legacy site routes.
- Future global acceleration: routing policy can choose ready resource libraries by site, path, asset class, health, region and availability. Smart routing and failover require at least two ready sources and are explicit opt-in.

The Go service remains the control plane: deploy intake, inspection, health checks, manifest generation, Cloudflare automation, storage synchronization and rollback. Public website delivery should move away from depending on the Go origin at runtime.

## What is included

- Go origin/control service with REST API.
- `supercdnctl` CLI for asset upload and `dist` directory deployment.
- SQLite state for projects, objects, replicas, jobs, sites and domains.
- Storage adapters for local disk, Cloudflare R2, AList and Pinata/IPFS.
- TypeScript Cloudflare Worker for same-origin storage fetch, edge cache and compatibility origin fallback.

Full CLI reference: [docs/cli-reference.md](docs/cli-reference.md).

v0.1.x maintenance scope and runbooks: [docs/v0.1-maintenance.md](docs/v0.1-maintenance.md).

Next feature roadmap: [docs/v0.2-roadmap.md](docs/v0.2-roadmap.md).

Web hosting product boundary: [docs/web-hosting-boundaries.md](docs/web-hosting-boundaries.md).

Development handoff and next goals: [docs/tomorrow-plan.md](docs/tomorrow-plan.md).

## Current verified live site

The latest preferred Web-hosting smoke test is CyberStream on hybrid IPFS:

```text
URL: https://cyberstream-ipfs-0501.qwk.ccwu.cc/
deployment: dpl-di6slq3zv3ja
route_profile: ipfs_archive
delivery: entry HTML and SPA fallback on Cloudflare Static, JS through Worker/KV to Pinata/IPFS gateway
probe: root and /movie/123 returned X-SuperCDN-Edge-Source: cloudflare_static; JS returned X-SuperCDN-Edge-Source: ipfs_gateway and X-SuperCDN-Edge-Manifest: route
```

The previous complex-frontend R2 compatibility smoke test is still useful as a legacy reference:

```text
URL: https://cyberstream.sites.qwk.ccwu.cc/?v=dpl-di49qyrhf5y0
deployment: dpl-di49qyrhf5y0
route_profile: overseas_r2
delivery: root index.html on origin, JS/CSS by 302 to Cloudflare R2
```

Detailed handoff notes and next tasks are in [docs/tomorrow-plan.md](docs/tomorrow-plan.md).

## Run locally

```powershell
.\scripts\start-dev.ps1 -Config .\configs\config.local.json
```

In another shell:

```powershell
$env:SUPERCDN_TOKEN = "change-me"
go run .\cmd\supercdnctl -- create-project -id assets
go run .\cmd\supercdnctl -- upload -project assets -file .\README.md -path /docs/readme.txt -profile overseas
```

Open `http://127.0.0.1:8080/o/assets/docs/readme.txt`.

## Team CLI use

The server admin token remains the root/break-glass credential. For team use, create an invite with root or an owner token, then let each user log in to a local CLI profile:

```powershell
go run .\cmd\supercdnctl -- -token <root-token> invite-user -name alice -role maintainer
go run .\cmd\supercdnctl -- -server https://qwk.ccwu.cc -profile alice login -invite-token sci_xxx
go run .\cmd\supercdnctl -- -profile alice whoami
```

User tokens are stored in the local `supercdn/cli.json` profile and are scoped to a workspace. Owners can manage invites and tokens; maintainers can create and deploy sites/buckets; viewers are read-only. Cloudflare/R2/AList configuration commands stay root-only.

## Foundation check

Before architecture changes, run the local baseline:

```powershell
.\scripts\foundation-check.ps1
```

This checks Go formatting, tests, vet, Windows/Linux builds, Worker tests/typecheck, and a local service `/healthz` startup probe when `configs/config.local.json` exists.

Run the full operational probe only when real Cloudflare/R2 credentials should be exercised:

```powershell
.\scripts\foundation-check.ps1 -Full -LiveSiteUrl "https://cyberstream.sites.qwk.ccwu.cc/?v=dpl-di49qyrhf5y0" -SpaPath /movie/123
```

`-Full` runs Cloudflare status checks plus `overseas_accel` R2 write/read/delete health probes and an `overseas_r2` API e2e probe.

## Deploy a static site

```powershell
$env:SUPERCDN_TOKEN = "change-me"
go run .\cmd\supercdnctl -- create-site -site demo -profile overseas -domains demo.local
go run .\cmd\supercdnctl -- list-sites
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist
go run .\cmd\supercdnctl -- update-site -site demo -dir .\dist
```

For local testing without DNS, use `http://127.0.0.1:8080/s/demo/`.

Static site deploys use immutable deployments. The CLI uploads a zip artifact, the server unpacks it locally, saves the original zip, writes a manifest, and uploads site files with the original directory layout under `sites/{site}/deployments/{deployment}/root/...`. Preview deployments are available at `/p/{site}/{deployment}/`; production is an atomic promote to the active deployment and can be rolled back by promoting an older deployment.

Use `update-site` for long-lived projects where the production domain must stay stable. It is the maintenance-facing wrapper around the deployment pipeline: the site must already exist, and omitted `-target` / `-domains` values are resolved from the current site record. The command replaces the active production deployment, Worker assets and/or hybrid edge manifest while keeping the same bound domain. Use `deploy-site` or `create-site` for first publish, then `update-site` for later code changes.

Site lifecycle is split between offline and destructive delete. `offline-site -site <id>` only marks the site offline and blocks production/public serving with `410 Gone`; deployments, previews and resource objects stay available for maintenance and can be restored with `online-site -site <id>`. `delete-site -site <id> -force` is the destructive path: it cleans tracked deployment files, artifacts, manifests and related metadata before removing the site record. Cloudflare Worker versions, custom domains and KV entries are outside this cleanup path and must be handled separately when used.

For `cloudflare_static`, promotion is intentionally stricter: the normal `promote-deployment` endpoint will not metadata-promote an older Cloudflare Static record, because the real Worker assets and the Super CDN active row could diverge. Rollback for Cloudflare Static should be done by redeploying the desired assets or by a dedicated Cloudflare Worker rollback flow. Deleting a Cloudflare Static deployment currently deletes Super CDN metadata only; it does not delete Worker versions or custom domains. For origin-assisted deployments, `delete-deployment -delete-objects -delete-remote=true` removes tracked site files, artifacts and manifests through the same remote-replica cleanup path used by asset buckets.

Sites and deployments carry a separate `deployment_target` so website hosting strategy is not overloaded onto `route_profile`. Supported target values are:

- `cloudflare_static`: Cloudflare-native website hosting, backed by Workers Static Assets or Pages. This is the intended default for ordinary overseas static sites.
- `hybrid_edge`: Cloudflare-hosted entry HTML plus Worker/KV routing to AList/OpenList, Cloudflare-native static assets, or IPFS/Pinata resource libraries. R2-backed website resource routes are legacy compatibility, not the preferred Web hosting path.
- `origin_assisted`: Go-origin serving with storage redirects for tests, integration and compatibility.

The Web hosting boundary is intentionally stricter than generic object delivery. In preferred Web hosting, static-resource failover never falls back to the Go server; it can only select another ready resource library that already has the object. Homepage fallback to Go is a last-resort, explicit operator request after every homepage hosting method has failed, and must carry a migration warning.

Static-resource failover is explicit. Pass `-resource-failover` on `deploy-site` only when the selected route profile has a primary target plus at least one backup target and the deployment should allow resource-library fallback. The edge manifest then emits `failover` routes with ordered candidates, and the Worker proxies those candidates in order until one succeeds. If all candidates fail, the request returns an edge error; it does not ask the Go origin to stream the asset.

Homepage fallback is a separate explicit compatibility switch. Pass `-entry-origin-fallback` only when a `hybrid_edge` site must temporarily serve entry HTML/SPAs from the Go origin after Cloudflare entry delivery fails. The Worker adds warning headers and `Cache-Control: no-store` on those responses. This switch does not apply to JS/CSS/images or other static resources.

`route_profiles[].deployment_target` can define the default target for new sites and deployments; `create-site -target` and `deploy-site -target` can override it explicitly. When `deploy-site` omits `-target`, the CLI asks the control plane for the site/profile default. If that resolves to `cloudflare_static` and `-domains` is empty, the control plane suggests a one-level subdomain under `cloudflare.root_domain`, for example `demo.qwk.ccwu.cc`; the `cloudflare.site_domain_suffix` default remains reserved for Go-origin site domains such as `demo.sites.qwk.ccwu.cc`.

For `cloudflare_static`, `deploy-site` uses the local Wrangler Workers Static Assets publisher and then records the deployment metadata in the Super CDN control plane. This path requires `-dir`, custom domains are passed with `-domains`, and R2 is not involved:

```powershell
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist -target cloudflare_static -domains demo-static-test.example.com -static-name supercdn-demo-static
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist -target hybrid_edge -profile china_mobile -domains demo.qwk.ccwu.cc -static-spa
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist -target hybrid_edge -profile china_mobile -domains demo.qwk.ccwu.cc -resource-failover
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist -target hybrid_edge -profile china_mobile -domains demo.qwk.ccwu.cc -entry-origin-fallback
go run .\cmd\supercdnctl -- deploy-site -site demo -dir .\dist -profile overseas
```

By default, Super CDN uses `-static-cache-policy auto` for Cloudflare Static deploys. If the source already contains `_headers`, it is respected. Otherwise the CLI publishes from a temporary copy with a generated `_headers` file: HTML and service-worker files stay revalidating, while versioned or common build assets get long immutable browser caching. The source directory is not modified. Use `-static-cache-policy none` to disable this, or `force` to replace an existing `_headers` during publish.

For SPAs, pass `-static-spa`. The CLI generates a temporary `wrangler.toml` with `assets.not_found_handling = "single-page-application"`, so deep links such as `/movie/123` return `index.html` directly from Cloudflare Static Assets. Use `-static-not-found-handling 404-page|single-page-application|none` when you need the explicit Cloudflare mode.

After a Cloudflare Static publish, `deploy-site` now runs a readiness probe by default before writing the active Super CDN deployment record. The probe verifies each custom domain over HTTPS, checks that the root returns HTML, verifies JS/CSS MIME types, requires direct same-site assets, checks generated cache headers, and validates SPA fallback when `-static-spa` is enabled. The readiness probe uses `1.1.1.1:53` by default so local DNS cache does not mistake an old wildcard origin record for the new Cloudflare custom domain. Use `-static-verify warn` to record the deployment even if readiness is not yet passing, or `-static-verify none` for low-level diagnostics.

For true hybrid no-origin website delivery, use `-target hybrid_edge`. The CLI uploads the deployment to the selected Super CDN route profile, waits until it is ready, publishes the active edge manifest to Workers KV, deploys the shared Worker with Cloudflare Static Assets (`ASSETS`, `run_worker_first = true`), and runs the same readiness probe without requiring direct same-site assets. Entry HTML and SPA fallback are served by Cloudflare Static Assets; manifest-backed resources redirect directly to the selected storage line unless `-resource-failover` is enabled, in which case resource requests are proxied through ordered manifest candidates. If an HTTPS page would load an HTTP-only AList/OpenList manifest URL, the Worker proxies that resource same-origin to avoid browser mixed-content blocking while keeping delivery on storage instead of the Go origin. When `-routing-policy` or `-resource-failover` is enabled, `hybrid_edge` waits for at least two ready non-entry resource candidates before publishing the active KV manifest, so async backup replication cannot silently put a single-source route live. Active manifest refresh can later skip a recently failed resource-library target and degrade to the remaining healthy candidate with a warning; that is a recovery state, not a green-light state for new smart-routing deployments. `-entry-origin-fallback` is an explicit temporary homepage/SPAs compatibility switch only; it does not affect static resources. Preferred Web storage lines are AList/OpenList, Cloudflare-native static assets, or IPFS/Pinata; R2-backed website resources are legacy compatibility. Use `-edge-candidate-timeout`, `-edge-kv-namespace`, `-edge-kv-namespace-id`, `-edge-name`, and `-edge-manifest-mode route|enforce` for explicit edge routing control. Hybrid verification also checks `X-SuperCDN-Edge-Source: cloudflare_static` on HTML/SPAs and `X-SuperCDN-Edge-Manifest: route` on asset first hops.

The lower-level canary command is still available when you only want to publish to Cloudflare without recording a Super CDN deployment:

```powershell
go run .\cmd\supercdnctl -- publish-cloudflare-static -site demo -dir .\dist -domains demo-static-test.example.com -dry-run
go run .\cmd\supercdnctl -- publish-cloudflare-static -site demo -dir .\dist -domains demo-static-test.example.com -dry-run=false
```

This command uses Workers Static Assets and reads Cloudflare credentials from `configs/private/cloudflare.env` or the process environment. It is intentionally separate from R2; use it for ordinary overseas static sites.

Deployment responses include direct access URLs when the site has bound domains:

- `production_url` / `production_urls` for the active production deployment.
- `preview_url` for the immutable deployment preview route.
- `site_domains` for the domains currently bound to the site.
- `deployment_target` for the intended website hosting target.
- `inspect` for non-blocking bundle warnings such as module scripts, dynamic chunks, CSS relative assets, fonts, wasm and service workers.
- `delivery_summary` for how many files are planned as origin or redirect delivery.

For `origin_assisted` hosted sites, the Go origin serves the root `index.html` directly. Other successful site file requests return `302 Found` to the freshest direct storage URL when one is available, so Cloudflare carries the cache/fetch traffic instead of forcing the Go origin to stream every asset. Range requests, 404 responses, and SPA fallbacks that resolve to root `index.html` stay on the origin. Generic `/o/...` asset redirects still follow the route profile `allow_redirect` policy. This target is a test/compatibility path, not the preferred production Web runtime and not a static-resource failover target for `hybrid_edge`.

Export an edge manifest for a ready deployment when preparing the zero-origin edge path. This is a read-only sidecar export; it does not change the current production delivery path.

```powershell
go run .\cmd\supercdnctl -- export-edge-manifest -site demo -deployment dpl-abc -out .\edge-manifest.json
go run .\cmd\supercdnctl -- publish-edge-manifest -site demo -deployment dpl-abc -kv-namespace supercdn-edge-manifest -dry-run
go run .\cmd\supercdnctl -- refresh-edge-manifest -site demo -kv-namespace supercdn-edge-manifest -spa-path /movie/123
```

`refresh-edge-manifest` reloads the active deployment manifest from the control plane, republishes the active/deployment KV keys, and then probes the hybrid edge path by default. This is the quick recovery path for stale AList/OpenList signed route locations without rebuilding or redeploying the website package.

Run a local-only bundle inspection before uploading:

```powershell
go run .\cmd\supercdnctl -- inspect-site -dir .\dist
```

Run a live delivery probe after deployment. `probe-site -site` resolves the active production URL from the control API; `probe-site -url` checks any public URL without needing an admin token. The probe fetches the root HTML, follows same-site JS/CSS references through any storage redirects, checks final MIME/CORS headers, and can verify one SPA fallback route:

```powershell
go run .\cmd\supercdnctl -- probe-site -site demo -spa-path /movie/123
go run .\cmd\supercdnctl -- probe-site -url https://demo.sites.qwk.ccwu.cc/ -max-assets 20
go run .\cmd\supercdnctl -- probe-site -url https://demo.qwk.ccwu.cc/ -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
```

Probe and readiness JSON redact query values for signed storage URLs by default so AList/OpenList and cloud storage signatures do not leak into logs. Use `-redact-urls=false` only for local low-level debugging.

Use `supercdn.site.json` to keep risky files on the origin while leaving the default non-index redirect behavior in place:

```json
{
  "delivery": [
    {"path": "/assets/*", "mode": "origin"},
    {"path": "/sw.js", "mode": "origin"}
  ]
}
```

### Site domains

When `cloudflare.root_domain` and `cloudflare.site_domain_suffix` are configured, creating a site automatically binds a managed default domain:

```text
{site_id}.sites.qwk.ccwu.cc
```

You can choose a different default-domain id or append a random suffix:

```powershell
go run .\cmd\supercdnctl -- create-site -site demo -profile china_all -domain-id docs
go run .\cmd\supercdnctl -- bind-domain -site demo -domains docs.qwk.ccwu.cc
go run .\cmd\supercdnctl -- domain-status -domain demo.sites.qwk.ccwu.cc
go run .\cmd\supercdnctl -- sync-site-dns -site demo -dry-run
go run .\cmd\supercdnctl -- sync-site-dns -site demo
go run .\cmd\supercdnctl -- sync-worker-routes -site demo -dry-run
go run .\cmd\supercdnctl -- sync-worker-routes -site demo
```

`sync-site-dns` creates or verifies DNS records for the site's bound domains. By default it creates proxied records, infers `CNAME` for domain targets and `A`/`AAAA` for IP targets, and uses `cloudflare.site_dns_target` as the destination. Pass `-force` to update an existing same-type record with different content or proxy status.

`sync-worker-routes` creates or verifies Cloudflare Worker route patterns such as `demo.sites.qwk.ccwu.cc/*` for the site's bound domains. It will not overwrite a route that already points at a different Worker unless `-force` is passed. Worker routes only run for proxied Cloudflare DNS records; DNS-only records bypass the Worker.

Multiple Cloudflare accounts are modeled like mount points:

- `cloudflare_accounts[]` defines each account/zone/token/root-domain pair.
- `cloudflare_accounts[].r2` attaches the account's R2 bucket as its storage mount.
- `cloudflare_libraries[]` groups one or more Cloudflare accounts into a logical acceleration library such as `overseas_accel`.
- `cloudflare_libraries[].bindings[].path` prefixes object keys inside that account's bucket, similar to one mount-point path per binding.
- Accounts without `r2` stay available for DNS, Worker routes and purge, but are ignored when building storage-backed Cloudflare libraries.
- The legacy single `cloudflare` block is still supported and becomes the default account/library when no multi-account blocks are configured.

Use `-cloudflare-account` to force one account, or `-cloudflare-library` to select a logical library and let the server match the bound domain to the right account:

```powershell
go run .\cmd\supercdnctl -- cloudflare-status -all
go run .\cmd\supercdnctl -- sync-site-dns -site demo -cloudflare-library overseas_accel -dry-run
go run .\cmd\supercdnctl -- sync-worker-routes -site demo -cloudflare-account cf_business_main -dry-run
```

After promoting a deployment, purge the exact site URLs generated from the deployment manifest:

```powershell
go run .\cmd\supercdnctl -- purge-site -site demo -dry-run
go run .\cmd\supercdnctl -- purge-site -site demo
go run .\cmd\supercdnctl -- purge-site -site demo -deployment dpl-abc -dry-run
```

The purge planner expands `index.html` to both `/` and `/index.html`, expands nested `*/index.html` to the directory URL, deduplicates URLs, and sends Cloudflare purge requests in batches of 100.

## Configure real backends

Use `configs/config.full.example.json` as a template and fill environment variables for:

- `CF_ACCOUNT_ID`, `R2_BUCKET`, `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, optional `CF_R2_API_TOKEN` when R2 control-plane permissions use a separate token
- `ALIST_TOKEN`, optional `ALIST_USERNAME` / `ALIST_PASSWORD` for automatic token refresh
- `PINATA_JWT`
- `CF_ZONE_ID`, `CF_API_TOKEN`
- `SUPERCDN_ADMIN_TOKEN`

Route profiles decide the primary storage and backup replicas. Backups are asynchronous jobs.

IPFS/Pinata readiness can be checked without uploading data:

```powershell
go run .\cmd\supercdnctl -- ipfs-status
go run .\cmd\supercdnctl -- ipfs-status -target ipfs_pinata
```

The status probe verifies the configured Pinata JWT through the v3 files API and checks that the configured gateway URL is reachable. Uploads use `storage[].pinata.upload_base_url` (default `https://uploads.pinata.cloud`) while list/delete/group operations use `storage[].pinata.api_base_url` (default `https://api.pinata.cloud`). Responses include provider, target, token and gateway status, but never echo the JWT.

When a route profile writes to a Pinata/IPFS target, Super CDN records the returned CID and Pinata v3 file ID under the object replica metadata. Upload responses, `replicas`, and asset bucket listings include an `ipfs` field with the CID, provider, pin status and gateway URL when available. Deletes with `delete_remote=true` delete the Pinata file through the replica locator; when another local object still references the same CID on the same target, the remote file is kept and the delete result reports `kept_shared`.

Asset bucket uploads to Pinata are also attached to a Pinata v3 public file group for console-side management. The group name is `supercdn-bucket-<bucket-slug>` by default; change `storage[].pinata.group_prefix` if you want a different prefix. Groups do not change the CID, gateway URL or delete semantics.

Refresh known pin status with:

```powershell
go run .\cmd\supercdnctl -- refresh-ipfs-pins -object-id 123
go run .\cmd\supercdnctl -- refresh-ipfs-pins -object-id 123 -target ipfs_pinata
```

Run a live IPFS smoke after `ipfs_pinata` is configured:

```powershell
go run .\cmd\supercdnctl -- ipfs-smoke -file .\poster.jpg -proxy-url http://127.0.0.1:10808 -download-runs 3
go run .\cmd\supercdnctl -- ipfs-smoke -file .\poster.jpg -cleanup
go run .\cmd\supercdnctl -- ipfs-web-smoke -file .\poster.jpg -proxy-url http://127.0.0.1:10808
```

`ipfs-smoke` creates an IPFS bucket by default, uploads the file, returns the CID and gateway URL, refreshes the pin status, and probes HEAD, Range GET and full GET against the gateway. It keeps the uploaded object unless `-cleanup` is passed.

`ipfs-web-smoke` exercises the website path: it creates a preview site deployment on an IPFS route profile, exports the edge manifest, verifies that the asset route is `ipfs`, checks the first Super CDN site hop, and probes the gateway URL. With `-cleanup`, it now deletes the preview deployment plus tracked remote Pinata v3 files. Run this before promoting IPFS into smart routing policies.

### Proxy support

Every network storage backend can use a dedicated proxy:

- R2: `storage[].r2.proxy_url` or `cloudflare_accounts[].r2.proxy_url`
- AList direct storage or mount point: `storage[].alist.proxy_url` / `mount_points[].alist.proxy_url`
- Pinata/IPFS: `storage[].pinata.proxy_url`

Supported values are standard Go HTTP proxy URLs, for example:

```json
"proxy_url": "http://127.0.0.1:10808"
```

Leave `proxy_url` empty to use no proxy. The service intentionally ignores `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`; network paths must be explicit. Current local convention is: R2 and Pinata/IPFS use `http://127.0.0.1:10808` when needed, while domestic AList/OpenList mount points use no proxy.

AList/OpenList endpoints can also set `network` to `tcp4` or `tcp6` when a host resolves to an unusable address family. Leave it empty for normal Go dual-stack behavior. Production AList nodes that publish unreachable IPv6 records should use `"network": "tcp4"`.

### Mount points and resource libraries

A mount point is a physical AList service entry. It only describes how to reach an AList instance and does not directly represent a CDN line.

A resource library is the logical storage target used by route profiles. It binds one path under one mount point per binding, for example `/supercdn/china_telecom` under `alist_main`. A library may contain multiple bindings over time, including paths from different AList mount points, but each binding is one explicit path. Uploads write to one binding only, currently the first binding in config, so the program never does accidental batch writes across many cloud drive paths.

Route profiles can reference normal storage targets such as `r2_global` or logical resource libraries such as `repo_china_telecom`.

Reads are failover-capable when more than one ready source exists. Object delivery tries ready replicas in order, and resource-library reads can fall back to another binding when the binding encoded in an older locator is unavailable. This only protects content that actually exists on the alternate binding or has a ready backup replica; configure `route_profiles[].backups` and backfill old objects when a storage line must survive a full provider outage.

Routing policies are explicit opt-in objects under `routing_policies[]`. A site, deployment, or asset bucket can set `routing_policy` only when its route profile includes every policy source as the primary target or a backup. Policies require at least two sources and support `load_balance`, `global_accel`, and `global_load_balance`; the Worker and bucket read path only use ready replicas, so multi-source copies must exist before smart routing can take effect. Resource-library candidates with recent failed health are skipped during edge manifest generation. Check configured policies with `supercdnctl routing-policy-status`.

For `hybrid_edge`, smart-routing and resource-failover deployments wait for resource candidates before KV publication. Resource-library writes also verify a post-upload direct locator, so signed AList/OpenList paths are not marked ready until the object can be resolved.

For Web hosting, failover is separate from ordinary read retry behavior. It is off unless the operator explicitly enables it. Static-resource failover can only move between ready resource-library replicas and must not stream from the Go origin. Homepage fallback to Go is a last-resort compatibility action after all Cloudflare entry options fail, and the operator should be warned to migrate back quickly.

Cloudflare libraries behave like resource libraries backed by per-account R2 stores. A route profile can point directly at a Cloudflare library such as `overseas_accel`, and uploads will land in the first storage-capable binding. Reads, public URLs, health checks and directory initialization work through the library binding path inside each account bucket.

For the overseas acceleration line, each R2 account is treated as an independent acceleration node. Super CDN does not shard objects across R2 accounts by default; Cloudflare/R2 is expected to carry the performance load without striping. Extra accounts are for isolation, redundancy, migration, and future routing policy, not for performance sharding.

The long-term website delivery direction is Cloudflare-native entry hosting plus Super CDN routing policy. Workers Static Assets should be the default overseas website host when the site fits native limits, with Cloudflare Pages kept as a supported alternative. R2 should not be the default website deployment surface for overseas-only static sites; keep it as the overseas object/CDN line for large files, media, archives and reusable downloads. Worker/KV edge manifests should route non-entry resources to the best storage line, including AList/OpenList and IPFS/Pinata. The current Go-origin HTML plus Go-origin 302 model is a test/compatibility stage.

Resource-library policy lives under `resource_libraries[].policy`. It describes the logical library as a whole: `max_bindings`, `total_capacity_bytes`, `available_bytes`, `reserve_bytes`, and `notes`. If `total_capacity_bytes` is omitted and all binding capacities are known, preflight derives a binding capacity sum. If `available_bytes` is unknown, preflight reports capacity metadata but does not enforce remaining capacity.

Binding-level constraints live under each `resource_libraries[].bindings[].constraints`, not on the resource library. Supported fields are `max_capacity_bytes`, `peak_bandwidth_mbps`, `max_batch_files`, `max_file_size_bytes`, `daily_upload_limit_bytes`, `daily_upload_limit_unlimited`, `supports_online_extract`, `max_online_extract_bytes`, and `notes`. The current service enforces file size, batch file count, runtime daily upload totals, configured resource-library capacity and configured available capacity. Peak speed and online extraction support are stored as capability metadata until those subsystems are added.

The API also exposes preflight checks:

- `POST /api/v1/preflight/upload`
- `POST /api/v1/preflight/site-deploy`

`supercdnctl upload` and `supercdnctl deploy-site` call these before sending file bytes. Preflight validates the selected route profile, primary target, total upload size, largest file size, file count, global upload limit and binding constraints so obvious failures are caught before a long transfer starts.

### Overclock mode

`limits.overclock_mode` is a risk switch for cases where configured limits are more conservative than the remote drive's current policy. Keep it `false` by default. When set to `true`, the service ignores configured upload-size, file-count, resource-library capacity/file-size/batch/daily-upload, resource-health, asset-bucket capacity/file-size/type and transfer-slot limits.

Responses include `overclock_mode: true` and a warning. This mode can produce unpredictable or catastrophic results if the upstream storage rejects traffic later, throttles hard, deletes sessions, or accepts more work than the server can safely handle.

Resource-library initialization creates the standard directory layout for every selected binding path and writes `_supercdn/init.json` as an idempotent marker. The default layout is:

- `_supercdn/manifests`, `_supercdn/locks`, `_supercdn/jobs`
- `assets/objects`, `assets/manifests`, `assets/tmp`
- `sites/artifacts`, `sites/bundles`, `sites/deployments`, `sites/releases`, `sites/manifests`, `sites/tmp`

Use dry-run first:

```powershell
go run .\cmd\supercdnctl -- init-libraries -dry-run
```

Then enqueue the actual initialization job:

```powershell
go run .\cmd\supercdnctl -- init-libraries
go run .\cmd\supercdnctl -- init-job -id 1
```

All transfer-style work, including foreground uploads, replica jobs and initialization jobs, is limited by `limits.max_active_transfers`. The default is `5`; extra operations wait in process or remain queued as jobs. `limits.overclock_mode=true` bypasses this transfer slot guard.

Resource-library health data is stored locally in SQLite, not in cloud drive paths. By default health checks are passive and only verify that each binding root can be listed:

```powershell
go run .\cmd\supercdnctl -- health-check -libraries repo_china_all
go run .\cmd\supercdnctl -- resource-status -library repo_china_all
```

The server applies `limits.resource_health_min_interval_seconds` as a local cooldown; repeated checks inside the window return cached local status instead of hitting AList/OpenList again. A write/read/delete probe is available only when explicitly requested with `-write-probe`.

For a full primary-path smoke test, use the e2e probe. It uploads a tiny text file through the selected route profile primary, records it as a normal object, reads it back through `/o/...`, verifies the payload, then deletes the remote probe file and local object/project records:

```powershell
go run .\cmd\supercdnctl -- e2e-probe -profile china_all
```

The probe intentionally skips backup replicas so a domestic resource-library test does not touch R2 or IPFS.

Cloudflare status is available as a read-only diagnostic before making edge changes:

```powershell
go run .\cmd\supercdnctl -- cloudflare-status
go run .\cmd\supercdnctl -- cloudflare-status -all
```

It checks token validity, zone metadata, managed DNS records, Worker routes and R2 status. When `cloudflare_accounts[].r2` is configured, the R2 section also verifies that the configured bucket exists in that account, reads the bucket CORS policy, and checks whether `public_base_url` matches an attached R2 custom domain or enabled r2.dev managed domain.

Plan or apply R2 bucket provisioning from an account or Cloudflare library:

```powershell
go run .\cmd\supercdnctl -- provision-cloudflare-r2 -cloudflare-library overseas_accel
go run .\cmd\supercdnctl -- provision-cloudflare-r2 -cloudflare-library overseas_accel -dry-run=false
go run .\cmd\supercdnctl -- provision-cloudflare-r2 -cloudflare-account cf_business_main -bucket supercdn-overseas-accel -public-base-url https://overseas-accel.r2.qwk.ccwu.cc -dry-run=false
```

`provision-cloudflare-r2` defaults to dry-run. It creates the R2 bucket when missing, then applies the same CORS/domain sync as `sync-cloudflare-r2`. Without explicit names, a library such as `overseas_accel` resolves to bucket `supercdn-overseas-accel` and public URL `https://overseas-accel.r2.<root_domain>`. In multi-account mode, the generated account label is appended to avoid collisions. Bucket upload still requires S3-compatible R2 access keys in `cloudflare_accounts[].r2` before that account becomes storage-capable.

Create or import the R2 S3 credentials used by the storage data plane:

```powershell
go run .\cmd\supercdnctl -- create-r2-credentials -cloudflare-account cf_business_main
go run .\cmd\supercdnctl -- create-r2-credentials -cloudflare-account cf_business_main -write-config .\configs\config.local.json -dry-run=false
go run .\cmd\supercdnctl -- set-r2-credentials -config .\configs\config.local.json -cloudflare-account cf_business_main -access-key-id <id> -secret-access-key <secret>
```

`create-r2-credentials` creates a Cloudflare Account API Token scoped to the R2 bucket and writes the resulting S3 `access_key_id`/`secret_access_key` into the local config when `-write-config` is provided. It requires the Cloudflare token to have account-token management permissions. `set-r2-credentials` is the manual import fallback; it never calls the Super CDN server or Cloudflare.

Plan or apply R2 CORS/domain repair from the account config:

```powershell
go run .\cmd\supercdnctl -- sync-cloudflare-r2 -cloudflare-account cf_business_main
go run .\cmd\supercdnctl -- sync-cloudflare-r2 -cloudflare-library overseas_accel
go run .\cmd\supercdnctl -- sync-cloudflare-r2 -all -dry-run=false
```

`sync-cloudflare-r2` defaults to dry-run. It writes a CORS rule for GET/HEAD public reads with `Access-Control-Allow-Origin: *` by default, and attaches `cloudflare_accounts[].r2.public_base_url` as an R2 custom domain or enables the matching r2.dev managed domain. Use `-cors-origins` to override the default. Use `-force` before replacing a different existing CORS policy or updating an inactive custom domain.

R2-backed Cloudflare libraries also support `health-check` and `init-libraries`. Passive health checks list the bucket or binding prefix; `-write-probe` writes, reads and deletes a temporary object. Directory initialization treats object-store directories as virtual paths and writes the `_supercdn/init.json` marker object.

### Asset buckets

Static asset buckets are local metadata namespaces for reusable assets. Bucket state, object indexes, usage and policies stay in SQLite; cloud drive paths only store resource files and low-frequency directory structure.

Create and initialize a bucket:

```powershell
go run .\cmd\supercdnctl -- create-bucket -slug movie-posters -name 影视海报桶 -profile china_all -types image
go run .\cmd\supercdnctl -- create-cdn-bucket -slug overseas-posters -name overseas-posters -types image,archive
go run .\cmd\supercdnctl -- create-domestic-cdn-bucket -slug mobile-posters -line mobile -types image,document
go run .\cmd\supercdnctl -- create-ipfs-bucket -slug durable-assets -types image,archive
go run .\cmd\supercdnctl -- init-bucket -bucket movie-posters
```

`create-cdn-bucket` is the overseas object-CDN shortcut. It defaults to route profile `overseas_r2` and `Cache-Control: public, max-age=31536000, immutable`; use versioned logical paths or override `-cache-control` for mutable files.

`create-domestic-cdn-bucket` is the AList/OpenList domestic shortcut. It defaults to the mobile line (`china_mobile`) and `Cache-Control: public, max-age=86400`; pass `-line telecom|unicom|all` or `-profile china_mobile` when you need an explicit route. `create-mobile-cdn-bucket` is a short alias for the mobile line.

`create-ipfs-bucket` is the IPFS/Pinata shortcut. It defaults to route profile `ipfs_archive` and `Cache-Control: public, max-age=31536000, immutable`; configure that profile with `primary: ipfs_pinata` when you want CID-first durable assets.

Upload and read an object:

```powershell
go run .\cmd\supercdnctl -- upload-bucket -bucket movie-posters -file .\poster.jpg -path posters/poster.jpg
go run .\cmd\supercdnctl -- upload-bucket -bucket overseas-posters -file .\poster.jpg -path posters/v1/poster.jpg -warmup
go run .\cmd\supercdnctl -- upload-bucket -bucket mobile-posters -file .\poster.jpg -path posters/poster.jpg -asset-type image -warmup
go run .\cmd\supercdnctl -- upload-bucket -bucket durable-assets -file .\poster.jpg -path posters/v1/poster.jpg -asset-type image -warmup
go run .\cmd\supercdnctl -- ipfs-smoke -file .\poster.jpg -bucket durable-smoke -download-runs 3
go run .\cmd\supercdnctl -- list-bucket -bucket movie-posters
```

Bucket uploads return both the relative `url` and the absolute `public_url`/`urls` fields when `server.public_base_url` is configured. If the storage backend exposes an HTTP direct URL, uploads also return `cdn_url` / `storage_url`; for `overseas_r2` this should be the R2/Cloudflare public URL, and for IPFS/Pinata this is the configured gateway URL for the returned CID. `-warmup` immediately probes the uploaded public URL; use `-warmup-method GET` when you want the edge to fetch the full object.

For domestic AList/OpenList buckets, `public_url` is the stable Super CDN `/a/...` URL. `cdn_url` is the signed AList storage URL when available; some downstream cloud drives reject `HEAD` after redirect, so use `GET` or a range `GET` when validating the direct storage path.

Purge or warm Cloudflare cache for tracked bucket URLs:

```powershell
go run .\cmd\supercdnctl -- purge-bucket -bucket movie-posters -prefix posters/ -dry-run
go run .\cmd\supercdnctl -- warmup-bucket -bucket movie-posters -path posters/poster.jpg -dry-run
go run .\cmd\supercdnctl -- warmup-bucket -bucket movie-posters -path posters/poster.jpg -method GET
```

`purge-bucket` and `warmup-bucket` select objects by `-path`, comma-separated `-paths`, `-prefix`, or `-all`. Warmup uses `HEAD` by default; pass `-method GET` only when you intentionally want to fetch full objects through the public URL.

Delete one object, a selected group, or a whole bucket:

```powershell
go run .\cmd\supercdnctl -- delete-bucket-object -bucket movie-posters -path posters/poster.jpg
go run .\cmd\supercdnctl -- delete-bucket-object -bucket movie-posters -paths posters/a.jpg,posters/b.jpg
go run .\cmd\supercdnctl -- delete-bucket-object -bucket movie-posters -prefix posters/tmp/ -force
go run .\cmd\supercdnctl -- delete-bucket-object -bucket movie-posters -all -force
go run .\cmd\supercdnctl -- delete-bucket -bucket movie-posters -force
```

`delete-bucket-object` supports the same object selectors as cache purge/warmup: `-path`, comma-separated `-paths`, `-prefix`, or `-all`. Prefix and all-object deletion require `-force`. By default deletions remove tracked remote replicas before removing local metadata; `-delete-remote=false` only drops the local index and should be reserved for recovery work.

Public bucket assets are served at:

```text
/a/{bucket_slug}/{logical_path}
```

The physical storage key is content-hash based under `assets/buckets/{bucket_slug}/{type}/yyyy/mm/...`, while the user-facing logical path is stored locally.

### Credential checklist

Prepare these values when enabling real backends:

- Super CDN: `SUPERCDN_ADMIN_TOKEN`
- Cloudflare R2: `cloudflare_accounts[].r2.account_id` (or inherit from the account), optional `api_token` for R2 bucket/CORS/domain control-plane calls when it differs from the account DNS/Worker token, `bucket`, `access_key_id`, `secret_access_key`, optional `public_base_url`, `endpoint`, `proxy_url`
- Cloudflare cache purge, DNS and Worker routes: `CF_ZONE_ID`, `CF_API_TOKEN`, `cloudflare.site_dns_target`, `cloudflare.worker_script`
- Cloudflare DNS automation: `cloudflare.root_domain`, `cloudflare.site_domain_suffix`, and an API token with Zone Read, DNS Edit and Workers Routes Write
- Cloudflare R2 diagnostics: an API token with account-level R2 bucket read access, plus R2 bucket CORS/domain read access when those checks are needed
- AList/OpenList mount point: token, optional username/password for `/api/auth/login` refresh, internal `base_url`, public `public_base_url`, optional mount `root`, explicit `proxy_url`
- Resource libraries: library name, mount point name, and exactly one path per binding
- Pinata/IPFS: `PINATA_JWT`, gateway URL such as `https://gateway.pinata.cloud`, optional `api_base_url`, optional `upload_base_url`, optional `group_prefix`, optional `proxy_url`
- Domain routing: origin domain for Worker `ORIGIN_BASE_URL`, site domains to bind with `create-site -domains`

## Cloudflare Worker

Copy `worker/wrangler.toml.example` to `worker/wrangler.toml`, set `ORIGIN_BASE_URL`, then:

```powershell
cd worker
npm install
npm run deploy
```

The Worker keeps public site URLs same-origin while still using storage direct URLs behind the edge. The Go origin marks storage redirects with `X-SuperCDN-Redirect: storage`; the Worker follows only those marked redirects, fetches the storage object, strips sensitive response headers, fixes common MIME types by path, and returns the object under the original site URL. Normal site redirects are passed through unchanged.

Runtime behavior:

- `GET`/`HEAD` requests use the edge cache for `200` and `404` responses unless the response is private or `no-store`.
- `Range` requests bypass the edge cache and preserve range delivery.
- Storage requests only forward safe headers such as `Accept`, `Accept-Language`, `Range`, validators and `User-Agent`.
- If storage fetch fails and `EDGE_BYPASS_SECRET` is configured, the Worker asks the origin to stream the file directly.

Zero-origin migration diagnostics are available as an explicit dry-run path. Bind a KV namespace as `EDGE_MANIFEST`, publish a deployment manifest with `publish-edge-manifest`, then set `EDGE_MANIFEST_DRY_RUN=true`. Requests with `?__supercdn_edge_manifest=dry-run` or `X-SuperCDN-Edge-Manifest-Dry-Run: true` return the Worker route decision as JSON without fetching origin or storage.

To start moving real traffic off the Go origin, set `EDGE_MANIFEST_MODE=route`. In this mode the Worker reads the active KV manifest and directly returns manifest-backed storage redirects for matching assets and site redirect rules. Smart manifest routes include a routing policy snapshot plus ready source candidates; the Worker chooses by Cloudflare request country for global acceleration and by stable weighted hash for load balancing. IPFS manifest routes include CID metadata and gateway fallback URLs; the Worker fetches those routes from the first healthy gateway and marks the response with `X-SuperCDN-Edge-Source: ipfs_gateway`. Manifest mode does not fall back to the Go origin unless `EDGE_ORIGIN_FALLBACK=true` is explicitly configured. Preferred `hybrid_edge` deployments set `EDGE_ORIGIN_FALLBACK=false` and use `ASSETS`, so entry HTML/SPAs stay on Cloudflare and static resources do not fall back to Go. If `EDGE_ENTRY_ORIGIN_FALLBACK=true`, only entry HTML/SPAs may temporarily fall back to Go after Cloudflare Static fails, with warning headers and `Cache-Control: no-store`; static resources still cannot use Go as fallback. `EDGE_MANIFEST_MODE=enforce` also blocks non-static origin fallback and is intended after entry HTML is served by Cloudflare-native static hosting.

For the hybrid no-Go website path, deploy this Worker with a Cloudflare Static Assets `ASSETS` binding, `run_worker_first = true`, and `EDGE_STATIC_ASSETS=true`. Manifest-backed assets are redirected before static handling; entry HTML and SPA fallback are then served by `env.ASSETS.fetch(request)`, so public website traffic no longer needs the Go origin.

For compatibility origin bypass in the old edge proxy path, set a shared secret in both places:

```powershell
wrangler secret put EDGE_BYPASS_SECRET
```

and set `cloudflare.edge_bypass_secret` on the Go origin to the same value. The Worker also forwards `X-Forwarded-Host`, so domain-bound compatibility sites still resolve at the Go origin. Do not use this bypass as static-resource failover for preferred `hybrid_edge` sites.
