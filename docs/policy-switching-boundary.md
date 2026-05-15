# Policy Switching Boundary

[English](policy-switching-boundary.md) | [简体中文](policy-switching-boundary.zh-CN.md)

Last updated: 2026-05-15 Asia/Shanghai.

This note records the current decision for `routing_policy` and `resource_failover` switching. It exists to prevent a false sense of maturity: Super CDN can inspect policy candidates today, but it must not expose a write command that claims to switch policy traffic until the real traffic boundary is proven.

## Current Decision

Do not implement policy-level apply/rollback as a metadata write. The product direction is explicit operator choice: Super CDN should show candidate readiness and explain the route decision, while users decide whether to change route profiles, routing policy configuration, or redeploy Cloudflare-backed assets.

Current supported behavior:

- `cdn-doctor` and `site-doctor` report candidates, skipped targets, health reasons and recommendations.
- `switch-plan` separates `candidate_ready` from `apply_supported`.
- `switch-plan` returns `apply_supported=false` for `routing_policy` and `resource_failover` routes.
- `switch-plan` suggests `routing-policy-status` and/or `route-explain` as next diagnostics.
- `switch-apply` rejects routing-policy and resource-failover paths instead of changing `primary_target`.
- rejected switch attempts are audited.

## Why Metadata Apply Is Not Enough

For a simple non-policy object, changing `objects.primary_target` can control the selected target.

For `routing_policy`, selection is derived from policy mode, region group, source weights/priorities, health cache, candidate readiness and request attributes. Changing one object's `primary_target` does not necessarily change live selection.

For `resource_failover`, candidate order comes from the route profile and exported manifest. Changing one object's `primary_target` does not change the failover route order already published to Worker/KV.

For `hybrid_edge`, even a correct control-plane policy change may not affect real traffic until the active edge manifest is republished and verified on the bound domain.

## If A Write Command Is Reconsidered

A future policy apply/rollback command must have all of these:

1. A durable source of truth for the intended policy override or rollback target.
2. A dry-run plan that names exactly which routes, objects, manifests and domains would change.
3. Explicit confirmation for writes.
4. Audit events for both applied and rejected attempts.
5. A rollback command or rollback plan emitted after success.
6. Active edge manifest publication when the target is `hybrid_edge`.
7. Live verification against the real custom domain, including edge headers that prove the selected candidate changed.
8. Tests showing unsupported policy/failover metadata-only changes remain rejected.

## Acceptable Interim Operations

Until the above exists, operators should use:

- `switch-plan` to inspect candidate readiness and apply support;
- `routing-policy-status` to inspect policy source health;
- `route-explain` to inspect a site path's actual selection logic;
- `refresh-edge-manifest` after repairing resource readiness or signatures for active hybrid deployments;
- a full `deploy-site -target hybrid_edge` or `deploy-site -target cloudflare_static` flow for Cloudflare-backed rollback.
