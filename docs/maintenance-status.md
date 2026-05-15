# Super CDN Maintenance Status

[English](maintenance-status.md) | [简体中文](maintenance-status.zh-CN.md)

Last updated: 2026-05-16 Asia/Shanghai.

This is the current first-read handoff for the project. Super CDN is now in maintenance stabilization: the next product step is a small-scope user test pass, not another broad feature or refactor cycle.

## Current Baseline

- Stable baseline: `v0.5.0`.
- Latest post-release code baseline: commit `8dd3b16` (`add user upload quota workflow`).
- Current position: preserve documented behavior, collect real user feedback, and make only targeted fixes until the small-scale test round is complete.
- Deferred work: broad refactors, new product surfaces, automatic CDN switching, and provider-mutating behavior changes should wait unless a test result exposes a concrete defect.

## What Can Change During Stabilization

Allowed by default:

- documentation, examples and runbook corrections;
- reproducible bug fixes with the smallest practical code change;
- test coverage for a confirmed regression;
- deployment/configuration fixes needed to keep the current release usable;
- CI, security scan or dependency maintenance that does not change product behavior.

Requires explicit review before implementation:

- new CLI commands or API mutation routes;
- changes to quota, auth, workspace scoping or audit behavior;
- Cloudflare Worker/KV, DNS, R2, AList/OpenList, IPFS/Pinata or rollback write behavior;
- routing-policy or resource-failover semantics;
- package-boundary refactors that move behavior without fixing a confirmed issue.

## Small-Scale Test Checklist

Use this as the next user-facing verification pass:

1. Team auth: `invite-user`, `login`, `whoami`, `logout`, local profiles and wrong-token behavior.
2. Quota workflow: `quota`, `request-quota`, root `quota-requests`, `approve-quota`, `reject-quota`, `set-user-quota`, and one low-quota upload rejection.
3. Asset bucket flow: create a CDN bucket, upload one file, run `upload-bucket-dir`, list objects, then run `cdn-doctor`.
4. Static site flow: `create-site`, `deploy-site`, `update-site`, `probe-site`, `site-doctor`, and `reconcile-deployment`.
5. Operator evidence: `doctor`, `audit-log`, dry-run `switch-plan`, dry-run rollback/recovery commands where a real site has enough evidence.
6. Failure visibility: expired token, missing object, provider timeout, quota exceeded and unsupported switch/rollback cases should return actionable errors, not silent success.

Record failures with command, server URL, workspace/profile, redacted output and whether the operation was read-only or mutating.

## Verification Gate For Future Changes

For documentation-only changes:

```powershell
go test ./internal/docscheck
git diff --check
```

For CLI/API/user-facing behavior changes:

```powershell
go test ./...
go vet ./...
go build ./cmd/supercdn ./cmd/supercdnctl
go test ./cmd/supercdnctl
go test ./internal/docscheck
git diff --check
```

For release, refactor or provider-affecting changes, use the full gate from [release-checklist.md](release-checklist.md), and verify the pushed GitHub Actions run with:

```powershell
.\scripts\github-actions-status.ps1 -Wait -IncludeJobs
```

Run [real-scenario-regression.md](real-scenario-regression.md) before asking for mutating real-provider canaries after Web delivery, storage-provider, switching, rollback, DNS or Cloudflare behavior changes.

## Reference Order

Read these in order when returning to the project:

1. This document.
2. [README.md](../README.md) or [README.zh-CN.md](../README.zh-CN.md).
3. [operations.md](operations.md) for operational triage.
4. [maturity-audit.md](maturity-audit.md) for the current evidence checklist.
5. [commands.md](commands.md) and [cli-reference.md](cli-reference.md) for advanced CLI usage.
6. [refactor-plan.md](refactor-plan.md) and [tomorrow-plan.md](tomorrow-plan.md) only after the maintenance gate is lifted or a test result requires targeted refactor work.
