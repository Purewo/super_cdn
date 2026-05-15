# Super CDN v0.5.0

Release date: 2026-05-15

Status: staged mature milestone.

This release closes the post-`v0.4.0` maturity cycle. It keeps the existing product surface, then makes the codebase, operations path, documentation and regression evidence strong enough to continue long-running development from a stable baseline.

## Included

- Release gates and CI:
  - GitHub Actions now runs Go format, tests, Linux race tests, vet, vulnerability scan, command builds, OpenAPI lint, Worker tests, TypeScript check and Worker dependency audit.
  - `scripts/foundation-check.ps1` is the Windows local equivalent, including PowerShell syntax validation, actionlint, optional Windows race coverage, `govulncheck`, Redocly OpenAPI lint and Worker audit.
  - `scripts/github-actions-status.ps1` checks the pushed branch/head SHA and can include job/step summaries for release evidence.
- API, database and audit maturity:
  - `api/openapi.yaml` describes the stable operator/control-plane surface and is linted in CI.
  - SQLite schema changes are tracked through `schema_migrations`.
  - Mutation audit events are written on representative security and operations paths and are queryable through `GET /api/v1/audit-events` / `supercdnctl audit-log`.
- Server and CLI structure:
  - `internal/server/server.go` is now a small construction, lifecycle and route-registration skeleton.
  - `cmd/supercdnctl/main.go` is now a dispatcher/global-flag entry point.
  - Stable ownership boundaries now exist for deployment targets, deployment evidence operations, Cloudflare Static option normalization, edge evidence headers, diagnostic URL redaction and CLI command hints.
- Operator workflow maturity:
  - `cdn-doctor` and `site-doctor` emit actionable recommendations.
  - `switch-plan` is read-only and separates candidate readiness from apply support.
  - `switch-apply` is explicit, confirmed and audited for supported primary-target switches.
  - Cloudflare-backed metadata-only promote is blocked where it would not move real traffic.
  - `rollback-plan`, `rollback-apply`, `recover-cloudflare-static`, `activate-cloudflare-static`, `recover-hybrid-edge` and `reconcile-deployment` provide evidence-based recovery paths.
- Documentation:
  - README now gives the first-run path, documentation map and core concepts.
  - `docs/commands.md` is the advanced command book.
  - `docs/operations.md` is the short operator runbook.
  - `docs/real-scenario-regression.md` describes the read-only real-environment regression harness.
  - `docs/maturity-audit.md` is the evidence checklist before making maturity claims.
  - Tests now fail if dispatched CLI commands are missing from `docs/commands.md` or built-in `usage()` output.
  - Tests now fail when README or `docs/*.md` contain broken local Markdown links.
- Real scenario regression:
  - `scripts/real-scenario-regression.ps1` collects read-only JSON evidence from `doctor`, `cdn-doctor`, `site-doctor`, `probe-site` and `reconcile-deployment`.
  - Public read-only regression passed against `https://cyberstream-ipfs-0501.qwk.ccwu.cc/` with Cloudflare Static HTML/SPA and IPFS gateway manifest-routed JS.

## Verification

Local release gate:

```powershell
$gcc = "E:\Tools\winlibs-x86_64-posix-seh-gcc-16.1.0-mingw-w64ucrt-14.0.0-r1\mingw64\bin"
$env:Path = "$gcc;$env:Path"
$env:CGO_ENABLED = "1"
$env:GOPROXY = "https://goproxy.cn,direct"
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
```

Observed local result:

- Go format check passed.
- PowerShell syntax check passed.
- GitHub Actions workflow lint passed.
- `go test ./...` passed.
- `go test -race ./...` passed on Windows with the portable WinLibs GCC toolchain.
- `go vet ./...` passed.
- `govulncheck ./...` reported no vulnerabilities.
- Windows server and CLI builds passed.
- OpenAPI lint passed.
- Worker tests passed.
- Worker TypeScript check passed.
- Worker npm audit reported 0 vulnerabilities.
- Service `/healthz` check passed against the running local service.

Pushed release commit gate:

```powershell
.\scripts\github-actions-status.ps1 -Wait -IncludeJobs
```

Record the final GitHub Actions run id in the GitHub Release entry after the release commit is pushed and the gate is green.

Read-only real scenario evidence:

```powershell
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -SkipDoctor `
  -PublicUrl https://cyberstream-ipfs-0501.qwk.ccwu.cc/ `
  -SpaPath /movie/123 `
  -RequireEdgeStaticHtml `
  -RequireEdgeManifestAssets `
  -OutputPath .\data\real-regression-urlredact-smoke.json
```

Observed result:

- Status: `ok`
- Public URL: `https://cyberstream-ipfs-0501.qwk.ccwu.cc/`
- HTML and SPA fallback source: `cloudflare_static`
- JS asset source: `ipfs_gateway`
- Edge manifest routing: `route`
- Failed steps: `0`

## Known Boundaries

- This is still a staged stable milestone, not a broad public GA claim.
- Policy-level CDN switching remains operator-controlled. `switch-apply` does not edit route profiles, routing policies, Worker code or KV manifests.
- Cloudflare-backed metadata delete does not remove Worker versions, custom domains or KV entries.
- Cloudflare provider writes are eventually consistent. Recovery paths require evidence, strict probes and audit events instead of silent metadata promotion.
- Additional mutating real-provider canaries are required before future changes that touch Cloudflare writes, DNS, Worker/KV publishing, AList/OpenList visibility, R2 provisioning/CORS, IPFS/Pinata upload, manual switching or rollback write behavior.
