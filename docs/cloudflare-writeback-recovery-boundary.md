# Cloudflare Writeback And Recovery Boundary

Last updated: 2026-05-15 Asia/Shanghai.

This note defines the boundary for future Cloudflare Static and `hybrid_edge` recovery commands when provider-side writes may have succeeded but Super CDN metadata, readiness verification or activation did not finish cleanly.

It complements:

- `docs/cloudflare-rollback-boundary.md`, which covers rollback write safety.
- `supercdnctl reconcile-deployment`, which is read-only and only compares recorded metadata with live provider behavior.
- `deploy-site` failure reports with `verify_failed_after_provider_write`.

## Current Decision

Do not add an automatic provider writeback command until the command can prove which state already moved, which state is still missing, and whether activating Super CDN metadata would match the real live domain.

Current supported recovery behavior:

- `hybrid_edge` readiness timeouts after deployment creation can be inspected with `reconcile-deployment` because a Super CDN deployment id already exists.
- `refresh-edge-manifest` can republish the active hybrid edge manifest when signatures or manifest contents need repair.
- `recover-cloudflare-static` can dry-run validate the evidence for unrecorded `cloudflare_static` provider writes: source summary, Worker/version/domain evidence and strict live probe. It intentionally refuses real writes until the server-side recovery endpoint and audit behavior exist.
- `cloudflare_static` readiness timeouts before metadata recording still require rerunning `deploy-site` or a future recovery write. A provider write alone is not enough evidence to create or activate Super CDN metadata.

## Failure Shapes

### Recorded Hybrid Deployment

The `hybrid_edge` flow creates and waits for a Super CDN deployment, publishes the edge manifest, publishes Worker assets, then verifies Cloudflare custom-domain traffic.

If verification times out after those writes, the failure output includes a deployment id. Recovery starts with:

```powershell
.\bin\supercdnctl.exe reconcile-deployment -site <site> -deployment <deployment> -max-assets 20
```

If the report is `settled=true`, the operator can treat the deployment as provider-verified. If it is not settled, the safe repair actions are still explicit:

- `refresh-edge-manifest` when asset signatures or active manifest contents are stale;
- rerun `deploy-site -target hybrid_edge` with the intended artifact when Worker/domain state is wrong;
- do not use metadata-only `promote-deployment`, which is intentionally blocked.

### Unrecorded Cloudflare Static Write

The `cloudflare_static` flow publishes Worker Static Assets first and records Super CDN metadata only after readiness verification. If verification times out before recording, Cloudflare may already have Worker assets and custom-domain state, but Super CDN has no deployment id for that provider write.

A future recovery command must not infer a deployment from Wrangler success alone. It needs durable local and remote evidence:

- source artifact directory and deterministic asset summary;
- Worker name, version id when available, compatibility date and Static Assets hash;
- custom domains and the exact URL probed;
- cache/header policy and SPA/not-found handling;
- successful strict probe evidence for the live custom domain;
- explicit operator confirmation.

## Required Future Write Command Behavior

A future `cloudflare_static` recovery/writeback command should be ordered as:

1. Load the site and current deployment target defaults.
2. Summarize the source directory using the same Cloudflare Static asset summary as `deploy-site`.
3. Load or require provider evidence from the failed write report: Worker name, version id, domains, compatibility date, cache policy and not-found handling.
4. Probe the real custom domain with `probe-site`-equivalent strict checks: Cloudflare Static HTML evidence, direct same-site assets, generated cache headers when applicable, and optional SPA fallback.
5. If strict probe fails, emit a recovery report and do not write metadata.
6. If strict probe passes, record a Super CDN deployment with `verification_status=ok`, published/verified timestamps and the exact evidence.
7. Activate metadata only when the recorded deployment evidence matches the verified live domain and the user passed an explicit confirmation flag.
8. Audit the recovery write separately from normal deploys.
9. Emit `reconcile-deployment` and `rollback-plan` next commands.

The command should default to dry-run. A real write must require a confirmation token such as `-dry-run=false -confirm recover`.

## Required Server Boundary

Before adding the write path, the server needs a recovery-specific endpoint or request mode with these properties:

- audit action distinct from normal deploy, for example `site.deployment.cloudflare_static.recovery`;
- idempotency key based on site id, Worker name, version id, assets hash and domains;
- no secret fields accepted or stored;
- rejected writes are audited when evidence is incomplete, probe evidence is missing, or activation would be metadata-only;
- activation remains provider-aware and must not reuse the generic `promote-deployment` path for Cloudflare-backed deployments.

## Non-Goals

- Do not recover a Cloudflare write from an arbitrary public URL without Worker/domain evidence.
- Do not promote non-active `cloudflare_static` or `hybrid_edge` metadata through the generic `promote-deployment` endpoint.
- Do not mark a timeout as successful only because Wrangler returned success.
- Do not delete or roll back Cloudflare Worker versions, custom domains or KV entries as part of this recovery boundary.

## Test And Canary Requirements

Before treating this mature, cover these cases:

- unit test: dry-run refuses missing source dir, Worker name, domain or strict probe evidence;
- unit test: successful recovery writes a deployment record with Cloudflare evidence and a distinct audit action;
- unit test: activation is rejected without explicit confirmation;
- integration test: failed strict probe prints a recovery report and writes nothing;
- live canary: induce or simulate a Cloudflare Static readiness timeout, recover after propagation, then run `reconcile-deployment` and `probe-site` against the recovered deployment.
