# Super CDN v0.1.0

Release date: 2026-04-30

Status: internal stable milestone.

This release freezes the first usable Super CDN control-plane baseline for personal and small-team operation. It is suitable for controlled production use with the documented server configuration, but it is not yet a public SaaS/GA release.

## Included

- Overseas website hosting through Cloudflare Workers Static Assets for ordinary static sites.
- Overseas CDN resource buckets backed by Cloudflare R2 for large reusable files such as images, videos and archives.
- Domestic AList/OpenList resource-library acceleration, live-tested on the mobile line.
- Hybrid edge deployment path with Cloudflare Static entry HTML plus KV manifest routing toward R2 or AList/OpenList.
- Origin-assisted site deployments with immutable deployments, preview URLs, production promotion and delivery probes.
- `supercdnctl` CLI for sites, deployments, buckets, probes, Cloudflare/R2 operations and resource-library checks.
- Team auth baseline: invites, users, roles, user API tokens, local CLI profiles and workspace-scoped sites/buckets/projects.
- Production smoke tests for auth, Cloudflare status and an existing real website.

## Role Model

- `server.admin_token` remains the root/break-glass credential.
- `owner` can manage invites, users and tokens.
- `maintainer` can create and deploy workspace resources.
- `viewer` is read-only for workspace resources.
- Cloudflare/R2/AList configuration endpoints remain root-only.

## Production Deployment

Production service:

```text
URL: https://qwk.ccwu.cc/
systemd unit: supercdn
install dir: /opt/supercdn
backup: /opt/supercdn/backups/20260429T225207-0400-team-auth
```

Deployed Linux amd64 hashes:

```text
supercdn    dd82bab607b33734986582d5357c163a1af5a994bbd340179628d7ace006d68a
supercdnctl 667291190017d9f708a8cf7d66dd6d53c81e6a8950155d1c9fa929774a185565
```

## Verification

Local:

```powershell
go test ./...
go vet ./...
```

Production:

```powershell
curl.exe -fsS https://qwk.ccwu.cc/healthz
.\bin\supercdnctl.exe -server https://qwk.ccwu.cc cloudflare-status -all
.\bin\supercdnctl.exe probe-site -url https://cyberstream-mobile-go.sites.qwk.ccwu.cc/ -spa-path /movie/123 -max-assets 10
```

Auth smoke performed on production:

- Root `auth/me` returned root owner.
- A short-lived viewer invite was created and accepted.
- Viewer `auth/me` returned the smoke user.
- Viewer `POST /sites` was rejected with `403`.
- Smoke token was revoked.

## Known Boundaries

- This release has a default workspace only; workspace creation/switching is a later feature.
- `audit_events` schema exists, but audit event writes are not fully wired yet.
- AList/OpenList automatic multi-origin failover is not part of this release.
- Cloudflare Static rollback and Worker version cleanup are intentionally conservative and still need a dedicated workflow.
