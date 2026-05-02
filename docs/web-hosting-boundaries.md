# Web Hosting Boundary

Last updated: 2026-05-02 Asia/Shanghai.

This document records the accepted product boundary for Super CDN Web hosting. It is the reference when implementation, docs, tests or config examples disagree.

## Hosting Modes

Super CDN Web hosting has two supported shapes.

1. Go entry mode.

   The Go service serves the homepage or preview entry HTML, and can redirect non-entry files to configured storage when a direct URL exists. This mode is for simple integration tests, early compatibility checks and diagnostics. It is not the preferred production Web hosting shape.

2. Cloudflare entry mode.

   Cloudflare owns the homepage and SPA entry path through Workers Static Assets or Pages. Non-entry resources are hosted on explicit resource libraries: AList/OpenList, Cloudflare-native static assets, or IPFS/Pinata. This is the preferred Web hosting shape.

Cloudflare R2 is not a mainstream Web hosting target. R2 remains maintained as the overseas CDN/object acceleration line for large reusable files, media, archives, images and downloads. Existing R2-backed site flows can stay for old deployments and diagnostics, but they should be treated as legacy Web hosting support and not expanded as the main path.

## Resource Ownership

- Go service: control plane, deploy intake, bundle inspection, metadata, manifest generation, provider health checks, storage synchronization, backup uploads and rollback records.
- Cloudflare Static/Pages: preferred homepage and SPA entry delivery.
- AList/OpenList: domestic resource libraries.
- IPFS/Pinata: durable CID-addressed assets and optional Web resource library.
- R2: CDN/object acceleration, not the primary Web hosting surface.

Public Web delivery should not depend on the Go origin in the preferred Cloudflare entry mode.

## Smart Routing And Failover

Smart routing and failover both require multiple resource libraries. The minimum is two ready sources. Super CDN must upload or replicate the same object to the primary plus configured backups before those sources can participate in routing.

Failover is off by default. It is enabled only when the user explicitly asks for it.

Backups are storage copies, not automatic Web delivery fallbacks. Without an explicit routing/failover policy, Web resource routes use the primary resource library only.

The explicit Web resource failover switch is `resource_failover` / `deploy-site -resource-failover`. It requires a route profile with primary plus backup targets. In the edge path, failover routes are proxied by the Worker across ordered manifest candidates. This is intentionally heavier than a redirect, so it stays opt-in.

When a Cloudflare-entry HTTPS page points a manifest resource at an HTTP-only AList/OpenList `/d` URL, the Worker proxies that resource same-origin and marks it with `X-SuperCDN-Edge-Source: storage` plus `X-SuperCDN-Edge-Proxy: mixed_content`. This avoids browser mixed-content blocking without falling back to the Go origin. HTTPS storage locators can still use normal manifest redirects.

Before a `hybrid_edge` deployment with a routing policy or resource failover writes the active Worker KV manifest, the CLI should confirm that every non-entry resource route has at least two ready candidates. This keeps async backup replication from publishing a single-source route as if smart routing or failover were already available.

Ready means the object replica is present, the route can resolve a direct locator or gateway URL, and resource-library targets do not have a recent failed health record inside `limits.resource_health_min_interval_seconds`. If a resource-library target is recently failed, manifest generation skips it and records a warning.

This publication guard is stricter than runtime recovery. After a deployment is already active, `refresh-edge-manifest` may republish a degraded single-source route when health filtering removes one candidate and one healthy candidate remains. That is an operational recovery state, not a new smart-routing success state; new routing-policy/failover deployments still wait for at least two ready candidates before active KV publication.

The explicit homepage compatibility switch is `deploy-site -entry-origin-fallback` / `EDGE_ENTRY_ORIGIN_FALLBACK=true`. It is only for `hybrid_edge` entry HTML/SPAs after Cloudflare entry delivery fails. Responses must carry warning headers and `Cache-Control: no-store`, and this switch must not affect JS/CSS/images or other static resources.

There are two different failover cases:

- Homepage failure: only when the user strongly requests it, and only after every homepage hosting method has failed, Super CDN may temporarily fall back to Go entry delivery. The response or CLI output must warn that this is temporary and the site should migrate back to Cloudflare entry delivery quickly.
- Static resource failure: never fall back to the Go server. Static resources can only fail over between ready resource libraries that already contain the object. If every configured resource library fails, return an error or surface the failed asset state.

`origin_assisted` can still be used intentionally for testing and compatibility, but it is not a production failover target for static resources.

## Alignment Audit

Current alignment:

- CDN/object acceleration: aligned. R2 is correctly positioned as the overseas CDN/object line, and AList/OpenList/IPFS can be storage lines.
- `cloudflare_static`: aligned. It keeps ordinary overseas static sites on Cloudflare-native hosting and does not involve R2.
- `origin_assisted`: aligned only as a test and compatibility mode. It still serves root HTML from Go and may stream assets when redirects are unavailable, so it must not be described as the preferred Web hosting runtime.
- `hybrid_edge`: aligned for the explicit split path. The preferred deployment uses Cloudflare Static `ASSETS` for entry HTML and Worker/KV manifest routes for resources. Static-resource fallback stays within resource libraries, and homepage Go fallback is a separate temporary switch.
- IPFS/Pinata: aligned for explicit Web resource routes. Production `ipfs_pinata` status, CID metadata, gateway probing, `ipfs-web-smoke`, and a live `hybrid_edge` canary have passed. IPFS can now be considered for broader smart-routing tests after multi-source policy config is added.
- Smart routing: aligned for the first live Web canary. Routing policies and resource failover are explicit opt-in, require at least two sources, only use ready replicas, and `hybrid_edge` waits for candidates before KV publication. Live manifest refresh now filters recently failed resource-library targets and can degrade an active route to the remaining healthy candidate with warnings. Remaining work is broader multi-file/provider testing and stronger operator warnings around homepage Go fallback.
- Example config: mainstream Web examples should avoid using R2 as the default non-entry Web resource path. R2 examples should be named and documented as CDN/object or legacy Web compatibility paths.

## Implementation Notes

- `cloudflare_static` is the default for ordinary overseas static sites.
- `hybrid_edge` is the preferred split model: Cloudflare entry plus manifest-routed resources on AList/OpenList, Cloudflare-native static assets or IPFS/Pinata.
- `origin_assisted` remains useful for local tests, smoke tests and low-level integration.
- R2-backed Web deployments should remain runnable for existing records, but new feature work should target Cloudflare entry plus non-R2 Web resource libraries.
- `hybrid_edge` publishes Workers with `EDGE_ORIGIN_FALLBACK=false`; turning it on is an explicit compatibility/failure action.
- `hybrid_edge` also publishes Workers with `EDGE_ENTRY_ORIGIN_FALLBACK=false` unless the operator passes `deploy-site -entry-origin-fallback`.
- `hybrid_edge` waits for ready resource candidates before KV publication when routing-policy or resource-failover behavior is enabled.
- `hybrid_edge` may proxy HTTP-only manifest resource redirects from HTTPS pages to avoid browser mixed-content blocking. This is still storage delivery, not Go-origin delivery.
- `refresh-edge-manifest` is allowed to repair active manifests when signed locators expire or health changes. It should not silently promote a failed source; recent resource-library health failures are skipped during candidate generation.
- Resource-library uploads should persist verified direct locators, especially for signed AList/OpenList paths; a provider returning upload success is not enough to mark a replica ready.
- AList/OpenList clients may force `network: "tcp4"` when a provider publishes unreachable IPv6 records. HTTP clients must keep finite dial and response-header timeouts so a dead resource library does not become a Go-origin 504.
- Any future failover switch must make the user opt in explicitly and must preserve the homepage/static-resource distinction.
