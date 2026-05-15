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
- Successful `deploy-site -target hybrid_edge` runs now record Worker/domain/Workers KV/edge-manifest evidence back into the deployment after strict provider verification through `POST /api/v1/sites/{id}/deployments/{deployment}/hybrid-edge/evidence`, with audit action `site.deployment.hybrid_edge.evidence`. This is normal deploy evidence capture, not a generic writeback command for unknown or partially failed provider writes.
- `refresh-edge-manifest` can republish the active hybrid edge manifest when signatures or manifest contents need repair.
- `recover-cloudflare-static` can validate the evidence for unrecorded `cloudflare_static` provider writes: source summary, Worker/version/domain evidence and strict live probe. With `-dry-run=false -confirm recover`, it records a non-active Super CDN deployment through a recovery-specific server endpoint and audit action.
- `activate-cloudflare-static` can activate a recovered `cloudflare_static` deployment only after loading recorded deployment evidence, matching it against the local source summary, running a strict live probe, and calling a dedicated audited endpoint with `-dry-run=false -confirm activate`.
- `cloudflare_static` readiness timeouts before metadata recording no longer require losing all metadata evidence, but a provider write alone is still not enough to activate Super CDN metadata; activation requires the dedicated verified path above.
- Live recovery canary `supercdn-recovery-0515-090858.qwk.ccwu.cc` proved this boundary on 2026-05-15: `publish-cloudflare-static` wrote Worker version `89fe1670-c92a-4896-bf22-d198dc2f6fa7` without Super CDN metadata, `recover-cloudflare-static` dry-run verified strict provider evidence, the confirmed write recorded inactive deployment `dpl-diiuko109n5o`, audit logged `site.deployment.cloudflare_static.recovery`, and `reconcile-deployment` returned `status=ok` / `settled=true`.
- The same live canary then proved provider-aware activation: commit `bc08ede` was deployed to production, `activate-cloudflare-static` dry-run verified the recorded source/provider evidence and live Cloudflare Static traffic, `-dry-run=false -confirm activate` made `dpl-diiuko109n5o` active, audit logged `site.deployment.cloudflare_static.activate`, and `reconcile-deployment` still returned `status=ok` / `settled=true`.
- Hybrid evidence canary `supercdn-hybrid-evidence-0515-1054.qwk.ccwu.cc` proved the normal deploy evidence path after commit `fa7b469` was deployed to production: deployment `dpl-diiwtv7u89vf` recorded Worker/KV/manifest evidence, `rollback-plan` surfaced the `hybrid_edge` evidence block, `reconcile-deployment` returned `status=ok` / `settled=true`, and audit logged `site.deployment.hybrid_edge.evidence`.
- Hybrid rollback canary `supercdn-hybrid-rollback-0515-113402.qwk.ccwu.cc` proved the provider-aware rollback path after commit `f1e9b43` was deployed to production: A deployment `dpl-diixrvg4csvl` had complete Worker/KV/manifest evidence, B deployment `dpl-diixsyguegs8` changed entry HTML, `rollback-apply` created active deployment `dpl-diixtnpcko01`, `reconcile-deployment` returned `status=ok` / `settled=true`, audit logged `site.deployment.hybrid_edge.rollback`, and the B marker disappeared from live HTML after rollback.

## Failure Shapes

### Recorded Hybrid Deployment

The `hybrid_edge` flow creates and waits for a Super CDN deployment, publishes the edge manifest, publishes Worker assets, verifies Cloudflare custom-domain traffic, then records provider evidence into the deployment manifest.

If verification times out after those writes, the failure output includes a deployment id. Recovery starts with:

```powershell
.\bin\supercdnctl.exe reconcile-deployment -site <site> -deployment <deployment> -max-assets 20
```

If the report is `settled=true`, the operator can treat the deployment as provider-verified; on successful deployments the metadata should also expose a `hybrid_edge` evidence block with Worker/KV/manifest fields. If it is not settled, the safe repair actions are still explicit:

- `refresh-edge-manifest` when asset signatures or active manifest contents are stale;
- rerun `deploy-site -target hybrid_edge` with the intended artifact when Worker/domain state is wrong;
- do not use metadata-only `promote-deployment`, which is intentionally blocked.

### Unrecorded Cloudflare Static Write

The `cloudflare_static` flow publishes Worker Static Assets first and records Super CDN metadata only after readiness verification. If verification times out before recording, Cloudflare may already have Worker assets and custom-domain state, but Super CDN has no deployment id for that provider write.

The recovery command must not infer a deployment from Wrangler success alone. It needs durable local and remote evidence:

- source artifact directory and deterministic asset summary;
- Worker name, version id when available, compatibility date and Static Assets hash;
- custom domains and the exact URL probed;
- cache/header policy and SPA/not-found handling;
- successful strict probe evidence for the live custom domain;
- explicit operator confirmation.

## Required Future Write Command Behavior

The `cloudflare_static` recovery/writeback command is ordered as:

1. Load the site and current deployment target defaults.
2. Summarize the source directory using the same Cloudflare Static asset summary as `deploy-site`.
3. Load or require provider evidence from the failed write report: Worker name, version id, domains, compatibility date, cache policy and not-found handling.
4. Probe the real custom domain with `probe-site`-equivalent strict checks: Cloudflare Static HTML evidence, direct same-site assets, generated cache headers when applicable, and optional SPA fallback.
5. If strict probe fails, emit a recovery report and do not write metadata.
6. If strict probe passes, record a Super CDN deployment with `verification_status=ok`, published/verified timestamps and the exact evidence.
7. Record the recovered deployment as non-active; activation is a separate `activate-cloudflare-static` step and must not use generic `promote-deployment`.
8. Audit the recovery write separately from normal deploys.
9. Emit `reconcile-deployment` and `rollback-plan` next commands.

The command should default to dry-run. A real write must require a confirmation token such as `-dry-run=false -confirm recover`.

## Required Server Boundary

The recovery-specific endpoint must keep these properties:

- audit action distinct from normal deploy, for example `site.deployment.cloudflare_static.recovery`;
- idempotency key based on site id, Worker name, version id, assets hash and domains;
- no secret fields accepted or stored;
- rejected writes are audited when evidence is incomplete, probe evidence is missing, or activation would be metadata-only;
- activation remains unsupported on the recovery endpoint itself; the dedicated provider-aware activation path must not reuse the generic `promote-deployment` path for Cloudflare-backed deployments.

## Non-Goals

- Do not recover a Cloudflare write from an arbitrary public URL without Worker/domain evidence.
- Do not promote non-active `cloudflare_static` or `hybrid_edge` metadata through the generic `promote-deployment` endpoint.
- Do not mark a timeout as successful only because Wrangler returned success.
- Do not delete or roll back Cloudflare Worker versions, custom domains or KV entries as part of this recovery boundary.

## Test And Canary Requirements

Before extending this into Cloudflare rollback writes or hybrid writeback, keep these cases covered:

- unit test: dry-run refuses missing source dir, Worker name, domain or strict probe evidence;
- unit test: successful recovery writes a deployment record with Cloudflare evidence and a distinct audit action;
- unit test: activation is rejected without explicit confirmation or when source/provider evidence does not match;
- unit test: successful activation uses a dedicated endpoint and writes a distinct audit action;
- integration test: failed strict probe prints a recovery report and writes nothing;
- live canary: simulate an unrecorded Cloudflare Static provider write, recover after propagation, activate only after strict evidence validation, then run `reconcile-deployment` and `probe-site` against the activated deployment. This is currently covered by `supercdn-recovery-0515-090858` / `dpl-diiuko109n5o`.
