# Super CDN Maturity Audit

Last updated: 2026-05-15 Asia/Shanghai.

This document tracks the current maturity work after the staged `v0.4.0` release. It is intentionally evidence-based: do not mark the product mature because a broad test command passed unless the command covers the specific requirement.

## Objective

Make Super CDN mature enough for long-running development and operator use without hiding remaining risk.

Concrete success criteria:

- release gates are repeatable locally and in CI;
- API and CLI behavior have an explicit contract;
- database changes are versioned;
- security and operational mutations have a usable audit trail;
- large server and CLI files have clear ownership boundaries;
- diagnostics explain what to do next, not just what failed;
- manual CDN line switching is explicit, confirmed and auditable;
- rollback paths do not claim to move real traffic unless metadata, Worker assets and KV manifests all move together;
- remaining unsupported paths are clearly documented and not presented as safe apply commands.

## Evidence Checklist

| Requirement | Current artifact | Verification |
| --- | --- | --- |
| Local release gate | `scripts/foundation-check.ps1` | Includes gofmt, PowerShell script syntax validation, actionlint, Go tests/vet/vuln/build, OpenAPI lint, Worker test/typecheck/audit and service healthz. Passed with `-SkipLinuxBuild` using temporary `GOPROXY=https://goproxy.cn,direct` after `proxy.golang.org` EOF. For this run, npm audit passed direct; routing npm audit through `127.0.0.1:10808` produced TLS socket disconnects. |
| CI release gate | `.github/workflows/ci.yml`, `scripts/github-actions-status.ps1` | Defines Go format/test/race/vet/vuln/build, OpenAPI lint, Worker test/typecheck/audit. Workflow static lint is now part of the local foundation gate, GitHub action majors are on Node 24-compatible versions, and the status script checks pushed branch/head SHA runs through the GitHub Actions API with optional job/step summaries, dirty-worktree protection and remote branch SHA verification. Must still be observed on GitHub after push. |
| Race coverage | `.github/workflows/ci.yml` | CI runs `go test -race ./...` on Ubuntu. Local Windows race remains environment-blocked: `go env` reports `CGO_ENABLED=0`, `CC=gcc`, and no `gcc`, `clang`, `zig` or `cc` command is currently available in PATH. |
| API contract | `api/openapi.yaml` | Redocly lint passed locally through foundation check. |
| Versioned migrations | `internal/db` migration code and tests | Covered by existing DB tests in `go test ./...`. |
| Audit query surface | `GET /api/v1/audit-events`, `supercdnctl audit-log` | Tests cover workspace scoping, viewer rejection and CLI query parameters. |
| Dangerous rollback audit | `site.deployment.promote.rejected` | Tests cover rejected `cloudflare_static` and `hybrid_edge` metadata promote attempts writing audit events. |
| Server ownership boundary | `internal/server/server.go` plus split handler/service files | `server.go` is now route/lifecycle plumbing; feature code lives in narrower files. |
| CLI ownership boundary | `cmd/supercdnctl/main.go` plus command files | `main.go` is dispatcher/global flag plumbing; command groups live in separate files. |
| Deployment target normalization | `internal/deploymenttarget` | Shared by config, server and CLI aliases. |
| Doctor next actions | `cdn-doctor`, `site-doctor` recommendations | Tests cover manual switch recommendations and not-ready paths. |
| Manual switch planning | `supercdnctl switch-plan` | Reports candidate readiness, apply support, risks and next commands. |
| Manual switch apply | `supercdnctl switch-apply` and primary-target APIs | Dry-run by default, requires `-confirm switch`, writes audit events for applied and rejected attempts, returns rollback command. |
| Policy/failover switch boundary | `switch-plan` and `switch-apply` | Policy and resource-failover routes are not presented as directly switchable; server rejects unsupported metadata apply. |
| Cloudflare-backed rollback guard | `promote-deployment`, `delete-deployment` | Non-active `cloudflare_static` and `hybrid_edge` metadata promote attempts return conflict and are audited; `delete-deployment -dry-run` and API `dry_run=true` preview safety/evidence, expose Cloudflare remote cleanup blockers, and deleting those deployments warns that Worker versions, custom domains and KV entries are not removed. |
| Rollback preflight | `supercdnctl rollback-plan` | Read-only plan distinguishes metadata promote from Cloudflare/hybrid redeploy, includes version evidence when available, reports Cloudflare rollback write blockers plus missing evidence, and emits post-redeploy probe commands for Cloudflare/hybrid traffic verification. |
| Operator command copy-paste safety | CLI command hint quoting | Switch and upload diagnostic commands quote PowerShell arguments with spaces or single quotes. |

## Accepted Boundaries

These are intentional product boundaries, not hidden unfinished features:

- Policy-level CDN switching is operator-controlled. `switch-plan` reports readiness and `apply_supported=false` for `routing_policy` and `resource_failover`; `switch-apply` rejects metadata-only policy/failover writes and audits rejected attempts. If this direction changes, use `docs/policy-switching-boundary.md` before adding any write command.
- Metadata delete does not clean Cloudflare Worker versions, custom domains or KV entries. `delete-deployment -dry-run` and API `dry_run=true` now expose `remote_cleanup_supported=false`, blockers and Cloudflare evidence so an operator can perform provider-specific cleanup deliberately.

## Remaining Gaps

These are not solved yet and must not be described as mature:

- GitHub Actions has not been observed after these local changes are pushed. Local `actionlint` passed and `scripts/github-actions-status.ps1` can verify the pushed branch/head SHA, but no remote run has been observed yet.
- Local Windows `go test -race ./...` is still unverified until a working C toolchain is installed; CI is the current race gate. Checked PATH on 2026-05-15 and found no `gcc`, `clang`, `zig` or `cc`.
- Full Cloudflare Static / hybrid-edge rollback is still not implemented as a write operation. Current behavior is read-only `rollback-plan` plus redeploy guidance; the future write-command boundary is recorded in `docs/cloudflare-rollback-boundary.md`.
- A live end-to-end rollback validation has not been run for Worker assets, active KV manifest and real custom-domain traffic.

## Next Concrete Work

1. Push the branch and run `.\scripts\github-actions-status.ps1 -Wait -IncludeJobs` to observe GitHub Actions, especially Ubuntu race coverage.
2. Use `docs/cloudflare-rollback-boundary.md` before designing any Cloudflare/hybrid rollback write command; do not add a write path until Worker, KV, domain and live-probe requirements are met.
3. Run a live canary rollback before adding any command that writes Cloudflare rollback state.
