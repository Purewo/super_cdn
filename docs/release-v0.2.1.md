# Super CDN v0.2.1

Release date: 2026-05-04

Status: internal stable patch milestone.

This release closes the current CDN management surface and tightens the Web hosting lifecycle after the `v0.2.0` split-hosting milestone. It is intended as a stable handoff before the next development stage starts on multi-resource governance, replica repair, smart routing and explicit failover hardening.

## Included

- Site lifecycle controls:
  - `offline-site` marks a site offline without deleting deployments or resource objects.
  - `online-site` restores an offline site.
  - `delete-site -force` performs destructive cleanup of tracked deployment files, artifacts, manifests and site metadata.
- Cloudflare Static safety boundaries:
  - Cloudflare Static rollback through metadata-only `promote-deployment` remains blocked.
  - Cloudflare Worker versions, custom domains and KV entries are explicitly outside Super CDN destructive cleanup.
- CDN bucket cleanup controls:
  - `delete-bucket-object -path` keeps the old single-object behavior.
  - `delete-bucket-object -paths` deletes multiple exact logical paths.
  - `delete-bucket-object -prefix -force` deletes every object under a prefix.
  - `delete-bucket-object -all -force` deletes every tracked object in a bucket.
  - `delete-bucket -force` remains the full bucket deletion path and deletes tracked objects first.
- CLI and API documentation now describe the destructive boundaries for site deletion and bucket object deletion.

## Verification

Local:

```powershell
go test ./...
```

Production smoke:

```powershell
supercdnctl create-cdn-bucket -slug cdn-delete-smoke-<stamp> -types document
supercdnctl upload-bucket -bucket cdn-delete-smoke-<stamp> -file ./a.txt -path docs/a.txt -asset-type document
supercdnctl upload-bucket -bucket cdn-delete-smoke-<stamp> -file ./b.txt -path docs/b.txt -asset-type document
supercdnctl upload-bucket -bucket cdn-delete-smoke-<stamp> -file ./c.txt -path tmp/c.txt -asset-type document
supercdnctl upload-bucket -bucket cdn-delete-smoke-<stamp> -file ./d.txt -path keep/d.txt -asset-type document
supercdnctl delete-bucket-object -bucket cdn-delete-smoke-<stamp> -paths docs/a.txt,docs/b.txt
supercdnctl delete-bucket-object -bucket cdn-delete-smoke-<stamp> -prefix tmp/ -force
supercdnctl delete-bucket-object -bucket cdn-delete-smoke-<stamp> -all -force
supercdnctl delete-bucket -bucket cdn-delete-smoke-<stamp> -force
```

Observed result: 4 uploaded objects, then 2 exact-path deletes, 1 prefix delete, 1 all-object delete, and final bucket deletion succeeded on production.

## Known Boundaries

- `delete-site` removes Super CDN metadata and tracked resource objects only. Cloudflare Worker versions, custom domains and KV entries still require separate Cloudflare-side cleanup.
- `delete-bucket-object -delete-remote=false` only removes local metadata and should be reserved for recovery work.
- Prefix and all-object deletion require `-force` by design.
- `v0.3` remains focused on multi-resource-library governance, replica repair, smart routing and explicit failover.
