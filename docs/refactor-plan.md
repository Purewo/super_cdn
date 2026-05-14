# Super CDN Refactor Plan

Last updated: 2026-05-14 Asia/Shanghai.

Baseline: `v0.4.0` is the current staged stable release. It freezes onboarding, diagnostics, cleanup and storage guardrails before the larger refactor.

## Progress On `main`

Completed after `v0.4.0`:

- Phase 0 safety net: GitHub Actions CI and `docs/release-checklist.md`.
- Phase 1/2 first extraction pass: diagnostics, GC, asset buckets, site deployments, edge manifests and several CLI command groups moved out of the largest files.
- Phase 3 API contract: `api/openapi.yaml` added and linked from `docs/cli-reference.md`.
- Phase 4 versioned migrations: `schema_migrations` added, existing additive columns converted to named migrations, and old-DB upgrade tests added.
- Phase 5 audit events: representative security and operational mutation paths write `audit_events`, with tests proving writes and secret redaction boundaries.
- Phase 6 server extraction pass: object operations, object replication, routing selection, public serving and site deletion helpers moved out of `internal/server/server.go`.
- Phase 6 CLI extraction pass: `cmd/supercdnctl/main.go` is now a small dispatcher; client/config, core commands, provider/IPFS commands, Cloudflare/R2 ops, Cloudflare Static, resources, object ops, diagnostics, probes, sites, buckets, GC and helper code live in separate files. Current line counts are about `internal/server/server.go` 3318 and `cmd/supercdnctl/main.go` 285.

Next refactor entry point:

- Continue Phase 6 package-boundary work only where the boundary is now obvious.
- Highest-value remaining cleanup is server-side: reduce `internal/server/server.go` by moving resource-library operations, Cloudflare/R2 orchestration, site/domain helpers and shared response/validation helpers without changing route paths or output JSON.
- CLI cleanup is mostly complete for now; only revisit it for command-specific tests, help text fixes or smaller ownership tweaks.
- Do not restart at CI/OpenAPI/migrations/audit unless a regression appears.

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
   - Go: `gofmt -l cmd internal`, `go test ./...`, `go vet ./...`, `go build ./cmd/...`.
   - Worker: `npm ci`, `npm test`, `npx tsc --noEmit`.
   - Optional dependency checks: `npm audit --registry=https://registry.npmjs.org --audit-level=high`; add `govulncheck` if the action can install it reliably.
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
go build ./cmd/...
cd worker
npm test
npx tsc --noEmit
cd ..
git diff --check
```

Before a release:

```powershell
npm audit --registry=https://registry.npmjs.org --audit-level=high
```

If available:

```powershell
govulncheck ./...
```

## Stop Conditions

Pause and reassess if:

- a move requires changing public JSON shape;
- tests need broad rewrites before behavior is protected;
- migration work risks production DB compatibility;
- CI cannot reproduce local checks;
- a refactor starts adding new product behavior.

## Next Session Entry Point

Start from Phase 6. Do not repeat the stable-release, CI, OpenAPI, migration or audit phases unless a regression appears. The next best slice is server-side extraction from `internal/server/server.go`, starting with resource-library operations or Cloudflare/R2 orchestration because their boundaries are now visible and already have CLI/API coverage.
