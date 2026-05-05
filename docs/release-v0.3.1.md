# Super CDN v0.3.1

Release date: 2026-05-06

Status: internal stable patch.

This patch adds a practical CDN bucket batch-upload path for real user onboarding. It does not change the server upload API; the CLI scans a local directory and safely reuses the existing single-object bucket upload endpoint.

## Included

- New `upload-bucket-dir` CLI command.
- Recursive local directory scan with relative path preservation.
- `-prefix` support for placing a directory tree under a bucket logical path.
- Parallel uploads with default `-concurrency 10`; operators can tune this per environment.
- Batch JSON report with per-file success/error details.
- Optional per-file warmup through the existing bucket warmup API.
- Multipart uploads now stream from disk instead of buffering the whole file in CLI memory.

## Verification

Local:

```powershell
go test ./cmd/supercdnctl
go test ./...
```

Production smoke:

```powershell
supercdnctl create-cdn-bucket -slug batch-upload-smoke-<stamp> -types document
supercdnctl upload-bucket-dir -bucket batch-upload-smoke-<stamp> -dir <temp-dir> -prefix smoke -asset-type document -concurrency 2
supercdnctl delete-bucket -bucket batch-upload-smoke-<stamp> -force
```

Observed result: 3 files uploaded successfully with concurrency 2, then the temporary bucket was deleted.

## Known Boundaries

- This is CLI-side batching over the existing single-file API, not a new server-side transaction.
- Failed files are reported after the batch finishes; already uploaded files remain in the bucket.
- There is no skip-existing or resume flag yet. Re-running with the same logical paths follows the existing single-object upload behavior.
