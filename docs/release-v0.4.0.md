# Super CDN v0.4.0

[English](release-v0.4.0.md) | [简体中文](release-v0.4.0.zh-CN.md)

Release date: 2026-05-13

Status: internal stable milestone.

This release freezes the current real-user onboarding and operations surface before the next larger refactor. It keeps the `v0.3.x` Web/CDN delivery model, then adds support-safe diagnostics, safer batch upload behavior, first-pass cleanup, and storage guardrails so a real user can create a bucket, upload files, publish a site, and send useful failure reports.

## Included

- First-pass diagnostics:
  - `doctor` / `GET /api/v1/doctor` reports auth, database, storage target, route profile, staging, resource-library and routing-policy status.
  - `cdn-doctor` / `GET /api/v1/asset-buckets/{slug}/doctor` reports bucket state, route profile, object state, public URL, redacted storage URL, replicas, IPFS metadata, route candidates and selected line.
  - `site-doctor` / `GET /api/v1/sites/{id}/doctor` reports site state, active deployment, deployment target, route explanation, redacted candidates and expected edge headers.
- Batch upload hardening:
  - `upload-bucket-dir -dry-run` prints an upload plan without sending files.
  - `-report-file` writes the JSON batch report for success, partial failure and dry-run flows.
  - `-retry` retries per-file upload or warmup failures.
  - `-skip-existing` skips logical paths already tracked in the bucket.
  - Reports now include `summary`, `report_saved_to` and `next_commands`.
- Single-file upload UX:
  - `upload-bucket` output keeps existing server fields and adds `summary`, `copy_urls` and `next_commands`.
  - Upload failures include the matching `cdn-doctor` command.
- First-pass cleanup:
  - `gc` / `POST /api/v1/gc` is root-only, dry-run by default, and removes stale local `data/staging` files when explicitly run with `-dry-run=false`.
  - Remote cleanup, bucket scope and site scope are represented but intentionally left as future guarded work.
- Storage guardrails:
  - Direct storage targets can declare `storage[].policy` and `storage[].constraints`.
  - Config examples use realistic account-sized budgets rather than provider maxima.
- Documentation:
  - Added `docs/onboarding.md` as the shortest real-user path.
  - Updated README, CLI reference and handoff notes for diagnostics, cleanup and onboarding.

## Verification

Local release checks:

```powershell
gofmt -l cmd internal
go test ./...
go vet ./...
go build ./cmd/...
$env:GOOS = "linux"; $env:GOARCH = "amd64"; go build ./cmd/...
cd worker
npm test
npx tsc --noEmit
npm audit --registry=https://registry.npmjs.org --audit-level=high
git diff --check
```

Observed local result:

- Go tests passed.
- Go vet passed.
- Windows build passed.
- Linux amd64 cross-build passed.
- Worker tests passed.
- TypeScript check passed.
- npm audit reported 0 high vulnerabilities.
- `git diff --check` passed.

## Known Boundaries

- This is not a public GA release. It is a staged stable release for controlled real-user onboarding before a larger codebase refactor.
- The repository still lacks project-level CI; release verification is currently performed locally.
- The server and CLI entry files are large and should be split during the next refactor.
- There is no formal OpenAPI contract yet.
- SQLite schema changes still use automatic table creation plus additive columns instead of versioned migrations.
- `audit_events` exists in the schema, but audit event writes are not fully wired.
- `govulncheck` was not available on the release machine at freeze time.
- `go test -race` could not run on the release machine because the Windows environment lacked a C compiler for cgo.
