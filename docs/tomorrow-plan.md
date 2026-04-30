# Super CDN Handoff

Last updated: 2026-04-30 Asia/Shanghai.

## Current State

The service is still in development mode. There is no need to preserve compatibility with the old static-site deployment flow.

Current local service:

```text
http://127.0.0.1:8080
```

Current server deployment:

```text
server: 166.0.198.218
domain: qwk.ccwu.cc
service: systemd unit supercdn
install dir: /opt/supercdn
public URL: https://qwk.ccwu.cc/
health URL: https://qwk.ccwu.cc/healthz
reverse proxy: nginx on 80/443
origin bind: 127.0.0.1:8080
TLS: Let's Encrypt via certbot, certificate expires 2026-07-26
admin token: stored locally in configs/private/prod_admin_token.txt and remotely in /opt/supercdn/config.json
previous binary update: 2026-04-29 Asia/Shanghai, backed up under /opt/supercdn/backups/20260428T223529Z
latest binary update: 2026-04-29 Asia/Shanghai, backed up under /opt/supercdn/backups/20260428T225418Z
last config merge: 2026-04-29 Asia/Shanghai, Cloudflare account/library merge backed up at /opt/supercdn/backups/config.cloudflare-merge-20260428T224341Z.json
previous deployed binary hashes: supercdn 4304ce8c8ed9c948aa69d04de9720ad47e335609ef9cb5b822e02bed5f12c3f1, supercdnctl 6096b5b308e875f223e7dff0b3236c34af17bbf99c0a3589c09373f3bc9fa6c6
current deployed binary hashes: supercdn a5e4753c33f6ee3fbf5085ba7ca9e038901f6a255313af838755f18a921b504b, supercdnctl 0f6d4c0514d2296feea98f7ca68ac5d7d7c2f14a213c535cdc39085d2c0431e3
SPA fallback binary update: 2026-04-29 Asia/Shanghai, backed up under /opt/supercdn/backups/20260428T230417Z
SPA fallback deployed binary hashes: supercdn 4fc4e63c1f519a4c0ca10c82d944ec9ca1cec278d47379464f320bdf15d18180, supercdnctl 9479492af07bcc0757306ccdc38d45dec814a517b942bcec3a3810e323a23c86
asset bucket UX binary update: 2026-04-30 Asia/Shanghai, backed up under /opt/supercdn/backups/20260429T155941Z-bucket-list-fix
asset bucket UX deployed binary hashes: supercdn 2062fd23697d45611fd0bdfcd288b9d5d261f5f96d0668620e5bf77f7c2c14ed, supercdnctl 52324207f7c27537fe146ecef63431bd60dfb04e304391c6271fbb734c5dcfb3
overseas R2 CDN bucket binary update: 2026-04-30 Asia/Shanghai, backed up under /opt/supercdn/backups/20260429T161836Z-bucket-r2-redirect
overseas R2 CDN bucket deployed binary hashes: supercdn 706f0abd66fa18132ede1bf9462238600b38261c51facfa683f91f315f738ad0, supercdnctl 74f3339675e51a8fdfeff735c0910cea3ab6613a146e34cf8f57446ccb3d8325
china mobile AList retry binary update: 2026-04-30 Asia/Shanghai, backed up under /opt/supercdn/backups/20260429T210651Z-china-mobile-alist-retry
china mobile AList retry deployed binary hashes: supercdn b97fe62869332e48d5d867f4a8f65ae419c58cd5bc4bf840f92ee79d26df61d9, supercdnctl 985854afc5203b803c6ce20d3666886d6d699645acf9bed80673f42393a4724d
domestic CDN bucket binary update: 2026-04-30 Asia/Shanghai, backed up under /opt/supercdn/backups/20260429T211742Z-domestic-cdn-bucket
domestic CDN bucket deployed binary hashes: supercdn 6d46b7e26fd9c7286a28e9037ebf729bb42425a3dc3963e703a6aa9a85b844d0, supercdnctl 07199d6ec8dfff4e25e6123a3c142650c02d22f7c9c103e68ea4cd3686607b14
AList parent-directory binary update: 2026-04-30 Asia/Shanghai, backed up under /opt/supercdn/backups/20260429T213448Z-alist-autodirs
AList parent-directory deployed binary hashes: supercdn 0fd51a8f9e6e35a477b72c050faf367d9d41bbf35b18af475f2f216f4c7e0808, supercdnctl e7ec37dd469ff3bea711ac82e4474a0155402593d815838b9a68e8f75b656c53
AList zero-length upload binary update: 2026-04-30 Asia/Shanghai, backed up under /opt/supercdn/backups/20260429T235323Z-alist-zero-length
AList zero-length upload deployed binary hashes: supercdn 1dcb17bc183c4b1fdd0a16a2d56d88308ee600c23b58f566b7aab91d15ca89fe, supercdnctl 20e118be1b72d2363342481b088be57cf110692f25e913d5af29c66b032819a8
```

Latest live validation:

```text
server health: https://qwk.ccwu.cc/healthz returns 200
cloudflare_static canary: path2agi-static-canary deployment dpl-di55pwokt51k returns https://path2agi-static-test.qwk.ccwu.cc/
cloudflare_static cache headers: `/` returns `Cache-Control: public, max-age=0, must-revalidate`; `/path2agi-data.js?v=escape-fix-20260428` returns `Cache-Control: public, max-age=31536000, immutable`
cyberstream cloudflare_static milestone: deployment dpl-di55wdod7eqh returns https://cyberstream-static-test.qwk.ccwu.cc/ with direct JS/CSS, immutable asset cache, SPA fallback for /movie/123, and browser screenshots in data/cyberstream-static-canary-home.png plus data/cyberstream-static-canary-spa.png
overseas default cloudflare_static milestone: deployment dpl-di5fkfplv0yg returns https://cyberstream-default-root-canary.qwk.ccwu.cc/ from a `deploy-site -profile overseas` command without `-target` or `-domains`; probe-site passed for HTML, JS/CSS, immutable asset cache, and SPA fallback /movie/123.
cloudflare_static readiness guard: `deploy-site` now defaults to `-static-verify wait`, probing each custom domain before writing the Super CDN active deployment record. The guard catches TLS/DNS/404/MIME/cache/SPA issues and supports `warn` or `none` for diagnostics. The probe uses `1.1.1.1:53` by default after a live local-DNS cache miss made a newly proxied Cloudflare custom domain look like the old wildcard origin.
cloudflare_static rollback guard: normal `promote-deployment` now rejects non-active Cloudflare Static deployments instead of doing metadata-only rollback. `delete-deployment` on Cloudflare Static returns a warning that it removes Super CDN metadata only and does not delete Worker versions/custom domains.
overseas CDN bucket smoke: `create-cdn-bucket` + `upload-bucket -warmup` created `overseas-r2-smoke-20260430001954`, defaulted to `route_profile=overseas_r2`, stored the object on `overseas_accel`, returned `public_url` https://qwk.ccwu.cc/a/overseas-r2-smoke-20260430001954/docs/readme-20260430001954.md and `cdn_url` https://overseas-accel.r2.qwk.ccwu.cc/assets/buckets/overseas-r2-smoke-20260430001954/documents/2026/04/ed/ed45ae53f5b24487025f6ba2cf106496f9401009a48547e7151499e01520f539.md. Warmup HEAD returned 200; public `/a/...` HEAD returns 302 to R2; direct R2 HEAD returns 200 with `Cache-Control: public, max-age=31536000, immutable`.
asset bucket list deadlock fix: live `GET /api/v1/asset-buckets` returns after the SQLite single-connection rows/usage query fix and reports the smoke bucket with `object_count=1`.
china_mobile line-only validation: `resource-status -library repo_china_mobile` reports `alist_mobile_primary` at `/移动资源/个人云/Super_CDN`. Passive health check passed, then write/read/delete health probe passed with list/write/read/delete latencies 1857/2038/2213/370 ms. `e2e-probe -profile china_mobile` passed with primary target `repo_china_mobile`, object id 45, upload latency 3908 ms, read latency 1976 ms, HTTP 200, and cleanup remote/db both deleted. During the first run it exposed an AList upload retry bug (`seek ... file already closed`) when token refresh was needed; fixed by preventing the HTTP client from closing the reusable file reader between auth retry attempts.
domestic mobile CDN bucket smoke: `create-domestic-cdn-bucket -slug mobile-cdn-smoke-20260430051907 -line mobile` created a bucket with `route_profile=china_mobile` and `default_cache_control=public, max-age=86400`. `upload-bucket -warmup` stored README.md on `repo_china_mobile`, object id 46, and returned stable public URL https://qwk.ccwu.cc/a/mobile-cdn-smoke-20260430051907/docs/readme-20260430051907.md plus signed AList storage URL. Public `/a/...` HEAD returned 200 with the bucket cache header, warmup returned 200, and direct signed storage Range GET followed redirects to 206 with the expected file prefix. Direct storage HEAD can return 403 because the downstream drive rejects HEAD; GET/Range GET is the meaningful validation for that path.
domestic mobile origin-assisted website smoke: first `deploy-site -site path2agi-mobile-go -dir test_file/path2agi -profile china_mobile -target origin_assisted -env production -promote` exposed an AList parent-directory gap because the new deployment path did not exist yet. AList uploads now create missing parent directories before `PUT`. The second deployment `dpl-di5ypwr3ukab` is active at https://qwk.ccwu.cc/s/path2agi-mobile-go/ with `route_profile=china_mobile`, `deployment_target=origin_assisted`, `delivery_summary={origin:1, redirect:1}`. Root HTML HEAD returns 200 from Go with `Cache-Control: public, max-age=300`; `path2agi-data.js` HEAD returns 302 with `X-Supercdn-Redirect: storage` to the signed mobile AList path; Range GET follows the mobile chain to 206. `probe-site -url https://qwk.ccwu.cc/s/path2agi-mobile-go/ -max-assets 5` passed with one redirected script asset and final 200.
cyberstream mobile origin-assisted website smoke: `test_file/cyberstream` latest source needed local TypeScript compatibility fixes and removal of a stale `/index.css` reference before Vite could build cleanly. Site `cyberstream-mobile-go` deployment `dpl-di61osrsjdz2` is active at https://cyberstream-mobile-go.sites.qwk.ccwu.cc/ with `route_profile=china_mobile`, `deployment_target=origin_assisted`, and `delivery_summary={origin:1, redirect:2}`. Root HTML returns 200 from Go origin; main JS returns 302 with `X-Supercdn-Redirect: storage` to the signed mobile AList path; Range GET returns 206; SPA fallback `/movie/123` returns 200 HTML. `probe-site -url https://cyberstream-mobile-go.sites.qwk.ccwu.cc/ -spa-path /movie/123 -max-assets 10` passed. Headless Chrome screenshots are in `data/cyberstream-mobile-go-home.png` and `data/cyberstream-mobile-go-mobile.png`; desktop renders, mobile renders but the hero title is oversized and clips horizontally.
legacy R2 site probe: cyberstream still passes HTML, JS/CSS redirect MIME/CORS, and /movie/123 SPA fallback checks
```

Latest legacy R2 site validation:

```text
site_id: cyberstream
source: G:\AI\AI_private\Codex_projects\Super_CDN\test_file\cyberstream
production deployment: dpl-di49qyrhf5y0
route_profile: overseas_r2
public URL: https://cyberstream.sites.qwk.ccwu.cc/?v=dpl-di49qyrhf5y0
preview URL: https://qwk.ccwu.cc/p/cyberstream/dpl-di49qyrhf5y0/
delivery summary: origin 1, redirect 3
status: complex Vite/React site builds, deploys, redirects assets to R2, and renders in Edge headless
```

Cloudflare DNS configuration:

```text
private config: configs/private/cloudflare.env
root domain: qwk.ccwu.cc
actual zone_id: c725aacaca98b598d2074f1f50bcd6d8
subdomain mode: two_level
default allocated domain: {site}.sites.qwk.ccwu.cc
wildcard DNS: *.qwk.ccwu.cc and *.sites.qwk.ccwu.cc -> 166.0.198.218, DNS-only
wildcard TLS: qwk.ccwu.cc, *.qwk.ccwu.cc and *.sites.qwk.ccwu.cc
status: API token is configured locally and on the server; DNS create/delete was verified
code status: create-site auto allocates the default site domain, bind-domain appends/replaces domains, domain-status checks local binding and Cloudflare DNS records, deployment responses include production_url/production_urls/preview_url
multi-account status: production config now loads cf_business_main and cf_business_secondary; `cloudflare-status -all` verifies both tokens/zones and R2 buckets.
```

The user-provided `5c...` identifier is stored privately as `CF_ACCOUNT_ID`, but it is not the zone id for `qwk.ccwu.cc`.

Cloudflare R2 live state:

```text
account label: cf_business_main
logical library: overseas_accel
bucket: supercdn-overseas-accel
public base URL: https://overseas-accel.r2.qwk.ccwu.cc
current bucket CORS: GET/HEAD, origins *, allowed headers *, exposed ETag/Content-Length/Content-Type/Cache-Control, max-age 86400
reason: module scripts redirected from site domains to the R2 custom domain require cross-origin JavaScript/CSS reads

secondary account label: cf_business_secondary
secondary root domain: cloudflare.pics
secondary bucket: aawadmortetl
secondary public base URL: https://image.cloudflare.pics
secondary status: token, zone, R2 bucket, CORS and custom domain all verified by production `cloudflare-status -all`
```

Current test site:

```text
site_id: ai-learning-map
name: AI学习星图
active deployment: dpl-di3ftps5h4cg
route_profile: china_all
storage layout: verbatim
local URL: http://127.0.0.1:8080/s/ai-learning-map/
server URL: https://qwk.ccwu.cc/
allocated URL: https://ai-learning-map.sites.qwk.ccwu.cc/
```

The active deployment was uploaded from:

```text
G:\AI\AI_private\Codex_projects\Super_CDN\test_file\dist
```

The active OpenList layout is:

```text
/豆包/Super_CDN/sites/ai-learning-map/deployments/dpl-di3ftps5h4cg/root/index.html
/豆包/Super_CDN/sites/ai-learning-map/deployments/dpl-di3ftps5h4cg/root/path2agi-data.js
/豆包/Super_CDN/sites/ai-learning-map/artifacts/dpl-di3ftps5h4cg.zip
/豆包/Super_CDN/sites/ai-learning-map/manifests/dpl-di3ftps5h4cg.json
```

Additional validation sites:

```text
site_id: path2agi
active deployment: dpl-di49436d5rg9
URL: https://path2agi.sites.qwk.ccwu.cc/?v=escape-fix-20260428
status: path2agi-data.js escaping fixed locally, deployed, and verified through 302 -> R2
```

Cloudflare-native static hosting canary:

```text
source: G:\AI\AI_private\Codex_projects\Super_CDN\test_file\path2agi
worker: supercdn-path2agi-static-test
custom domain: https://path2agi-static-test.qwk.ccwu.cc/
latest worker version: cc489b82-a0d0-4975-82ec-7973de3573ae
cache policy worker version: e35d2118-222a-4765-9506-15bc3e0e5a9f
local control-plane deployment: dpl-di5544cdc5uo
production control-plane deployment: dpl-di55dq96wt0z
cache policy production deployment: dpl-di55pwokt51k
deployment target: Workers Static Assets
status: deployed and verified; index.html and path2agi-data.js are served directly by Cloudflare with no R2 redirect and no Go origin dependency
comparison: existing https://path2agi.sites.qwk.ccwu.cc/ still works through Go origin HTML plus 302 -> R2 for path2agi-data.js
cache note: Workers Static Assets defaulted to `Cache-Control: public, max-age=0, must-revalidate`; SuperCDN `-static-cache-policy auto` now injects a temporary `_headers` file for Cloudflare Static deploys while keeping the source directory unchanged. Verified live: HTML stays revalidating and query-versioned `path2agi-data.js` is immutable for one year.
automation: `supercdnctl deploy-site -target cloudflare_static` now publishes through local Wrangler Workers Static Assets and records Super CDN deployment metadata; `publish-cloudflare-static` remains the lower-level canary/diagnostic publisher.
```

```text
site_id: cyberstream
active deployment: dpl-di49qyrhf5y0
URL: https://cyberstream.sites.qwk.ccwu.cc/?v=dpl-di49qyrhf5y0
local source: test_file/cyberstream
local build output: test_file/cyberstream/dist
status: complex frontend smoke test passed
```

CyberStream Cloudflare-native milestone:

```text
site_id: cyberstream-static-canary
deployment: dpl-di55wdod7eqh
worker: supercdn-cyberstream-static-test
worker version: b7fe743f-0033-4de7-aa09-915cb4a414dc
URL: https://cyberstream-static-test.qwk.ccwu.cc/
source: G:\AI\AI_private\Codex_projects\Super_CDN\test_file\cyberstream\dist
deployment target: Workers Static Assets
file_count: 4
total_size: 613166
cache_policy: auto
headers_generated: true
not_found_handling: single-page-application
status: milestone passed. HTML, JS, CSS and /movie/123 are served by Cloudflare Static Assets with no R2 redirect and no Go origin dependency. Playwright screenshots confirm nonblank rendered UI for both root and SPA deep link.
```

CyberStream notes:

- The downloaded source now includes the previously missing components (`Views`, `History`, `Toaster`, `CyberComponents`, `Cards`).
- `npm ci` and `npm run build` pass.
- `npx tsc --noEmit` still reports frontend type errors; this does not currently block Vite production build.
- The original HTML referenced `/index.css` without providing the file. A local empty `test_file/cyberstream/index.css` placeholder was added so Vite emits a real CSS asset and browser MIME noise is avoided.
- The built app still loads external services directly: `https://cdn.tailwindcss.com`, `https://esm.sh/...`, Google fonts from runtime CSS, and API calls to `https://pw.pioneer.fan:84/api`.
- External links are intentionally outside Super CDN's artifact control. The deployment test only proves bundled output and platform delivery behavior.

## Decisions Made

Static-site hosting now preserves the original `dist` directory structure. The server no longer rewrites `index.html` or any other website file.

The canonical site deployment API is:

```http
POST /api/v1/sites/{id}/deployments
```

The old development-only endpoint was removed:

```http
POST /api/v1/sites/{id}/deploy
```

New site files are stored under:

```text
sites/{site}/deployments/{deployment}/root/{original_path}
```

The old Web layout is no longer used for site hosting:

```text
sites/_objects/{sha_prefix}/{sha}{ext}
```

Keep content-hash style storage for reusable asset buckets, not for Web hosting.

## Routing Model

Production should use host-based routing:

```text
Host: ai-learning-map.example.com
Path: /
Path: /assets/app.js
```

This lets absolute asset paths such as `/assets/app.js` resolve without rewriting files.

Local subpath testing remains useful only for projects with relative asset references:

```text
http://127.0.0.1:8080/s/ai-learning-map/
```

If a site uses root-absolute paths like `/assets/app.js`, local subpath mode cannot infer the site identity from the path. Treat that as a testing limitation, not a reason to rewrite files.

## Important Constraints

Do not auto-read `HTTP_PROXY`, `HTTPS_PROXY`, or `NO_PROXY`. Only use explicitly configured `proxy_url`.

Current convention:

- Domestic AList/OpenList mount points: no proxy.
- R2 and IPFS/Pinata: use `http://127.0.0.1:10808` only when explicitly configured.

AList/OpenList public links must include `sign`. The storage layer now refreshes signed `/d/...?...sign=` links through `Stat` before redirecting.

Go HTTP redirects intentionally strip `Referer` to avoid OpenList/downstream drive `Referer check fail` errors.

AList uploads create missing parent directories before `PUT`, because site deployment keys include a fresh deployment id and cannot rely on manual directory pre-creation.

AList upload requests send an explicit `Content-Length: 0` for zero-byte files. Some AList/downstream combinations reject empty uploads when the request length is omitted.

## What Was Cleaned

The old deployment record was removed:

```text
dpl-di3e6akzdg1g
```

Known old OpenList files from the hash-based Web layout were removed by path:

```text
/豆包/Super_CDN/sites/_objects/81/81e5f7e2c25abc8d284c0c12fe1fca933532477ed8684afa69a50767a808532f.html
/豆包/Super_CDN/sites/_objects/c3/c376ae4d27dc309c10186c834aecc7e8a17570796dfba6b443f8d0d1b462f01d.js
/豆包/Super_CDN/sites/ai-learning-map/artifacts/dpl-di3e6akzdg1g.zip
/豆包/Super_CDN/sites/ai-learning-map/manifests/dpl-di3e6akzdg1g.json
```

The old local SQLite database may still contain orphan object rows from earlier experiments. This is acceptable in development. If it becomes noisy, reset the local database and redeploy the active test site.

## Verification Commands

Run tests:

```powershell
go test ./...
```

Build:

```powershell
go build -o .\bin\supercdn.exe .\cmd\supercdn
go build -o .\bin\supercdnctl.exe .\cmd\supercdnctl
```

Deploy current test site:

```powershell
.\bin\supercdnctl.exe -token change-me deploy-site `
  -site ai-learning-map `
  -dir "G:\AI\AI_private\Codex_projects\Super_CDN\test_file\dist" `
  -profile china_all `
  -env production `
  -promote
```

Check:

```powershell
curl.exe -I http://127.0.0.1:8080/s/ai-learning-map/
curl.exe -I http://127.0.0.1:8080/s/ai-learning-map/path2agi-data.js
```

## Tomorrow Goals

1. Deploy the Go service on the server. Done on 2026-04-27.
2. Bind a real domain or subdomain to the site and add it with `create-site -domains`. Done for `qwk.ccwu.cc`.
3. Verify host-based routing for:
   - `/`
   - relative assets
   - root-absolute assets such as `/assets/...`
   - SPA fallback routes
4. Decide the safe redirect policy for Web files. Done on 2026-04-27.

Current redirect policy:

- Root `index.html` is served through Go, including `/`, `/index.html`, and SPA fallback routes that resolve to root `index.html`.
- Other successful site file requests return `302 Found` to the freshest direct storage URL when one is available.
- Site-file `302` responses use `Cache-Control: no-store` so browser caches do not pin old deployment asset redirects.
- Range requests and 404 responses stay on the Go origin.
- Direct storage URLs are refreshed through `Stat` first, so AList/OpenList signed links are not served stale.
- Resource-library reads fall back to another binding when the binding encoded in an old locator is unavailable. This is a read-path guardrail; real outage tolerance still requires `route_profiles[].backups` or multiple resource-library bindings with backfilled objects.

Overclock mode was added as `limits.overclock_mode`. Keep it off by default. When enabled, it skips configured upload-size, file-count, resource-library capacity/file-size/batch/daily-upload, resource-health, asset-bucket capacity/file-size/type and transfer-slot limits, and API responses include a risk warning. This can cause unpredictable or catastrophic results if the remote drive policy tightens or the server accepts too much work.

Site inspection is local-first and non-blocking. `supercdnctl inspect-site -dir ./dist` scans the built artifact for module scripts, dynamic imports, CSS relative assets, fonts, wasm, service workers, source maps and root-absolute paths. Deployments store the same report in the manifest and expose it as `inspect`.

File delivery can now be overridden in `supercdn.site.json` with `delivery` rules. Default remains root `index.html` on origin and other successful files by 302 redirect; use `{"path": "/sw.js", "mode": "origin"}` or prefix rules such as `{"path": "/assets/*", "mode": "origin"}` if a complex frontend needs a same-origin fallback.

Do not reintroduce runtime HTML rewriting unless there is a very narrow, explicit rule and a test site that proves it is safe.

## Architecture Direction

The product goal is website hosting plus CDN acceleration, with Cloudflare-native hosting used as the overseas static-site layer rather than rebuilding that whole layer ourselves.

Target shape:

- Go service: deployment/control plane, site inspection, health checks, manifest builder, Cloudflare automation, storage synchronization and rollback.
- Overseas website hosting: Workers Static Assets first, with Cloudflare Pages as a supported alternative for entry HTML and ordinary static sites when the site fits native Cloudflare limits. For overseas-only acceleration, do not involve R2 when Cloudflare-native static hosting fits the site.
- Overseas object acceleration: Cloudflare R2 for large objects such as video, images, archives and other reusable downloads, plus account-isolated overseas acceleration nodes.
- Domestic acceleration: AList/OpenList-backed resource libraries for China-facing static resources.
- Edge routing: Worker reads KV or another edge-readable manifest store for `virtual path -> storage locator` lookups, then keeps the request on Cloudflare-native hosting, redirects to R2, or redirects/proxies to domestic AList/OpenList based on route policy.
- Future global acceleration: routing policy should choose AList or R2 by site, path, asset class, health, region and availability, so one deployment can be optimized for domestic and overseas users.

The current Go-origin HTML plus Go-origin 302 flow is only an intermediate, origin-assisted CDN stage. New features should avoid deepening runtime dependency on the Go origin when a Cloudflare-native or edge-manifest path is plausible.

Current overseas R2 decision:

- Each Cloudflare/R2 account remains an independent acceleration node.
- R2 is not the default website deployment surface. Use it for large objects, media, archives, reusable downloads and object-level acceleration, not ordinary static-site hosting when Workers Static Assets or Pages can host the site directly.
- Do not introduce object sharding across R2 accounts for now. Cloudflare/R2 performance is strong enough that extra sharding complexity is not worth the operational cost.
- Use multiple R2 accounts for account isolation, redundancy, migration and future policy/routing choices, not for performance striping.

## Near-Term Engineering Tasks

- Codify the live R2 static-site CORS lesson: change the default `sync-cloudflare-r2` / `provision-cloudflare-r2` CORS origin from the R2 `public_base_url` origin to `*`, update tests/help/docs, run `go test ./...`, rebuild, and redeploy the server/CLI. Done locally.
- Add a live static-site probe. Done locally as `supercdnctl probe-site`: it fetches the active deployment HTML, follows redirected JS/CSS with an `Origin` header, checks MIME/CORS, and can verify a configured SPA fallback path. Remaining optional enhancement: headless browser white-screen detection.
- Add the first zero-origin sidecar primitive. Done locally as `GET /api/v1/sites/{id}/deployments/{deployment}/edge-manifest` and `supercdnctl export-edge-manifest`; it exports exact file routes, directory index aliases, SPA fallback, 404 behavior, storage redirect locations and delivery-rule overrides without changing production traffic.
- Add Worker-side edge manifest dry-run consumption. Done locally: when `EDGE_MANIFEST_DRY_RUN=true` and `EDGE_MANIFEST` KV is bound, `?__supercdn_edge_manifest=dry-run` returns the route decision JSON from the edge manifest without fetching origin or storage.
- Add Worker-side active edge manifest routing. Done locally: `EDGE_MANIFEST_MODE=route` lets the Worker return manifest-backed storage redirects and site redirects without contacting Go, while unresolved HTML/origin routes still fall back to the origin-assisted path. `EDGE_MANIFEST_MODE=enforce` disables that fallback for the final no-Go cutover.
- Add Worker-side Cloudflare Static fallback. Done locally: when this Worker has an `ASSETS` binding and `EDGE_STATIC_ASSETS=true`, unresolved HTML/origin routes are served by Cloudflare Static Assets instead of Go, while manifest-backed resources still route first.
- Run the first true no-Go hybrid website canary. Done live with `cyberstream-hybrid-canary.qwk.ccwu.cc`: entry HTML and SPA fallback came from Cloudflare Static Assets, JS chunks were routed by KV edge manifest to the mobile AList line, browser screenshots rendered, `probe-site` passed, and a unique probe path had zero origin/Nginx log hits.
- Promote the no-Go hybrid canary flow into `deploy-site -target hybrid_edge`. Done locally and live-tested with `cyberstream-hybrid-canary`: the CLI now uploads the storage deployment, publishes the active KV edge manifest, deploys the Worker with `ASSETS` and `run_worker_first`, verifies HTML/SPA/redirected assets, and confirmed a unique probe path had zero origin/Nginx log hits.
- Add control-plane KV publication. Done locally as `supercdnctl publish-edge-manifest`; it plans or writes deployment and active manifest keys to Cloudflare Workers KV, defaulting to dry-run and avoiding active-key writes for non-active deployments.
- Add a deployment target model before pushing deeper into custom R2/KV routing. Done locally: sites, deployments, deployment manifests and edge manifests now carry `deployment_target`; route profiles can set the default. First-class targets are `cloudflare_static` for Workers Static Assets/Pages, `hybrid_edge` for Cloudflare entry HTML plus Worker/KV path routing, and `origin_assisted` for the current Go-origin fallback path.
- Add the first formal Cloudflare Static deployment flow. Done locally and live-tested with path2agi: `deploy-site -target cloudflare_static` publishes Workers Static Assets, captures Worker/domain/version metadata, creates an active Super CDN deployment, and returns the HTTPS production URL. Remaining: production cache-header policy before using it as the default overseas website path.
- Add Cloudflare Static cache-header automation. Done locally and live-tested with path2agi: `deploy-site -target cloudflare_static` and `publish-cloudflare-static` accept `-static-cache-policy auto|force|none`; auto respects an existing `_headers` file or injects a generated one from a temporary assets copy. Production deployment `dpl-di55pwokt51k` records `cache_policy:auto` and `headers_generated:true`.
- Add Cloudflare Static SPA fallback automation. Done locally and live-tested with CyberStream: `-static-spa` generates a temporary Wrangler config with `assets.not_found_handling = "single-page-application"`; production deployment `dpl-di55wdod7eqh` records the setting and `/movie/123` returns HTML directly from Cloudflare Static Assets.
- Run a real Cloudflare-native static hosting canary with CyberStream, then compare it with the current R2/KV canary on deployment complexity, SPA fallback, custom domain handling, cache behavior and rollback. Done as milestone canary `cyberstream-static-canary`; remaining follow-up is rollback ergonomics and possibly promoting the pattern as the default overseas site path.
- Promote Cloudflare Static to the ordinary overseas site default path. Done locally: `deploy-site` now resolves the site/profile deployment target through `GET /api/v1/sites/{id}/deployment-target`; if `overseas.deployment_target=cloudflare_static` and no `-domains` are passed, the CLI uses existing site domains or a one-level `cloudflare.root_domain` default domain for Wrangler. Important live lesson: nested `*.sites.qwk.ccwu.cc` is fine for Go-origin DNS defaults, but Cloudflare Static custom domains should use one-level `*.qwk.ccwu.cc` hosts to avoid TLS handshake failure.
- Make overseas object-CDN buckets easy to use. Done and live-tested: `create-cdn-bucket` creates a resource bucket with R2-backed `overseas_r2` defaults, bucket uploads now return `public_url`/`urls` and `cdn_url`/`storage_url` when a direct storage URL is available, and `upload-bucket -warmup` can immediately probe or fetch the uploaded public URL.
- Make domestic AList/OpenList CDN buckets easy to use. Done and live-tested for the mobile line: `create-domestic-cdn-bucket` maps `-line mobile|telecom|unicom|all` to domestic route profiles, `create-mobile-cdn-bucket` is the mobile alias, uploads return stable `/a/...` public URLs plus signed AList storage URLs when available, and the live mobile smoke passed warmup, public HEAD and direct Range GET checks.
- Make domestic AList/OpenList origin-assisted website deploys usable. Done and live-tested for the mobile line with `path2agi-mobile-go`: root `index.html` stays on Go origin, non-root JS redirects to the signed mobile AList path, and the site probe passed.
- Validate a larger domestic origin-assisted site. Done with `cyberstream-mobile-go`: after local CyberStream build fixes, Go-origin HTML plus mobile AList JS redirects passed HTTP probe, SPA fallback and headless Chrome render checks.
- Teach the edge manifest to express route intent, not only route mechanics: `entry_html`, `overseas_static`, `overseas_r2`, `domestic_alist`, `fallback_origin`, plus cache/CORS expectations.
- Keep `qwk.ccwu.cc` / ai-learning-map as a legacy domestic-chain compatibility sample. Its normal script path currently works through the AList/OpenList chain, but the final drive stream is not a clean CORS-capable module/fetch target, so probes must distinguish classic scripts from resources that actually require CORS.
- Add a preflight warning when a built `index.html` references root-absolute files that are not present in the artifact, because SPA fallback can turn missing CSS/JS into HTML and produce browser MIME errors.
- Add a deployment-level file policy field in the manifest, for example `delivery: origin | redirect`.
- Add site deployment cleanup that can delete remote deployment files, artifact, manifest, and local object rows together.
- Add a domain validation/status command in `supercdnctl`.
- Add an explicit warning/preflight for local `/s/{site}` testing when root-absolute paths are detected in `index.html`.
- Re-run resource-library initialization after the new `sites/deployments` directory is in config.

## Cloudflare Integration Module

Cloudflare should be developed as its own edge-control module because it sits across the two core product surfaces: website deployment and static resource storage.

Main goals:

- Website deployment: automate DNS/domain checks, worker route binding, edge proxy fallback, cache purge, cache warmup, and same-origin delivery for complex frontend assets when direct 302 storage links are risky.
- Static resource storage: make R2 a first-class storage backend, expose bucket/domain/status checks, support edge-cached asset URLs, and provide purge/verification tools for asset buckets.
- Control plane: add a Cloudflare provider layer that manages account/zone/token validation, DNS records, Worker scripts/routes, R2 bucket metadata, cache purge, and diagnostic status without scattering Cloudflare API calls through server handlers.
- Multi-account topology: model each Cloudflare account as a mount-point-like control plane and group multiple accounts under logical Cloudflare libraries such as `overseas_accel`; keep the legacy single-account `cloudflare` block as a compatibility default.
- Safety: keep local mode independent, keep credentials in private config/env, add dry-run/status commands first, and make destructive Cloudflare changes explicit.

Suggested build order:

1. Cloudflare provider abstraction and `cloudflare-status` command. Done locally: provider covers token verify, zone metadata, DNS records, Worker routes, R2 bucket visibility, purge reuse, and multi-account status via `cloudflare_accounts`.
2. DNS/domain automation hardening for root domains, managed site subdomains, wildcard records, and status diagnostics. In progress locally: explicit site DNS sync can create/update proxied A/AAAA/CNAME records for bound site domains with dry-run and force controls.
3. Worker edge proxy for website deployment. Done locally for same-origin storage fetch, origin fallback, cache headers, Range bypass, explicit Worker route sync and purge by site/deployment manifest.
4. R2 storage hardening: multi-account topology now builds Cloudflare libraries over per-account R2 stores with per-binding path prefixes. Done locally and live-tested: configured bucket existence diagnostics, CORS policy diagnostics, public custom/r2.dev domain diagnostics, R2 health checks/init markers, multipart upload planning for large files, asset-bucket purge/warmup URL planning, dry-run/apply R2 CORS/domain sync, dry-run/apply R2 bucket provisioning from account or library selections, manual/imported R2 S3 credentials, and real website object upload through `overseas_r2`. Remaining: make the code default match the live CORS `*` setting and keep Account API Token creation as an optional convenience when permissions are available.
5. Static asset bucket integration: edge URLs, purge by bucket/object, warmup, and health checks. In progress: overseas CDN bucket shortcut, domestic AList/OpenList CDN bucket shortcut, upload `public_url`/`cdn_url`, upload-time warmup, and the asset-bucket list deadlock fix are implemented and live-tested.
6. End-to-end local/remote verification commands for both website and asset flows.

## Server Verification

```powershell
curl.exe -I http://qwk.ccwu.cc/
curl.exe -i https://qwk.ccwu.cc/healthz
curl.exe -I https://qwk.ccwu.cc/
curl.exe -I https://qwk.ccwu.cc/path2agi-data.js
curl.exe -I https://ai-learning-map.sites.qwk.ccwu.cc/
curl.exe -I https://ai-learning-map.sites.qwk.ccwu.cc/path2agi-data.js
$env:SUPERCDN_TOKEN = Get-Content .\configs\private\prod_admin_token.txt -Raw
.\bin\supercdnctl.exe -server https://qwk.ccwu.cc domain-status -domain ai-learning-map.sites.qwk.ccwu.cc
.\bin\supercdnctl.exe -server https://qwk.ccwu.cc deployment -site ai-learning-map -deployment dpl-di3ftps5h4cg
```

CyberStream verification commands:

```powershell
cd G:\AI\AI_private\Codex_projects\Super_CDN\test_file\cyberstream
npm ci
npm run build
..\..\bin\supercdnctl.exe inspect-site -dir dist
```

```powershell
curl.exe -I https://cyberstream.sites.qwk.ccwu.cc/
curl.exe -I https://cyberstream.sites.qwk.ccwu.cc/assets/index-Fpv9CN4f.js
curl.exe -I -L -H "Origin: https://cyberstream.sites.qwk.ccwu.cc" https://cyberstream.sites.qwk.ccwu.cc/assets/index-Fpv9CN4f.js
curl.exe -I -L -H "Origin: https://cyberstream.sites.qwk.ccwu.cc" https://cyberstream.sites.qwk.ccwu.cc/assets/index-tn0RQdqM.css
curl.exe -I https://cyberstream.sites.qwk.ccwu.cc/movie/123
curl.exe -I -H "Origin: https://cyberstream.sites.qwk.ccwu.cc" https://pw.pioneer.fan:84/api/v1/homepage/config
```

Expected CyberStream checks:

- `/` returns `200 OK` with `text/html`.
- `/assets/index-Fpv9CN4f.js` returns `302 Found`, `Cache-Control: no-store`, and `X-Supercdn-Redirect: storage`.
- Following the JS redirect returns R2 `200 OK`, `Content-Type: text/javascript; charset=utf-8`, and `Access-Control-Allow-Origin: *`.
- Following the CSS redirect returns R2 `200 OK`, `Content-Type: text/css; charset=utf-8`, and `Access-Control-Allow-Origin: *`.
- `/movie/123` returns root `index.html` as SPA fallback.
- The external API returns `Access-Control-Allow-Origin: https://cyberstream.sites.qwk.ccwu.cc`.

Operational notes:

- SSH to `166.0.198.218:22` was intermittently slow or timed out during deployment. TCP sometimes needed retries.
- HTTP now redirects to HTTPS through nginx. The Go service listens only on `127.0.0.1:8080`.
- Nginx config lives at `/etc/nginx/sites-available/supercdn`, with the enabled symlink in `/etc/nginx/sites-enabled/supercdn`.
- Certbot renewal is installed through the Debian `certbot.timer`; renewal deploy hook reloads nginx. Wildcard renewal uses `/root/.secrets/certbot/cloudflare.ini`.
- The old local DB was copied to `/opt/supercdn/data/supercdn.db`; then `qwk.ccwu.cc` was bound to `ai-learning-map`.
- Cloudflare DNS automation can now use the private token in `configs/private/cloudflare.env`.
- New CLI commands: `bind-domain` and `domain-status`. `create-site` also accepts `-domain-id`, `-random-domain`, and `-no-default-domain`.
