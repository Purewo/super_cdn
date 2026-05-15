# Super CDN Handoff

Last updated: 2026-05-15 Asia/Shanghai.

## Current State

The service is still in development mode. There is no need to preserve compatibility with the old static-site deployment flow.

`v0.5.0` is the current staged mature milestone. It closes the post-`v0.4.0` refactor, operations, documentation and read-only real-scenario regression cycle. Start future work from `docs/refactor-plan.md`, `docs/maturity-audit.md`, `docs/operations.md` and the relevant boundary document instead of restarting broad cleanup.

v0.1 and v0.2.0 are closed as stable internal milestones. The `v0.1.x` line remains feature-frozen, and `v0.2.x` should only receive bug fixes, documentation, operational hardening and regression coverage around IPFS/Web hosting. New product work starts from `docs/v0.3-roadmap.md`, with multi-resource-library scheduling, replica repair, smart routing and explicit static-resource failover as the next major feature surface.

Next-session priority note: continue from the post-`v0.4.0` refactor audit in [docs/refactor-plan.md](refactor-plan.md), the evidence checklist in [docs/maturity-audit.md](maturity-audit.md), the policy switching boundary in [docs/policy-switching-boundary.md](policy-switching-boundary.md), the Cloudflare rollback boundary in [docs/cloudflare-rollback-boundary.md](cloudflare-rollback-boundary.md), and the Cloudflare writeback/recovery boundary in [docs/cloudflare-writeback-recovery-boundary.md](cloudflare-writeback-recovery-boundary.md). Phase 0 through Phase 6 are now done on `main`: CI/release checklist, OpenAPI, versioned migrations, audit-event writes, CLI dispatcher cleanup and server skeleton extraction. Narrow package-boundary cleanup has started: deployment target normalization is centralized in `internal/deploymenttarget`, deployment evidence operation names are centralized in `internal/deploymentevidence`, Go-side edge evidence/route header names are centralized in `internal/edgeheaders`, and server audit action names are centralized in `internal/server/audit.go` while tests keep literal audit contract assertions. Commits `e79d694`, `2e6153c` and `b450185` passed CI runs `25900531880`, `25901652012` and `25902976766`; the two production-relevant boundary packages were deployed with backups `/opt/supercdn/backups/20260515T044024Z-deployment-evidence-ops` and `/opt/supercdn/backups/20260515T052212Z-edgeheaders`. Do not restart from Phase 0; only continue with narrow package-boundary cleanup when the boundary and tests are clear.

Web hosting boundary note: the current product rule is recorded in `docs/web-hosting-boundaries.md`. Go entry delivery is for tests/integration/compatibility; preferred Web hosting is Cloudflare entry plus non-entry resources on AList/OpenList, Cloudflare-native static assets or IPFS/Pinata. R2 remains a CDN/object acceleration line; R2-backed Web hosting is legacy compatibility and not the mainstream path. Static-resource failover must never fall back to Go.

Smart-resource failover note: Worker regressions now cover multi-file mixed-provider failover across AList-style redirects, R2-style redirects and IPFS gateway candidates without using Go-origin fallback. When `deploy-site -entry-origin-fallback` is used for `hybrid_edge`, CLI output warns that it is only temporary entry HTML/SPA fallback and not static-resource failover. Commit `b43a7d2` passed local foundation and CI run `25903360498`.

Operator maturity note: `cdn-doctor` and `site-doctor` now include `recommendations[]` so support reports say whether to run health checks, repair replicas, refresh the edge manifest, redeploy Cloudflare Static assets, or manually review line switching. `supercdnctl switch-plan` consumes those reports and produces a read-only manual switching plan with candidate counts, risks and next commands; it separates `candidate_ready` from `apply_supported` so routing-policy/resource-failover routes are not presented as directly switchable. `supercdnctl switch-apply` is the first explicit apply path: it can switch one bucket object or site deployment file to a ready `primary_target`, defaults to dry-run, requires `-confirm switch`, audits writes and rejected attempts, returns a rollback command, and refuses routing-policy/resource-failover/Cloudflare Static cases where metadata-only switching would not control real traffic. Super CDN still does not silently switch cross-line traffic for the user.

Rollback safety note: `promote-deployment` is now intentionally metadata-only and only suitable for targets where metadata actually controls traffic, such as origin-assisted deployments. Non-active `cloudflare_static` and `hybrid_edge` deployments are rejected because rollback must republish Cloudflare Worker assets and/or the active KV manifest together with Super CDN metadata, and those rejected metadata-promote attempts write `site.deployment.promote.rejected` audit events. Use `supercdnctl rollback-plan -site <site> -deployment <deployment>` before recovery; it returns either a safe metadata promote plan or a redeploy-required Cloudflare/hybrid plan, plus evidence such as artifact hash, manifest key, Worker name, version id, assets hash, domains and verification status when available. `supercdnctl rollback-apply` now supports Cloudflare Static and hybrid_edge rollback when `rollback-plan` has complete evidence and the operator provides the matching historical source directory; hybrid writes rerun the full Worker/KV/domain verification flow and are audited as `site.deployment.hybrid_edge.rollback`.

API contract note: the diagnostic and operator result surfaces are now explicitly described in OpenAPI. `cdn-doctor`, `site-doctor`, `route-explain`, asset-bucket init/delete/replica-refresh results and site delete results no longer rely on generic `AnyObject` schemas for their primary response bodies.

Current maturity snapshot: local foundation is green, including Windows `go test -race ./...` through `foundation-check.ps1 -SkipLinuxBuild -Race` with portable WinLibs GCC under `E:\Tools\winlibs-x86_64-posix-seh-gcc-16.1.0-mingw-w64ucrt-14.0.0-r1\mingw64\bin`, and recent pushed CI runs, including GitHub Actions runs `25898412913`, `25899747542`, `25901060286`, `25901652012`, `25902701652`, `25902976766` and `25903360498`, were green for Go test, Ubuntu race, vet, vulnerability scan, command builds, OpenAPI lint, Worker tests, TypeScript check and Worker audit. Live Cloudflare rollback-path validation has now been run on canary domains: `supercdn-maturity-static-0515.qwk.ccwu.cc` proved Cloudflare Static A -> B -> redeploy A with strict probe and evidence headers, `supercdn-rollback-apply-0515-100952.qwk.ccwu.cc` proved Cloudflare Static `rollback-apply` A -> B -> rollback A with a dedicated audit event, `supercdn-maturity-hybrid-ipfs-0515.qwk.ccwu.cc` proved hybrid_edge A -> B -> redeploy A with active KV manifest writes and strict manifest-route probing, and `supercdn-hybrid-rollback-0515-113402.qwk.ccwu.cc` proved hybrid_edge `rollback-apply` A -> B -> rollback A with `site.deployment.hybrid_edge.rollback` audit. The earlier `repo_china_mobile` write-after-read visibility failure was reproduced on hybrid deployments, fixed by refreshing the AList/OpenList parent directory before visibility retry, deployed to production from commit `c2243727223d9ce9bf20a4692ff25797ec2c021e`, and revalidated with live deployment `dpl-diit34iw5d3t` on `supercdn-maturity-hybrid-0515.qwk.ccwu.cc`. Cloudflare provider writes remain eventually consistent with CLI readiness timeouts, but the operator repair surface is now explicit and evidence-based: `deploy-site` verify failures print provider write evidence plus retry/probe commands; `reconcile-deployment` compares recorded Super CDN metadata with live provider behavior after a timeout; `recover-cloudflare-static` records verified unrecorded Cloudflare Static writes as non-active deployments; `activate-cloudflare-static` is the dedicated verified activation path; `rollback-apply` covers Cloudflare Static and hybrid_edge source-verified rollback; and `recover-hybrid-edge` records verified post-timeout hybrid evidence on existing deployments. Commits `249ebdc`, `bc08ede`, `9c2b369`, `f1e9b43`, `cdd348c` and `1048197` deployed the recovery/activation/rollback/writeback/probe paths to production with backups `/opt/supercdn/backups/20260515T010302Z-recovery-endpoint`, `/opt/supercdn/backups/20260515T013045Z-cf-static-activation`, `/opt/supercdn/backups/20260515T020403Z-rollback-apply`, `/opt/supercdn/backups/20260515T032447Z-hybrid-edge-rollback-apply`, `/opt/supercdn/backups/20260515T041424Z-hybrid-edge-writeback-recovery` and `/opt/supercdn/backups/20260515T045806Z-browser-render-probe`; package-boundary commit `2e6153c` deployed `internal/edgeheaders` with backup `/opt/supercdn/backups/20260515T052212Z-edgeheaders`; live canaries `supercdn-recovery-0515-090858.qwk.ccwu.cc` and `supercdn-hybrid-writeback-0515-122312.qwk.ccwu.cc` proved the recovery/writeback paths, and `probe-site -browser-render` on the writeback canary returned `browser.ok=true`; `rollback-plan` emits `rollback-apply` for Cloudflare Static and hybrid_edge when evidence is complete and post-redeploy `probe-site` commands for Cloudflare-backed recovery; and `delete-deployment -dry-run` / API `dry_run=true` expose Cloudflare remote cleanup blockers instead of implying provider resources are removed.

Hybrid-edge evidence note: `deploy-site -target hybrid_edge` records Worker/domain/KV/edge-manifest evidence through `POST /api/v1/sites/{id}/deployments/{deployment}/hybrid-edge/evidence` after strict verification; `rollback-plan`, `reconcile-deployment`, `deployment` and delete dry-runs expose `hybrid_edge` evidence and missing `hybrid_edge.*` fields. Commit `fa7b469` passed CI run `25897481088`, was deployed with backup `/opt/supercdn/backups/20260515T025304Z-hybrid-edge-evidence`, and live canary `supercdn-hybrid-evidence-0515-1054.qwk.ccwu.cc` deployment `dpl-diiwtv7u89vf` recorded evidence plus audit action `site.deployment.hybrid_edge.evidence`. Commit `f1e9b43` adds provider-aware `rollback-apply` for hybrid_edge and live canary `supercdn-hybrid-rollback-0515-113402.qwk.ccwu.cc` proved A `dpl-diixrvg4csvl` -> B `dpl-diixsyguegs8` -> rollback `dpl-diixtnpcko01` with `reconcile-deployment status=ok settled=true`, audit event `site.deployment.hybrid_edge.rollback` id 29 and B marker removed from live HTML. Commit `cdd348c` adds `recover-hybrid-edge`; live canary `supercdn-hybrid-writeback-0515-122312.qwk.ccwu.cc` created deployment `dpl-diiyocxeby02` with provider state active but no evidence, dry-run verified after propagation, confirmed writeback recorded `hybrid_edge` evidence, `reconcile-deployment status=ok settled=true`, and audit action `site.deployment.hybrid_edge.writeback` was present.

Release gate note: CI and `scripts/foundation-check.ps1` include PowerShell script syntax validation, `govulncheck`, GitHub Actions workflow linting, Redocly OpenAPI lint and Worker dependency audit as first-class checks. After pushing future changes, run `.\scripts\github-actions-status.ps1 -Wait -IncludeJobs` to verify the GitHub Actions run for the current branch/head SHA and include job/step summaries on failure. CI also runs `go test -race ./...` on Ubuntu; local Windows race coverage is now available through `.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race` by temporarily prepending `E:\Tools\winlibs-x86_64-posix-seh-gcc-16.1.0-mingw-w64ucrt-14.0.0-r1\mingw64\bin` to `PATH` and setting `CGO_ENABLED=1`. The module is pinned to Go `1.25.10` because older `go1.25.1` standard-library scans reported vulnerabilities. On this machine, use the temporary `http://127.0.0.1:10808` proxy for Go/npm downloads if direct access times out; if `proxy.golang.org` returns EOF during `govulncheck`, set `$env:GOPROXY="https://goproxy.cn,direct"` in that shell only.

Audit note: mutation audit events are now queryable through `GET /api/v1/audit-events` and `supercdnctl audit-log`. Root can query all workspaces or filter by workspace; owner/maintainer tokens are scoped to their own workspace; viewer tokens are blocked. Use `-action`, `-resource` and `-limit` to inspect deployment, bucket, switching and auth operations without exposing tokens or signed URLs.

Cloudflare live validation note: generated Cloudflare Static `_headers` now include `X-SuperCDN-Edge-Source: cloudflare_static`, and existing `_headers` are augmented in a temporary deploy directory unless they already provide that header. This made direct Cloudflare Static traffic verifiable by `probe-site -require-edge-static-html`. Live canary deployment ids to keep for audit are: static A `dpl-diirv63ptmqj`, static B `dpl-diirvxa1gg60`, static restored A `dpl-diirwc54ee9k`; hybrid IPFS A `dpl-diirzx0ukg5r`, hybrid B `dpl-diis5n6vp8vr`, hybrid restored A `dpl-diis6hanbdlq`; mobile AList failed visibility canaries `dpl-diirx3euf9xl` and `dpl-diisq5nm9f7y`; mobile AList fixed canary `dpl-diit34iw5d3t`; Cloudflare Static recovery canary `supercdn-recovery-0515-090858` deployment `dpl-diiuko109n5o`; hybrid writeback canary `supercdn-hybrid-writeback-0515-122312` deployment `dpl-diiyocxeby02`. The fixed mobile canary passed `probe-site -require-edge-static-html -require-edge-manifest-assets` with entry HTML from `cloudflare_static` and JS through the `repo_china_mobile` manifest route, and `reconcile-deployment -site supercdn-maturity-hybrid-0515 -deployment dpl-diit34iw5d3t -max-assets 10` later returned `status=ok` / `settled=true` with the same provider evidence.

GitHub operation note: for GitHub network operations such as `git push`, `git fetch`, tag push and release API calls, prefer using the local proxy at `http://127.0.0.1:10808` when direct access is slow or reset. Do not configure a permanent global, system or repository Git proxy. Use per-command temporary proxy flags instead, for example `git -c http.proxy=http://127.0.0.1:10808 -c https.proxy=http://127.0.0.1:10808 push origin main`.

## Next Session Plan: Post-v0.4.0 Refactor

The real-user onboarding hardening cycle is frozen in `v0.4.0`.

The next session should directly follow [docs/refactor-plan.md](refactor-plan.md).

Immediate execution order:

1. Start with a completion audit against [docs/refactor-plan.md](refactor-plan.md) and the current tree.
2. Keep `internal/server/server.go` as the server skeleton and `cmd/supercdnctl/main.go` as the CLI dispatcher.
3. Further refactor slices should be narrow package-boundary extractions with focused tests, not broad line-count work. The deployment-target and deployment-evidence-operation boundaries are already extracted.
4. Continue improving operator workflows around explicit user-confirmed switching, rollback and health visibility. `switch-plan` and the first safe non-policy/non-failover `switch-apply` path are done; future work should focus on policy-level apply/rollback or Cloudflare/hybrid-edge rollback only when confirmation, audit and real traffic boundaries are clear.
5. Update `api/openapi.yaml` and audit coverage in the same patch as any API mutation change.

Do not start with UI, routing redesign, or new cleanup semantics.

Refactor success criteria:

- `internal/server/server.go` becomes construction, middleware, route registration and shared server plumbing.
- `cmd/supercdnctl/main.go` becomes global flag parsing and command dispatch.
- CI enforces the current local release checks, including Go race testing, Go vulnerability scanning, OpenAPI lint and Worker audit.
- OpenAPI, versioned migrations and audit-event wiring stay aligned with future code changes.

## Previous Cycle: Real User Onboarding Hardening

Baseline:

- Latest GitHub release: `v0.4.0`.
- Stable CDN/Web surface is closed enough for real user integration.
- Do not start a large new product module before the onboarding and diagnostics gaps below are handled.
- `v0.4.0` freezes the current onboarding, diagnostics, cleanup and guardrail surface before the next larger refactor.

Primary goal:

Make the first real-user connection path predictable: users should be able to create a bucket, upload many files, publish a site, understand which resource line is being used, and send useful diagnostics when something fails.

Priority order:

1. Harden `upload-bucket-dir`.
2. Add garbage data cleanup for interrupted uploads and stale temporary data.
3. Add first-pass diagnostics commands.
4. Improve onboarding docs and command output.
5. Tighten Web hosting probes around `hybrid_edge + resource_failover`.
6. Release as a patch unless diagnostics becomes a broader new surface.

### 1. `upload-bucket-dir` Patch Hardening

Current state:

- Implemented in `v0.3.1`.
- Recursively uploads a directory.
- Preserves relative paths under `-prefix`.
- Uses default `-concurrency 10`.
- Outputs a JSON per-file report.
- Reuses the single-file bucket API.
- Current branch adds `-dry-run`, `-report-file`, `-retry` and `-skip-existing` for the first real-user hardening pass.

Implemented hardening:

- `-dry-run` prints the upload plan without sending files.
- `-report-file <path>` persists the JSON report for support/debugging.
- `-retry <n>` retries transient per-file upload or warmup failures.
- `-skip-existing` skips files when the bucket already has the target logical path.
- `-fail-fast` remains intentionally deferred; default behavior continues the whole batch and reports all failures.

Acceptance criteria:

- Dry run prints the exact logical paths and sizes.
- Report file is written for both successful and partially failed batches.
- Retry is per file and visible in the result report.
- Skip-existing does not upload matching logical paths already returned by `list-bucket`.
- Existing `upload-bucket-dir` behavior remains backward compatible.

### 2. Garbage Data Cleanup

This is a required stability feature. Interrupted uploads, failed deployments, partial remote writes and abandoned staging files can create garbage data over time. The system needs both automatic cleanup and explicit operator cleanup.

Current branch first pass:

- Added root-only `gc` / `POST /api/v1/gc`.
- CLI defaults to dry-run and requires `-dry-run=false` for deletion.
- Cleans only stale local `data/staging` files older than `-older-than` / `older_than_seconds`.
- Remote cleanup, bucket-scoped cleanup and site-scoped cleanup are represented in the request/response model but intentionally return warnings until reference-counted cleanup lands.

Scope to cover:

- Local staged upload files left behind after CLI/server interruption.
- Temporary zip artifacts generated by site deployment or CLI-side directory packaging.
- Incomplete bucket uploads where the remote object exists but DB metadata was not fully recorded.
- Failed deployment objects, manifests or artifact records that are not active and not pinned.
- Stale replica records where the remote object is already missing.
- Remote provider leftovers that are still tracked by Super CDN but no longer referenced by any active bucket object or active site deployment.
- Pinata/IPFS file records that are no longer referenced by local objects and can be safely deleted when `delete_remote=true`.

Required behavior:

- Add a manual cleanup command first, for example `gc` or `gc-objects`.
- Add targeted modes:
  - `gc -dry-run` shows what would be deleted.
  - `gc -bucket <slug>` cleans garbage related to one CDN bucket.
  - `gc -site <id>` cleans garbage related to one site.
  - `gc -older-than <duration>` limits cleanup to stale data older than a safe threshold.
  - `gc -delete-remote=false` only removes local metadata or local temp files where appropriate.
- Add automatic cleanup later as a conservative background job:
  - default enabled for local temp/staging files;
  - remote destructive cleanup should stay opt-in or require explicit config.
- Cleanup must be idempotent. Running it twice should not create errors or delete active data.
- Cleanup must never delete:
  - active site deployment files;
  - pinned deployments;
  - active bucket objects;
  - replicas still referenced by live objects;
  - Cloudflare Worker versions/custom domains unless a separate Cloudflare cleanup workflow explicitly owns that.

Safety requirements:

- Destructive remote cleanup must support `-dry-run`.
- Prefix/all cleanup style operations must require explicit `-force`.
- Results must include per-item status: `planned`, `deleted`, `kept_active`, `kept_pinned`, `not_found`, `error`.
- If any remote delete fails, keep enough DB metadata to retry later instead of hiding the residue.
- Credentials and signed URLs must not be printed in full cleanup reports.

Acceptance criteria:

- A simulated interrupted upload can leave staged or partial data, and `gc -dry-run` reports it.
- Manual GC can remove local temp garbage safely.
- Manual GC can clean unreferenced tracked remote replicas when explicitly allowed.
- Active CyberStream deployment and active CDN bucket objects are not selected by GC.
- Production smoke creates a temporary failed/abandoned object scenario, runs dry-run, then real cleanup, then confirms no active data was touched.

### 3. First-Pass Diagnostics

Add a CLI diagnostic surface before adding more product features.

Candidate commands:

- `doctor`: overall control-plane and credential sanity check.
- `cdn-doctor -bucket <slug> [-path <path>]`: bucket-specific diagnostics.
- `site-doctor -site <id>` or extend `probe-site`/`route-explain` for Web hosting diagnostics.

Current branch first pass:

- Added `doctor` / `GET /api/v1/doctor`.
- Reports auth summary, DB reachability, storage target count, route profile target checks, staging directory readiness, optional resource-library status and optional routing-policy status.
- Keeps tokens/secrets out of the report; resource-library details keep the existing root-only boundary and non-root tokens receive a warning when that section is skipped.
- Added `cdn-doctor` / `GET /api/v1/asset-buckets/{slug}/doctor`.
- `cdn-doctor -bucket <slug> -path <logical>` reports bucket/profile checks, public URL, redacted storage URL, replicas, IPFS metadata, routing candidates and selected line.
- Added `site-doctor` / `GET /api/v1/sites/{id}/doctor`.
- `site-doctor -site <id> -path <request_path>` reports active deployment, hosting target, route explanation, redacted candidates and expected edge headers.

Minimum useful checks:

- Server reachable and authenticated user/token is valid.
- Bucket or site exists and is in an expected state.
- Route profile exists and has primary/backup targets configured.
- Resource libraries report capability and health.
- For a bucket path: show public URL, redirect/storage URL, selected routing candidate, replica states, IPFS metadata when present, and recent health failures.
- For a site path: show active deployment, deployment target, edge manifest route, selected candidate, failover status, and expected edge headers.

Acceptance criteria:

- A real user can paste one command output back for support.
- Credentials and tokens are never printed.
- Diagnostics explain "what is selected now" and "why other candidates were skipped".

### 4. Onboarding Docs And CLI UX

Write a shortest-path guide for real users.

Required docs:

- "Create first CDN bucket and upload one file."
- "Batch upload a local folder."
- "Publish a static Web site with Cloudflare entry plus resource-library static assets."
- "Choose between overseas R2 CDN, domestic AList/OpenList, and IPFS/Pinata."
- "Common failure checklist."

CLI output improvements:

- After upload, clearly highlight copyable `public_url` and `cdn_url`/`storage_url`.
- After batch upload, summarize total/succeeded/failed and point to `-report-file`.
- On common failures, suggest the next diagnostic command.

Current branch first pass:

- Added [docs/onboarding.md](onboarding.md) as the shortest real-user flow for login, CDN bucket creation, one-file upload, batch upload, static Web publish, diagnostics and conservative cleanup.
- `upload-bucket` output now keeps existing server fields and adds `summary`, `copy_urls` and `next_commands`; upload failures append the matching `cdn-doctor` command.
- `upload-bucket-dir` reports now include `summary`, `report_saved_to` and `next_commands`, including retry and `cdn-doctor` hints for partial failures.

Acceptance criteria:

- A new user can follow the docs from empty bucket to accessible URL without asking for hidden context.
- Failure output names the next command to run.

### 5. Web Hosting Stability Checks

Focus on the already preferred Web model, not new hosting modes.

Targets:

- `hybrid_edge`
- Cloudflare entry HTML/SPAs
- non-entry assets through resource libraries
- `resource_failover=true`
- IPFS/Pinata plus domestic AList/OpenList candidates

Next checks:

- Extend probes to assert `X-SuperCDN-Edge-Source: resource_failover` when failover is enabled.
- Add a clear probe report for static asset MIME, cache, CORS, route target and failover source.
- Keep route explanation aligned with runtime Worker behavior for smart routing plus failover.
- Document black-screen triage: HTML, JS MIME, CORS, KV manifest, Worker version, selected resource candidate.

Acceptance criteria:

- `probe-site` can distinguish Cloudflare entry success from static resource route success.
- Failover-enabled assets are checked as same-origin Worker responses, not only as 302 redirects.
- If a site goes black for a user, the next diagnostic step is unambiguous.

### 6. Release Strategy

Patch release path:

- If the next session only adds `upload-bucket-dir` hardening, garbage cleanup, docs, and small probe fixes, release as `v0.3.2`.

Minor release path:

- If `doctor`/`cdn-doctor` or automatic remote garbage cleanup becomes a broad first-class operations surface, use `v0.4.0` because it changes the operator workflow.

Required verification before release:

```powershell
go test ./...
cd worker
npm test
npx tsc --noEmit
```

Production smoke should include:

- Create temporary CDN bucket.
- Batch upload a small directory with `upload-bucket-dir`.
- Simulate or identify cleanup-safe garbage and run GC dry-run plus real cleanup.
- Run the new diagnostic command.
- Delete the temporary bucket.
- Probe the stable CyberStream hybrid site.

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
team auth/CLI profile binary update: 2026-04-30 Asia/Shanghai, backed up under /opt/supercdn/backups/20260429T225207-0400-team-auth
team auth/CLI profile deployed binary hashes: supercdn dd82bab607b33734986582d5357c163a1af5a994bbd340179628d7ace006d68a, supercdnctl 667291190017d9f708a8cf7d66dd6d53c81e6a8950155d1c9fa929774a185565
cloudflare recovery endpoint binary update: 2026-05-15 Asia/Shanghai, commit 249ebdc, backed up under /opt/supercdn/backups/20260515T010302Z-recovery-endpoint
cloudflare recovery endpoint deployed binary hashes: supercdn a000b5218994bc96d433f532e4a2bb0ac7dd1f316cd9a14af45463b4b853f082, supercdnctl 8159ab61c2f02c10089c5238a6419a293342cbf0a4cf34a34acda243b5d6ed19
cloudflare static activation endpoint binary update: 2026-05-15 Asia/Shanghai, commit bc08ede, backed up under /opt/supercdn/backups/20260515T013045Z-cf-static-activation
cloudflare static activation endpoint deployed binary hashes: supercdn cb8a88c649d5c86f38e2e8f152a7144a5e21ef49b98b92136614e92045ae3e91, supercdnctl 4a620a3ae939368435749b56245c039b1a9b8b3164afcc6904a9d15853c03dd9
hybrid edge evidence binary update: 2026-05-15 Asia/Shanghai, commit fa7b469, backed up under /opt/supercdn/backups/20260515T025304Z-hybrid-edge-evidence
hybrid edge evidence deployed binary hashes: supercdn 8a9e2dfd3afa326e89e970bcb52ac72a5df074d779c637052dd42f9ccbdcc483, supercdnctl 5c963c4f068734bf26531f28eb757aef59f95dd4d194b1474abdf1577e2f15db
hybrid edge rollback-apply binary update: 2026-05-15 Asia/Shanghai, commit f1e9b43, backed up under /opt/supercdn/backups/20260515T032447Z-hybrid-edge-rollback-apply
hybrid edge rollback-apply deployed binary hashes: supercdn 98b87ae40d63e9ef4ecd45691306366158b262eabf77a5a4dbcd2a89e5cf62ab, supercdnctl 056d1e37c8827b3e0a0d595298492fb8f0c842c94e101389bdc96fbf01e2361d
edge evidence header constants binary update: 2026-05-15 Asia/Shanghai, commit 2e6153c, backed up under /opt/supercdn/backups/20260515T052212Z-edgeheaders
edge evidence header constants deployed binary hashes: supercdn e1cbaa2604697bb0a2ebdf855b2a800584743fc59c431f75514d7d03d7be8d35, supercdnctl 32f92aa026b99360f9d48a98b6d623e14afe59c6105970482828f1692acff1a8
```

Latest live validation:

```text
server health: https://qwk.ccwu.cc/healthz returns 200
cloudflare_static canary: path2agi-static-canary deployment dpl-di55pwokt51k returns https://path2agi-static-test.qwk.ccwu.cc/
cloudflare_static cache headers: `/` returns `Cache-Control: public, max-age=0, must-revalidate`; `/path2agi-data.js?v=escape-fix-20260428` returns `Cache-Control: public, max-age=31536000, immutable`
cyberstream cloudflare_static milestone: deployment dpl-di55wdod7eqh returns https://cyberstream-static-test.qwk.ccwu.cc/ with direct JS/CSS, immutable asset cache, SPA fallback for /movie/123, and browser screenshots in data/cyberstream-static-canary-home.png plus data/cyberstream-static-canary-spa.png
overseas default cloudflare_static milestone: deployment dpl-di5fkfplv0yg returns https://cyberstream-default-root-canary.qwk.ccwu.cc/ from a `deploy-site -profile overseas` command without `-target` or `-domains`; probe-site passed for HTML, JS/CSS, immutable asset cache, and SPA fallback /movie/123.
cloudflare_static readiness guard: `deploy-site` now defaults to `-static-verify wait`, probing each custom domain before writing the Super CDN active deployment record. The guard catches TLS/DNS/404/MIME/cache/SPA issues and supports `warn` or `none` for diagnostics. The probe uses `1.1.1.1:53` by default after a live local-DNS cache miss made a newly proxied Cloudflare custom domain look like the old wildcard origin.
cloudflare_static/hybrid_edge rollback guard: normal `promote-deployment` now rejects non-active Cloudflare Static and hybrid_edge deployments instead of doing metadata-only rollback. `delete-deployment` on Cloudflare Static or hybrid_edge returns a warning that it removes Super CDN metadata only and does not delete Worker versions/custom domains/KV entries.
hybrid IPFS canary: `cyberstream-ipfs-0501.qwk.ccwu.cc`, deployment `dpl-di6slq3zv3ja`, serves entry HTML and SPA fallback from Cloudflare Static and JS through Worker/KV to Pinata/IPFS gateway. `probe-site -require-edge-static-html -require-edge-manifest-assets` passed.
smart-routing canary: `cyberstream-smart-single-0501.qwk.ccwu.cc`, deployment `dpl-di6v613q8rne`, serves entry HTML and SPA fallback from Cloudflare Static and routes JS through a smart manifest with `repo_china_mobile` plus `ipfs_pinata` candidates. When AList health is OK, Cloudflare region routing can select the mobile AList candidate; when recent AList health is failed, refreshed manifests skip that candidate and degrade to the healthy IPFS candidate with warnings.
overseas CDN bucket smoke: `create-cdn-bucket` + `upload-bucket -warmup` created `overseas-r2-smoke-20260430001954`, defaulted to `route_profile=overseas_r2`, stored the object on `overseas_accel`, returned `public_url` https://qwk.ccwu.cc/a/overseas-r2-smoke-20260430001954/docs/readme-20260430001954.md and `cdn_url` https://overseas-accel.r2.qwk.ccwu.cc/assets/buckets/overseas-r2-smoke-20260430001954/documents/2026/04/ed/ed45ae53f5b24487025f6ba2cf106496f9401009a48547e7151499e01520f539.md. Warmup HEAD returned 200; public `/a/...` HEAD returns 302 to R2; direct R2 HEAD returns 200 with `Cache-Control: public, max-age=31536000, immutable`.
asset bucket list deadlock fix: live `GET /api/v1/asset-buckets` returns after the SQLite single-connection rows/usage query fix and reports the smoke bucket with `object_count=1`.
china_mobile line-only validation: `resource-status -library repo_china_mobile` reports `alist_mobile_primary` at `/移动资源/个人云/Super_CDN`. Passive health check passed, then write/read/delete health probe passed with list/write/read/delete latencies 1857/2038/2213/370 ms. `e2e-probe -profile china_mobile` passed with primary target `repo_china_mobile`, object id 45, upload latency 3908 ms, read latency 1976 ms, HTTP 200, and cleanup remote/db both deleted. During the first run it exposed an AList upload retry bug (`seek ... file already closed`) when token refresh was needed; fixed by preventing the HTTP client from closing the reusable file reader between auth retry attempts.
domestic mobile CDN bucket smoke: `create-domestic-cdn-bucket -slug mobile-cdn-smoke-20260430051907 -line mobile` created a bucket with `route_profile=china_mobile` and `default_cache_control=public, max-age=86400`. `upload-bucket -warmup` stored README.md on `repo_china_mobile`, object id 46, and returned stable public URL https://qwk.ccwu.cc/a/mobile-cdn-smoke-20260430051907/docs/readme-20260430051907.md plus signed AList storage URL. Public `/a/...` HEAD returned 200 with the bucket cache header, warmup returned 200, and direct signed storage Range GET followed redirects to 206 with the expected file prefix. Direct storage HEAD can return 403 because the downstream drive rejects HEAD; GET/Range GET is the meaningful validation for that path.
domestic mobile origin-assisted website smoke: first `deploy-site -site path2agi-mobile-go -dir test_file/path2agi -profile china_mobile -target origin_assisted -env production -promote` exposed an AList parent-directory gap because the new deployment path did not exist yet. AList uploads now create missing parent directories before `PUT`. The second deployment `dpl-di5ypwr3ukab` is active at https://qwk.ccwu.cc/s/path2agi-mobile-go/ with `route_profile=china_mobile`, `deployment_target=origin_assisted`, `delivery_summary={origin:1, redirect:1}`. Root HTML HEAD returns 200 from Go with `Cache-Control: public, max-age=300`; `path2agi-data.js` HEAD returns 302 with `X-Supercdn-Redirect: storage` to the signed mobile AList path; Range GET follows the mobile chain to 206. `probe-site -url https://qwk.ccwu.cc/s/path2agi-mobile-go/ -max-assets 5` passed with one redirected script asset and final 200.
cyberstream mobile origin-assisted website smoke: `test_file/cyberstream` latest source needed local TypeScript compatibility fixes and removal of a stale `/index.css` reference before Vite could build cleanly. Site `cyberstream-mobile-go` deployment `dpl-di61osrsjdz2` is active at https://cyberstream-mobile-go.sites.qwk.ccwu.cc/ with `route_profile=china_mobile`, `deployment_target=origin_assisted`, and `delivery_summary={origin:1, redirect:2}`. Root HTML returns 200 from Go origin; main JS returns 302 with `X-Supercdn-Redirect: storage` to the signed mobile AList path; Range GET returns 206; SPA fallback `/movie/123` returns 200 HTML. `probe-site -url https://cyberstream-mobile-go.sites.qwk.ccwu.cc/ -spa-path /movie/123 -max-assets 10` passed. Headless Chrome screenshots are in `data/cyberstream-mobile-go-home.png` and `data/cyberstream-mobile-go-mobile.png`; desktop renders, mobile renders but the hero title is oversized and clips horizontally.
team auth/CLI profile smoke: production `healthz` returned ok after the binary update. Root `auth/me` returned root owner; a short-lived viewer invite was created and accepted, viewer `auth/me` returned the smoke user, viewer `POST /sites` was rejected with 403, and the smoke token was revoked. `cloudflare-status -all` still reports both configured accounts with token/zone/R2 ok. `probe-site -url https://cyberstream-mobile-go.sites.qwk.ccwu.cc/ -spa-path /movie/123 -max-assets 10` passed with summary `{html_ok:1, spa_ok:1, assets_found:1, assets_ok:1, assets_redirected:1}`.
cloudflare recovery endpoint smoke: production `https://qwk.ccwu.cc/healthz` returned 200 after commit `249ebdc` deployment. Authenticated `GET /api/v1/sites/supercdn-maturity-static-0515/cloudflare-static/recoveries` returned 405, confirming the new POST-only recovery route is live without creating a recovery record.
cloudflare_static recovery canary: `publish-cloudflare-static` wrote Worker `supercdn-recovery-0515-090858-static` and domain `supercdn-recovery-0515-090858.qwk.ccwu.cc` with Worker version `89fe1670-c92a-4896-bf22-d198dc2f6fa7`, without creating Super CDN metadata. `recover-cloudflare-static` dry-run verified strict Cloudflare Static HTML, direct same-site JS, revalidate HTML cache and immutable asset cache; `-dry-run=false -confirm recover` recorded deployment `dpl-diiuko109n5o` as `ready` and `active=false`. `audit-log -action site.deployment.cloudflare_static.recovery -resource site:supercdn-recovery-0515-090858` returned the recovery event, and `reconcile-deployment -site supercdn-recovery-0515-090858 -deployment dpl-diiuko109n5o -max-assets 10` returned `status=ok` / `settled=true`.
cloudflare_static activation canary: after commit `bc08ede` production deployment, `activate-cloudflare-static -site supercdn-recovery-0515-090858 -deployment dpl-diiuko109n5o -dir .\test_file\path2agi` dry-run verified the source hash `50c73ab4056c2e980808be0fa6d78b2fa31b594b8a9235c1be41587e7f933302` and live Cloudflare Static probe. `-dry-run=false -confirm activate` made `dpl-diiuko109n5o` `active=true` with production URL `https://supercdn-recovery-0515-090858.qwk.ccwu.cc/`; `audit-log -action site.deployment.cloudflare_static.activate -resource site:supercdn-recovery-0515-090858` returned the activation event, and `reconcile-deployment -site supercdn-recovery-0515-090858 -deployment dpl-diiuko109n5o -max-assets 10` returned `status=ok` / `settled=true`.
cloudflare_static rollback-apply canary: after commit `9c2b369` production deployment, disposable site `supercdn-rollback-apply-0515-100952.qwk.ccwu.cc` deployed A `dpl-diivyw5assi5`, deployed B `dpl-diivzds9sjwh`, planned A rollback with `rollback_write_ready=true`, dry-run verified, and `rollback-apply -dry-run=false -confirm rollback` created active deployment `dpl-diivzj76uo7b`. `reconcile-deployment` returned `status=ok` / `settled=true`, audit action `site.deployment.cloudflare_static.rollback` was present with target `dpl-diivyw5assi5`, and the B marker was absent from live HTML.
hybrid_edge provider evidence slice: normal hybrid deploys now persist Worker/domain/KV/edge-manifest evidence into deployment manifests after strict provider verification. The server route is `POST /api/v1/sites/{id}/deployments/{deployment}/hybrid-edge/evidence`, audited as `site.deployment.hybrid_edge.evidence`; CLI `rollback-plan`, `reconcile-deployment`, `deployment`, delete dry-run and OpenAPI now expose the `hybrid_edge` evidence block. Live canary `supercdn-hybrid-evidence-0515-1054.qwk.ccwu.cc` deployment `dpl-diiwtv7u89vf` passed strict probe, `rollback-plan` surfaced `hybrid_edge` evidence, `reconcile-deployment` returned `status=ok` / `settled=true`, and the audit event was present. Hybrid rollback apply now also has production canary coverage through `supercdn-hybrid-rollback-0515-113402.qwk.ccwu.cc`.
hybrid_edge rollback-apply canary: after commit `f1e9b43` production deployment, disposable site `supercdn-hybrid-rollback-0515-113402.qwk.ccwu.cc` deployed A `dpl-diixrvg4csvl`, deployed B `dpl-diixsyguegs8`, planned A rollback with `rollback_write_ready=true`, dry-run verified, and `rollback-apply -dry-run=false -confirm rollback` created active deployment `dpl-diixtnpcko01`. `reconcile-deployment` returned `status=ok` / `settled=true`, audit action `site.deployment.hybrid_edge.rollback` event id 29 was present with target `dpl-diixrvg4csvl`, and the B marker was absent from live HTML after rollback. First A attempt `dpl-diixmub50y9h` hit Cloudflare custom-domain readiness propagation and did not record hybrid evidence; rerunning A after propagation recorded complete evidence and became the rollback target.
legacy R2 site probe: cyberstream still passes HTML, JS/CSS redirect MIME/CORS, and /movie/123 SPA fallback checks
```

Latest local validation:

```text
2026-04-30 Asia/Shanghai closeout check: `go test ./...` and `go vet ./...` pass. A quick grep for common leaked-secret patterns only found placeholders, tests and documentation examples.

domestic all-line local CDN smoke: local service was started from `configs/config.local.json`, then stopped after the test. `repo_china_all` write/read/delete health probe passed with status ok and list/write/read/delete latencies around 496/2578/1293/1835 ms. `create-domestic-cdn-bucket -line all` created a temporary bucket with `route_profile=china_all`, upload of README.md returned a CDN/storage URL, warmup passed, public URL HEAD returned 200, Range GET returned 206 with 32 bytes, and `list-bucket` returned the uploaded object metadata.

local config note: if `repo_china_all` reports `alist root path "...Super_CDN" does not exist`, check for mojibake in the ignored local `configs/config.local.json`. The expected path is `/豆包/Super_CDN`, matching `configs/config.full.example.json`.
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
- Add a live static-site probe. Done locally as `supercdnctl probe-site`: it fetches the active deployment HTML, follows redirected JS/CSS with an `Origin` header, checks MIME/CORS, verifies a configured SPA fallback path, and can optionally run `-browser-render` with local Chrome/Edge headless screenshot analysis for blank-page detection.
- Add the first zero-origin sidecar primitive. Done locally as `GET /api/v1/sites/{id}/deployments/{deployment}/edge-manifest` and `supercdnctl export-edge-manifest`; it exports exact file routes, directory index aliases, SPA fallback, 404 behavior, storage redirect locations and delivery-rule overrides without changing production traffic.
- Add Worker-side edge manifest dry-run consumption. Done locally: when `EDGE_MANIFEST_DRY_RUN=true` and `EDGE_MANIFEST` KV is bound, `?__supercdn_edge_manifest=dry-run` returns the route decision JSON from the edge manifest without fetching origin or storage.
- Add Worker-side active edge manifest routing. Done locally: `EDGE_MANIFEST_MODE=route` lets the Worker return manifest-backed storage redirects and site redirects without contacting Go, while unresolved HTML/origin routes still fall back to the origin-assisted path. `EDGE_MANIFEST_MODE=enforce` disables that fallback for the final no-Go cutover.
- Add Worker-side Cloudflare Static fallback. Done locally: when this Worker has an `ASSETS` binding and `EDGE_STATIC_ASSETS=true`, unresolved HTML/origin routes are served by Cloudflare Static Assets instead of Go, while manifest-backed resources still route first.
- Run the first true no-Go hybrid website canary. Done live with `cyberstream-hybrid-canary.qwk.ccwu.cc`: entry HTML and SPA fallback came from Cloudflare Static Assets, JS chunks were routed by KV edge manifest to the mobile AList line, browser screenshots rendered, `probe-site` passed, and a unique probe path had zero origin/Nginx log hits.
- Promote the no-Go hybrid canary flow into `deploy-site -target hybrid_edge`. Done locally and live-tested with `cyberstream-hybrid-canary`: the CLI now uploads the storage deployment, publishes the active KV edge manifest, deploys the Worker with `ASSETS` and `run_worker_first`, verifies HTML/SPA/redirected assets, and confirmed a unique probe path had zero origin/Nginx log hits.
- Add first-class no-Go status assertions to `probe-site`. Done locally and live-probed on `cyberstream-hybrid-canary`: probe reports SuperCDN edge headers for HTML and asset redirect chains, and `hybrid_edge` deploy verification now requires Cloudflare Static HTML plus manifest-routed JS/CSS first hops.
- Deploy the IPFS/Pinata service path to production. Done live: production server/CLI were rebuilt and deployed, `ipfs_pinata` was added through the production config with `PINATA_JWT` supplied by a systemd EnvironmentFile, and `ipfs-status -target ipfs_pinata` reports Pinata v3 authentication and gateway reachability as OK.
- Run the formal IPFS Web-side smoke. Done live with `cyberstream-ipfs-smoke`: `ipfs-web-smoke` uploaded the CyberStream JS asset to Pinata, exported an edge manifest with `type: ipfs`, probed the Super CDN preview hop, and verified HEAD/Range/GET against the Pinata gateway.
- Run a preferred hybrid IPFS website canary. Done live with `cyberstream-ipfs-0501.qwk.ccwu.cc`, deployment `dpl-di6slq3zv3ja`: entry HTML and SPA fallback are served by Cloudflare Static, JS is served same-origin by the Worker through `X-SuperCDN-Edge-Source: ipfs_gateway`, and `probe-site -require-edge-static-html -require-edge-manifest-assets` passes.
- Update `probe-site` for proxied manifest resources. Done locally and on production CLI: Worker-proxied IPFS routes return same-origin `200` with `X-SuperCDN-Edge-Manifest: route`, not a `302`, so the probe now accepts `ipfs_gateway`, `resource_failover` and `storage` sources as manifest-routed first hops when the route headers are present.
- Add AList/OpenList signed-route recovery. Done locally as `supercdnctl refresh-edge-manifest`: it republishes the active edge manifest to Workers KV, probes the hybrid edge path, redacts signed query values in the embedded probe report, and marks likely expired storage signatures as `signature_suspect`.
- Gate `hybrid_edge` KV publication on resource-candidate readiness. Done locally: `deploy-site -target hybrid_edge` now defaults to `-edge-candidate-wait=true` when `-routing-policy` or `-resource-failover` is enabled, repeatedly exports the edge manifest, and requires at least two ready candidates per non-entry resource route before writing the active Workers KV manifest.
- Harden AList-backed resource-library readiness. Done locally and deployed to production: resource-library writes now verify post-upload `Stat` and store the verified signed locator, while backup replication waits longer for source visibility before marking a target failed. This prevents AList's delayed visibility from being recorded as a ready-but-undownloadable replica.
- Run the first live smart-routing Web canary. Done live with `cyberstream-smart-single-0501.qwk.ccwu.cc`, deployment `dpl-di6v613q8rne`: Cloudflare Static serves entry HTML and SPA fallback, the JS route is a smart manifest route with ready `repo_china_mobile` and `ipfs_pinata` candidates, and `probe-site -require-edge-static-html -require-edge-manifest-assets` passes. Current local Cloudflare region selection picked the mobile AList candidate; the IPFS candidate is present in the active manifest for overseas selection.
- Harden AList/OpenList networking and health-aware candidate generation. Done locally and deployed to production: AList/OpenList mount points support `network: "tcp4"`/`"tcp6"` for broken dual-stack hosts, HTTP clients use finite dial/header timeouts, and routing-policy/resource-failover manifest generation skips resource-library candidates with recent failed health. Active manifest refresh can degrade to one healthy candidate with warnings, while new `hybrid_edge` routing/failover deployments still wait for two ready candidates before active KV publication.
- Record the AList small-chunk follow-up. The original multi-chunk CyberStream build exposed a provider/replication edge case where the 578B runtime chunk failed to become visible on the mobile AList backup path, while the large JS candidate succeeded. The canary was completed with a single-chunk CyberStream build; follow-up should isolate whether this is AList path/content behavior or retry policy before declaring multi-chunk AList backup fully closed.
- Harden probe output for operational logs. Done locally: `probe-site` and deployment readiness reports now redact query values for signed storage URLs by default, with `-redact-urls=false` left for local low-level debugging only.
- Add control-plane KV publication. Done locally as `supercdnctl publish-edge-manifest`; it plans or writes deployment and active manifest keys to Cloudflare Workers KV, defaulting to dry-run and avoiding active-key writes for non-active deployments.
- Add a deployment target model before pushing deeper into custom R2/KV routing. Done locally: sites, deployments, deployment manifests and edge manifests now carry `deployment_target`; route profiles can set the default. First-class targets are `cloudflare_static` for Workers Static Assets/Pages, `hybrid_edge` for Cloudflare entry HTML plus Worker/KV path routing, and `origin_assisted` for the current Go-origin fallback path.
- Add the first formal Cloudflare Static deployment flow. Done locally and live-tested with path2agi: `deploy-site -target cloudflare_static` publishes Workers Static Assets, captures Worker/domain/version metadata, creates an active Super CDN deployment, and returns the HTTPS production URL. Cache-header automation is covered by the next item.
- Add Cloudflare Static cache-header automation. Done locally and live-tested with path2agi: `deploy-site -target cloudflare_static` and `publish-cloudflare-static` accept `-static-cache-policy auto|force|none`; auto respects an existing `_headers` file or injects a generated one from a temporary assets copy. Production deployment `dpl-di55pwokt51k` records `cache_policy:auto` and `headers_generated:true`.
- Add Cloudflare Static SPA fallback automation. Done locally and live-tested with CyberStream: `-static-spa` generates a temporary Wrangler config with `assets.not_found_handling = "single-page-application"`; production deployment `dpl-di55wdod7eqh` records the setting and `/movie/123` returns HTML directly from Cloudflare Static Assets.
- Run a real Cloudflare-native static hosting canary with CyberStream, then compare it with the current R2/KV canary on deployment complexity, SPA fallback, custom domain handling, cache behavior and rollback. Done as milestone canary `cyberstream-static-canary`; rollback ergonomics are now covered by `rollback-plan` / `rollback-apply` and live Cloudflare Static rollback canaries.
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
4. R2 storage hardening: multi-account topology now builds Cloudflare libraries over per-account R2 stores with per-binding path prefixes. Done locally and live-tested: configured bucket existence diagnostics, CORS policy diagnostics, public custom/r2.dev domain diagnostics, R2 health checks/init markers, multipart upload planning for large files, asset-bucket purge/warmup URL planning, dry-run/apply R2 CORS/domain sync, dry-run/apply R2 bucket provisioning from account or library selections, CORS default origin `*`, manual/imported R2 S3 credentials, optional Account API Token creation through `create-r2-credentials`, and real website object upload through `overseas_r2`.
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
