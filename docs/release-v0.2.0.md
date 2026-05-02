# Super CDN v0.2.0

Release date: 2026-05-02

Status: internal stable milestone.

This release freezes the preferred Web hosting split model: Cloudflare owns entry HTML and SPA fallback, while non-entry resources are delivered by explicit storage libraries such as AList/OpenList or IPFS/Pinata. It also promotes IPFS/Pinata from an experimental path to a tested resource-library option.

## Included

- Pinata/IPFS resource-library support with status checks, uploads, CID metadata, gateway URLs, groups, delete/list refresh and bucket/site flows.
- `ipfs-status`, `ipfs-smoke`, `ipfs-web-smoke` and `refresh-ipfs-pins` CLI coverage.
- Hybrid Web hosting with Cloudflare Static entry HTML and Worker/KV manifest routing for JS/CSS/assets.
- Preferred Web hosting boundaries documented in `docs/web-hosting-boundaries.md`.
- Explicit static-resource failover semantics: opt-in only, requires at least two ready resource libraries, and never falls back to the Go origin.
- Explicit homepage origin fallback semantics for `hybrid_edge`: opt-in, temporary, warning-marked and limited to entry HTML/SPAs.
- Health-aware resource-library candidate generation and candidate readiness gates before publishing smart-routing/failover manifests.
- AList/OpenList HTTP-only manifest resources are proxied same-origin by the Worker for HTTPS pages to avoid browser mixed-content blocking.
- Probe hardening for Cloudflare Static HTML plus manifest-routed assets, including proxied `storage`, `ipfs_gateway` and `resource_failover` sources.

## Live Canaries

- IPFS hybrid Web canary: `https://cyberstream9.qwk.ccwu.cc/`
- Domestic AList/OpenList hybrid Web canary: `https://cyberstream9-cn.qwk.ccwu.cc/`
- Smart-routing Web canary: `https://cyberstream-smart-single-0501.qwk.ccwu.cc/`

The domestic canary serves entry HTML/SPAs from Cloudflare Static and JS/CSS through Worker/KV to the `china_all` AList/OpenList line. The Worker returns those HTTP-only storage resources same-origin with `X-SuperCDN-Edge-Source: storage`, so browser delivery does not depend on CORS or Go-origin streaming.

## Verification

Local:

```powershell
go test ./...
cd worker
npm test
npx tsc --noEmit
```

Live checks performed for this milestone:

```powershell
go run .\cmd\supercdnctl -- probe-site -url https://cyberstream9-cn.qwk.ccwu.cc/ -spa-path /movie/123 -require-edge-static-html -require-edge-manifest-assets
```

Headless Chromium loaded `https://cyberstream9-cn.qwk.ccwu.cc/`, mounted `#root`, and rendered the CyberStream home page without mixed-content errors.

## Known Boundaries

- R2 remains supported for CDN/object acceleration and legacy Web compatibility, but it is not the preferred Web resource target for new hybrid Web deployments.
- Smart routing and static-resource failover still require at least two ready resource libraries; a single healthy source after manifest refresh is a degraded recovery state, not a new smart-routing success state.
- HTTP-only AList/OpenList storage resources can be proxied by Worker for HTTPS pages. This solves browser delivery but is still storage delivery, not a Go-origin fallback.
