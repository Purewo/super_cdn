# Super CDN v0.3.0

[English](release-v0.3.0.md) | [简体中文](release-v0.3.0.zh-CN.md)

Release date: 2026-05-06

Status: internal stable milestone.

This release closes the current CDN and Web hosting cycle. It keeps the preferred Web model from `v0.2.0`: Cloudflare owns entry HTML and SPA fallback, while static resources are delivered from explicit resource libraries. The major hardening in this release is that smart-routed Web assets can now be protected by explicit resource failover, so a failed primary resource line can be retried through another ready source without falling back to the Go origin.

## Included

- Route profile replication policies:
  - `primary_only` stores new objects on the primary line only.
  - `best_effort_backups` queues asynchronous backup replicas after primary success.
  - `require_backups` makes backup replication a synchronous requirement.
- Replica maintenance:
  - Refresh and repair paths skip intentionally deleted replicas by default.
  - Primary-only profiles no longer recreate backup replicas during ordinary repair.
  - Bucket replica refresh exposes object-level diagnostics.
- Web route diagnostics:
  - `route-explain` shows the active deployment route, ready and skipped candidates, simulated country, hash key, selected target and decision reason.
- Hybrid Web failover:
  - `deploy-site -resource-failover` remains opt-in.
  - Smart-routing deployments with `resource_failover=true` now try the selected candidate first, then retry other ready candidates at the Worker edge.
  - Static-resource failover never falls back to the Go origin.
- CDN cleanup and lifecycle controls from the `v0.2.1` patch line remain part of the stable surface:
  - Site offline/online/delete controls.
  - Bucket object exact-path, multi-path, prefix, all-object and bucket deletion controls.

## Live Stable Site

- CyberStream hybrid Web: `https://cyberstream.sites.qwk.ccwu.cc/`

Current production deployment at release time:

- Deployment: `dpl-diaw5ggmmo0v`
- Target: `hybrid_edge`
- Route profile: `smart_ipfs_mobile`
- Routing policy: `mobile_ipfs_global`
- Resource failover: `true`

Observed behavior:

- Entry HTML returns `X-SuperCDN-Edge-Source: cloudflare_static`.
- Static JS returns `X-SuperCDN-Edge-Source: resource_failover`.
- CN route explanation selects `repo_china_mobile`.
- US route explanation selects `ipfs_pinata`.

## Verification

Local:

```powershell
go test ./...
cd worker
npm test
```

Production smoke:

```powershell
supercdnctl route-explain -site cyberstream -path /assets/index-CYaJxtoL.js -country CN
supercdnctl route-explain -site cyberstream -path /assets/index-CYaJxtoL.js -country US
curl.exe -sS -D - -o NUL https://cyberstream.sites.qwk.ccwu.cc/
curl.exe -sS -D - -o NUL https://cyberstream.sites.qwk.ccwu.cc/assets/index-CYaJxtoL.js
```

## Known Boundaries

- Enabling Web resource failover makes matching static resources Worker-proxied. This is more resilient than a plain redirect, but it shifts resource traffic through Cloudflare Worker.
- Smart-routing country detection depends on Cloudflare request metadata. CLI and dry-run diagnostics can simulate country headers, but real routing still follows the edge request context.
- Go-origin homepage fallback remains a last-resort, explicit compatibility switch. Static resources must fail over only between ready resource libraries.
- R2 remains supported for CDN and legacy Web paths, but new Web hosting should prefer Cloudflare entry plus explicit resource libraries such as AList/OpenList and IPFS/Pinata.
