# Cloudflare Rollback Boundary

Last updated: 2026-05-15 Asia/Shanghai.

This note records the boundary for future Cloudflare Static and `hybrid_edge` rollback write commands. It complements `rollback-plan`, which is intentionally read-only.

## Current Decision

Do not add a command that claims to roll back Cloudflare-backed live traffic until the command can prove that all real traffic state moved together. The first supported write wrapper is limited to `cloudflare_static`, where the command can verify the historical source directory against recorded Worker/domain/assets evidence and then rerun the full Cloudflare Static provider deployment flow.

Current supported behavior:

- `promote-deployment` blocks non-active `cloudflare_static` and `hybrid_edge` metadata-only rollback.
- rejected metadata rollback attempts are audited as `site.deployment.promote.rejected`.
- `rollback-plan` returns a read-only plan, version evidence when available, and explicit `write_blockers[]` / `missing_evidence[]` when a rollback write path is not ready.
- `rollback-apply` can apply `cloudflare_static` rollback when the operator provides the historical source directory and the recorded target deployment has enough Worker/domain/assets evidence. It defaults to dry-run, requires `-dry-run=false -confirm rollback` for writes, verifies source hash/file count/size before writing, then republishes Cloudflare Static assets through the same provider flow as `deploy-site`. Successful writes are audited as `site.deployment.cloudflare_static.rollback` with both the new deployment id and the rollback target id.
- `rollback-apply` does not support `hybrid_edge`; hybrid rollback still requires a future provider-aware command that couples Worker assets, active KV manifest, deployment KV manifest and live custom-domain checks.
- `reconcile-deployment` is a read-only post-timeout inspection command for comparing recorded Super CDN deployment metadata with live Cloudflare Static / hybrid-edge provider behavior.
- `delete-deployment` warns that Cloudflare Worker versions, custom domains and KV entries are not deleted.
- `refresh-edge-manifest` can republish the active hybrid edge manifest after repair or signature recovery, but it is not a deployment rollback command.

## Live Validation

Validated on 2026-05-15 Asia/Shanghai with real custom domains:

- `cloudflare_static`: `supercdn-maturity-static-0515.qwk.ccwu.cc` was deployed as A, updated to a B artifact containing a visible marker, planned back to A with `rollback-plan -site supercdn-maturity-static-0515 -deployment dpl-diirv63ptmqj -dir .\test_file\path2agi`, then restored by rerunning `deploy-site` with the A artifact. `probe-site -require-edge-static-html -require-html-revalidate -require-immutable-assets` passed after restore, and the B marker was absent.
- `hybrid_edge`: `supercdn-maturity-hybrid-ipfs-0515.qwk.ccwu.cc` was deployed as A, updated to B, planned back to A with `rollback-plan -site supercdn-maturity-hybrid-ipfs-0515 -deployment dpl-diirzx0ukg5r -dir .\test_file\cyberstream\dist`, then restored by rerunning `deploy-site -target hybrid_edge`. The active Workers KV key was rewritten during restore, and `probe-site -require-edge-static-html -require-edge-manifest-assets -spa-path /movie/123` passed with entry HTML from `cloudflare_static` and the JS route from `ipfs_gateway`.

The live run also exposed two operator risks:

- Cloudflare custom-domain propagation can outlast the CLI readiness timeout even when the provider write later succeeds.
- `repo_china_mobile` AList upload visibility failed for new hybrid deployment paths and correctly blocked activation. This specific visibility issue was later reproduced, fixed by refreshing the AList/OpenList parent directory before post-upload stat retry, deployed from commit `c2243727223d9ce9bf20a4692ff25797ec2c021e`, and revalidated by mobile hybrid canary `dpl-diit34iw5d3t`.
- Recorded post-timeout deployments can now be inspected with `reconcile-deployment`; live run on `supercdn-maturity-hybrid-0515` deployment `dpl-diit34iw5d3t` returned `settled=true` with entry HTML from `cloudflare_static` and manifest-routed JS through `repo_china_mobile`.

## Why A Metadata Rollback Is Unsafe

`cloudflare_static` traffic is controlled by the published Worker/Static Assets version and its custom domains. Changing the Super CDN deployment row does not republish those assets or change the Worker version serving the domain.

`hybrid_edge` traffic depends on multiple pieces moving together:

- Cloudflare Static Assets for entry HTML and SPA fallback;
- Worker script/configuration;
- active Workers KV edge manifest keys;
- deployment-specific manifest keys;
- route bindings/custom domains;
- resource candidate readiness and signed URLs.

If only one of those changes, the control plane can claim rollback while the domain still serves the old Worker, old assets or old manifest.

## Minimum Source Of Truth

A future write command needs a durable rollback target that includes:

- Super CDN deployment id and site id;
- deployment target;
- source artifact hash and file count;
- Cloudflare Worker name;
- Cloudflare Worker version id or deployment id when available;
- assets hash;
- bound domains;
- active KV key names and namespace id/title;
- deployment KV key names;
- edge manifest hash;
- routing policy/resource failover mode;
- verification report for the target version.

If any of these are unavailable, the command should stay read-only and tell the operator to rerun the full `deploy-site` flow with the intended artifact.

## Required Write Flow

A Cloudflare rollback command should be ordered as:

1. Read active deployment and target deployment.
2. Build a dry-run plan with exact Worker, domain and KV keys.
3. Verify the target artifact and edge manifest evidence.
4. Require explicit confirmation.
5. Publish or select the target Cloudflare asset/Worker version.
6. Publish deployment and active KV manifest keys when `hybrid_edge` is involved.
7. Verify the real custom domain with `probe-site`-equivalent checks.
8. Write a success audit event with deployment id, target, Worker evidence and KV evidence.
9. Emit a rollback plan for undoing the rollback.

## Verification Requirements

The live check must prove:

- root/SPA HTML is served by the expected Cloudflare entry path;
- JS/CSS first hops use the expected edge manifest route when `hybrid_edge` is involved;
- response headers identify the expected edge source or manifest route;
- no static resource silently falls back to the Go origin unless explicitly allowed for entry HTML;
- the active KV manifest hash matches the rollback target;
- the domain being checked is a real bound custom domain, not only a preview URL.

## Current Operator Path

For `cloudflare_static`:

- run `rollback-plan -site <site> -deployment <deployment> -dir <historical-dist>`;
- confirm `rollback_write_ready=true`;
- run `rollback-apply -site <site> -deployment <deployment> -dir <historical-dist>`;
- if dry-run returns `status=verified`, run the same command with `-dry-run=false -confirm rollback`;
- run `reconcile-deployment` or the emitted `probe-site` command against the new active deployment when a live check is needed.

For `hybrid_edge`, until a safe write command exists:

- run `rollback-plan` for the target deployment;
- confirm the target evidence;
- rerun `deploy-site -target hybrid_edge` with the intended historical artifact directory;
- run the `probe-site` command emitted by `rollback-plan` against the active production deployment after redeploy; Cloudflare Static must prove edge static HTML, and hybrid edge must also prove manifest-routed assets;
- use `audit-log` to review rejected metadata promote attempts and actual deployment writes.
