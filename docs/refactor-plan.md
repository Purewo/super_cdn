# Super CDN Refactor Plan

Last updated: 2026-05-15 Asia/Shanghai.

Baseline: `v0.4.0` is the current staged stable release. It freezes onboarding, diagnostics, cleanup and storage guardrails before the larger refactor.

## Progress On `main`

Completed after `v0.4.0`:

- Phase 0 safety net: GitHub Actions CI and `docs/release-checklist.md`.
- Phase 1/2 first extraction pass: diagnostics, GC, asset buckets, site deployments, edge manifests and several CLI command groups moved out of the largest files.
- Phase 3 API contract: `api/openapi.yaml` added and linked from `docs/cli-reference.md`.
- Phase 4 versioned migrations: `schema_migrations` added, existing additive columns converted to named migrations, and old-DB upgrade tests added.
- Phase 5 audit events: representative security and operational mutation paths write `audit_events`, with tests proving writes and secret redaction boundaries.
- Phase 5 audit query follow-up: `GET /api/v1/audit-events` and `supercdnctl audit-log` expose workspace-scoped mutation audit events with action/resource filters; viewers are blocked and non-root users stay scoped to their workspace.
- Phase 6 server extraction pass: `internal/server/server.go` is now an about 170-line server skeleton containing construction, lifecycle, HTTP entry and route registration. Auth, API access, response helpers, jobs, uploads, resource services, Cloudflare/R2 services, site-domain services, site services and shared helpers live in separate files.
- Phase 6 CLI extraction pass: `cmd/supercdnctl/main.go` is now an about 310-line dispatcher; client/config, core commands, provider/IPFS commands, Cloudflare/R2 ops, Cloudflare Static, resources, object ops, diagnostics, probes, sites, buckets, GC and helper code live in separate files.
- Phase 6 package-boundary follow-up: deployment target normalization and CLI alias handling now live in `internal/deploymenttarget`, shared by config, server and `supercdnctl`. Deployment evidence operation names (`deploy`, `rollback_apply`, `writeback`) now live in `internal/deploymentevidence`, shared by CLI provider writes and server evidence validation. Go-side edge evidence and route header names now live in `internal/edgeheaders`, shared by Cloudflare Static header generation, site probing, public redirects and doctor expected-header reporting. Server-side audit action names now live as constants in `internal/server/audit.go`, while tests keep literal assertions to protect the external audit contract. Commits `e79d694`, `2e6153c` and `b450185` passed CI runs `25900531880`, `25901652012` and `25902976766`; the two production-relevant boundary packages were deployed with backups `/opt/supercdn/backups/20260515T044024Z-deployment-evidence-ops` and `/opt/supercdn/backups/20260515T052212Z-edgeheaders`.
- Phase 6 package-boundary follow-up: Cloudflare Static option values and normalization for cache policy, not-found handling and readiness verification now live in `internal/cloudflarestatic`, shared by publish, deploy, recovery, activation and rollback CLI paths.
- Phase 6 package-boundary follow-up: diagnostic URL query redaction and signed-query detection now live in `internal/urlredact`, shared by CLI probes, server doctor responses and site probe signature-expiry detection.
- Phase 6 CLI ownership follow-up: shared operator command-hint helpers now live in `cmd/supercdnctl/command_hints.go` with focused tests for deduplication and PowerShell-safe argument quoting, instead of being owned by bucket commands.
- Operations maturity follow-up: `cdn-doctor` and `site-doctor` now emit `recommendations[]` with explicit next actions for health checks, replica repair, manifest refresh and manual line-switch review, without performing automatic traffic switching.
- Operations maturity follow-up: `supercdnctl switch-plan` now builds a read-only manual switching plan from `cdn-doctor` or `site-doctor`, reporting `safe_to_switch`, ready candidate counts, risks, recommendations and next commands.
- Operations maturity follow-up: `supercdnctl switch-apply` now provides an explicit, audited manual switch for a single bucket object or site deployment file by changing `primary_target` to a ready replica. It defaults to dry-run, requires `-confirm switch` for writes, returns a rollback command and refuses routing-policy or Cloudflare Static cases where metadata-only switching would not control real traffic. Rejected switch attempts write `.switch.rejected` audit events for after-action review.
- Rollback safety follow-up: `promote-deployment` now rejects non-active `hybrid_edge` deployments as well as `cloudflare_static`, because metadata-only promotion does not republish Worker assets or the active KV manifest. Origin-assisted deployments remain metadata-promotable. Rejected Cloudflare-backed metadata promote attempts write `site.deployment.promote.rejected` audit events for after-action review.
- Rollback planning follow-up: `supercdnctl rollback-plan` is a read-only preflight for a target deployment. It returns `metadata_promote` for ready `origin_assisted` rollbacks, and `redeploy_cloudflare_static` / `redeploy_hybrid_edge` plans when Cloudflare assets or active KV state must be republished instead of metadata-promoted. Plans include evidence such as artifact hash, manifest key, Worker name, version id, assets hash, domains and verification status when those fields are available.
- API contract follow-up: OpenAPI now models `CDNDoctorReport`, `SiteDoctorReport`, route explanation, edge routes, route candidates, asset-bucket init/delete/replica-refresh results, site delete results and `DoctorRecommendation` instead of leaving operator responses as unstructured `AnyObject` responses.
- CI/security follow-up: GitHub Actions and `scripts/foundation-check.ps1` now run `govulncheck`, GitHub Actions workflow linting, Redocly OpenAPI lint and Worker dependency audit as normal release gates. After pushing, `scripts/github-actions-status.ps1 -Wait -IncludeJobs` checks the GitHub Actions run for the current branch/head SHA and includes job/step summaries on failure. GitHub Actions also runs `go test -race ./...` on Ubuntu so race coverage is not blocked by this Windows host's C toolchain. `go.mod` is pinned to Go `1.25.10` so the vulnerability scan uses the patched standard library.
- Documentation maturity follow-up: `go test ./cmd/supercdnctl` now checks that every dispatched `supercdnctl` command in `cmd/supercdnctl/main.go` is covered by both `docs/commands.md` and the built-in `usage()` output, so operator docs cannot silently miss newly added commands. `go test ./internal/docscheck` also verifies local Markdown links in README and `docs/*.md`.
- Real-scenario regression follow-up: `scripts/real-scenario-regression.ps1` and `docs/real-scenario-regression.md` provide a read-only JSON evidence harness for existing public sites, authenticated sites/deployments and CDN buckets. It wraps `doctor`, `cdn-doctor`, `site-doctor`, `probe-site` and `reconcile-deployment` without doing provider writes, so refactor slices can collect real environment evidence before asking for mutating canary tests.

Next refactor entry point:

- Use `docs/maturity-audit.md` as the current evidence checklist before claiming the project is mature.
- Use `docs/policy-switching-boundary.md` before adding any policy-level switch apply/rollback write path.
- Use `docs/cloudflare-rollback-boundary.md` before changing Cloudflare Static or hybrid-edge rollback write behavior.
- Phase 6 large-file reduction is substantially complete. Continue only with narrow package-boundary work where a stable boundary is obvious.
- Do not move behavior again just to reduce line counts; next work should be focused tests, docs alignment, or package extraction with clear ownership.
- CLI cleanup is mostly complete for now; only revisit it for command-specific tests, help text fixes or smaller ownership tweaks.
- Current narrow package-boundary extractions: `internal/deploymenttarget`, `internal/deploymentevidence`, `internal/edgeheaders`, `internal/cloudflarestatic`, `internal/urlredact`, and server audit action constants in `internal/server/audit.go`.
- Manual switching now has a safe first apply path for non-policy, non-resource-failover primary-target cases. `switch-plan` separates candidate readiness from apply support so policy/failover routes do not look directly switchable. Metadata-only rollback is blocked for Cloudflare-backed site targets where it would not move real traffic, and `rollback-plan` now gives operators a read-only plan before they attempt recovery. Further switching work should focus on policy-level apply/rollback or full Cloudflare Worker rollback only when the real traffic boundary can be verified end to end.
- Do not restart at CI/OpenAPI/migrations/audit unless a regression appears.
- Use the read-only real-scenario regression harness after touching Web delivery, provider evidence, rollback, switching, DNS or storage-provider behavior. Ask for mutating real-provider canaries only after local gates and read-only probes pass.

## Goal

Turn the current internally stable codebase into a maintainable long-lived product codebase without changing the working product surface.

The refactor should preserve the `v0.4.0` behavior while making future feature work safer:

- smaller modules instead of very large control-plane and CLI files;
- repeatable CI and release gates;
- explicit API contract;
- versioned database migrations;
- usable audit trail;
- clearer ownership boundaries between control plane, storage, routing, Web hosting and Cloudflare operations.

## Non-Goals

- Do not start a new UI.
- Do not change the preferred Web hosting model.
- Do not redesign routing semantics before extracting and protecting the current behavior.
- Do not remove legacy compatibility paths until tests and docs prove an equivalent migration path.
- Do not do a broad formatting-only rewrite.

## Original Pain Points

- `internal/server/server.go` is too large and mixes routing, handlers, service logic, diagnostics, Cloudflare orchestration, storage coordination and response shaping.
- `cmd/supercdnctl/main.go` is too large and mixes command parsing, HTTP transport, output formatting, local file packaging, Cloudflare Static publishing and operational workflows.
- There is no project-level CI workflow in the repository.
- There is no formal OpenAPI contract for the REST API.
- SQLite schema evolution is mostly `CREATE TABLE IF NOT EXISTS` plus additive `ensureColumn`, without a schema version table or migration history.
- `audit_events` exists in the schema, but mutation paths do not consistently write audit records.
- Release verification is documented and has been run locally, but it is not enforced by CI.

## Refactor Order

### Phase 0: Safety Net First

Do this before moving code.

1. Add GitHub Actions CI:
   - Go: `gofmt -l cmd internal`, `go test ./...`, Linux `go test -race ./...`, `go vet ./...`, `go build ./cmd/...`.
   - Go security: `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
   - API contract: `npx --yes @redocly/cli lint api/openapi.yaml`.
   - Worker: `npm ci`, `npm test`, `npx tsc --noEmit`, `npm audit --registry=https://registry.npmjs.org --audit-level=high`.
2. Add a short `docs/release-checklist.md` that mirrors the `v0.4.0` release checks.
3. Keep `scripts/foundation-check.ps1` as the Windows local equivalent.
4. Run all checks before and after each extraction slice.

Acceptance:

- CI passes on `main`.
- Local `scripts/foundation-check.ps1 -SkipLinuxBuild` still works where config permits.
- No production behavior changes.

### Phase 1: Split HTTP Server By Surface

Extract handlers and service helpers from `internal/server/server.go` by product surface.

Suggested target layout:

```text
internal/server/
  server.go              # construction, middleware, route registration
  auth_handlers.go
  doctor_handlers.go
  project_handlers.go
  site_handlers.go
  site_deploy_handlers.go
  edge_manifest_handlers.go
  asset_bucket_handlers.go
  resource_handlers.go
  cloudflare_handlers.go
  gc_handlers.go
  response.go
  routing_logic.go
  diagnostics.go
```

Rules:

- Move code in narrow slices; avoid changing behavior during moves.
- Keep route paths and JSON shapes unchanged.
- For each slice, run `go test ./internal/server ./cmd/supercdnctl`.
- Prefer moving tests only when the target file is stable; behavior tests can stay in `server_test.go` until the extraction is complete.

First slices:

1. Move `doctor`, `cdn-doctor`, `site-doctor` structs and helpers into `doctor_handlers.go` / `diagnostics.go`.
2. Move manual GC request/response and handlers into `gc_handlers.go`.
3. Move asset bucket handlers and helpers into `asset_bucket_handlers.go`.
4. Move site deployment and edge manifest logic into `site_deploy_handlers.go` and `edge_manifest_handlers.go`.

Acceptance:

- `internal/server/server.go` becomes mostly construction, routing and shared server methods.
- No endpoint response changes except intentional doc-backed fixes.
- `go test ./...` passes after each slice.

### Phase 2: Split CLI Into Commands

Extract `cmd/supercdnctl/main.go` by command groups while preserving command names and output.

Suggested target layout:

```text
cmd/supercdnctl/
  main.go             # global flags, dispatch only
  client.go           # HTTP client, JSON helpers, errors
  config.go           # CLI profile config
  output.go           # printJSON, report helpers
  auth_commands.go
  site_commands.go
  cloudflare_commands.go
  edge_commands.go
  bucket_commands.go
  diagnostic_commands.go
  resource_commands.go
  gc_commands.go
```

Rules:

- Keep the command dispatch table obvious in `main.go`.
- Move tests beside the command area only after the command files exist.
- Do not change CLI JSON fields while splitting.

First slices:

1. Move client/profile/print helpers.
2. Move `doctor`, `cdn-doctor`, `site-doctor`, `route-explain`, `probe-site`.
3. Move bucket commands including `upload-bucket` and `upload-bucket-dir`.
4. Move Cloudflare Static and hybrid edge command code.

Acceptance:

- `cmd/supercdnctl/main.go` is a small dispatcher.
- `go test ./cmd/supercdnctl` passes after each slice.
- Existing onboarding commands remain copy-paste compatible.

### Phase 3: API Contract

Add a first formal OpenAPI document for stable control-plane endpoints.

Suggested path:

```text
api/openapi.yaml
```

Start with the endpoints real users and operators touch most:

- auth/profile: `login`, `whoami`;
- diagnostics: `doctor`, `cdn-doctor`, `site-doctor`;
- asset buckets: create/init/upload/list/delete/warmup/purge;
- sites: create/deploy/list/deployment/probe-supporting metadata;
- route explanation and edge manifest export;
- GC.

Rules:

- OpenAPI should describe actual `v0.4.0` behavior, not a desired future API.
- Add contract lint if a stable tool is available.
- Keep `docs/cli-reference.md` and OpenAPI aligned.

Acceptance:

- New API changes must update OpenAPI in the same patch.
- The CLI reference links to the OpenAPI file.

### Phase 4: Versioned Migrations

Replace implicit schema evolution with explicit migration tracking.

Suggested design:

- Add `schema_migrations(version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`.
- Keep existing bootstrap safe for empty databases.
- Convert current additive columns into named migrations.
- Add a migration test that opens an old fixture DB and upgrades it.

Rules:

- Do not break existing production SQLite files.
- Keep backups as an operational requirement before deploying binaries with migrations.

Acceptance:

- `Open()` reports migration version.
- A migration failure leaves a clear error and does not silently continue.

### Phase 5: Audit Events

Wire `audit_events` for security and operational mutations.

Initial audit scope:

- invite/login/token revoke;
- site create/deploy/promote/delete/offline/online;
- domain bind/sync;
- bucket create/upload/delete/purge/warmup;
- GC dry-run and delete;
- Cloudflare control-plane writes;
- resource health/e2e write probes.

Rules:

- Never store tokens, signed URLs or secrets in audit records.
- Include workspace, user id when available, action and resource id.
- Keep query commands out of audit unless they mutate remote state.

Acceptance:

- Tests prove representative mutations write audit records.
- Add `supercdnctl audit-log` only after the write path is trustworthy.

### Phase 6: Package Boundaries

After the safe extractions, decide whether service packages are needed.

Possible package layout:

```text
internal/site/
internal/bucket/
internal/routing/
internal/diagnostics/
internal/ops/
internal/apimodel/
```

Only create packages when moved code has a stable boundary. Avoid premature abstractions that make tests harder.

## Validation Command Set

Run after every phase:

```powershell
gofmt -l cmd internal
go test ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go build ./cmd/...
npx --yes @redocly/cli lint api/openapi.yaml
cd worker
npm test
npx tsc --noEmit
npm audit --registry=https://registry.npmjs.org --audit-level=high
cd ..
git diff --check
```

If available:

```powershell
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
```

## Stop Conditions

Pause and reassess if:

- a move requires changing public JSON shape;
- tests need broad rewrites before behavior is protected;
- migration work risks production DB compatibility;
- CI cannot reproduce local checks;
- a refactor starts adding new product behavior.

## Next Session Entry Point

Start from a completion audit of the post-`v0.4.0` refactor. Do not repeat the stable-release, CI, OpenAPI, migration, audit or large-file extraction phases unless a regression appears. Further refactor work should be narrow package-boundary extraction with clear tests, not another broad line-count pass.
