# Release Checklist

[English](release-checklist.md) | [简体中文](release-checklist.zh-CN.md)

Use this checklist before tagging a staged stable release.

## Scope

- Confirm the release goal and non-goals are documented.
- Confirm no unrelated worktree changes are included.
- Update the release notes with user-facing changes, validation results, and known limits.
- Keep release notes and handoff docs in UTF-8 without BOM.

## Local Validation

Run from the repository root:

```powershell
gofmt -l cmd internal
powershell -NoProfile -Command "$errors=@(); Get-ChildItem scripts -Filter *.ps1 | ForEach-Object { $tokens=$null; $parseErrors=$null; [System.Management.Automation.Language.Parser]::ParseFile($_.FullName,[ref]$tokens,[ref]$parseErrors) | Out-Null; $errors += $parseErrors }; if ($errors.Count -gt 0) { $errors; exit 1 }"
go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/ci.yml
go test ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go build ./cmd/...
npx --yes @redocly/cli lint api/openapi.yaml
git diff --check
```

Run the Worker checks:

```powershell
Push-Location worker
npm test
npx tsc --noEmit
npm audit --registry=https://registry.npmjs.org --audit-level=high
Pop-Location
```

Run the Windows foundation check where local config permits:

```powershell
.\scripts\foundation-check.ps1 -SkipLinuxBuild
```

`foundation-check.ps1` includes PowerShell script syntax validation and GitHub Actions workflow linting by default. If module download access is temporarily unavailable and the workflow file was not touched, use `-SkipActionlint` and record that static CI validation was skipped.

To run only the workflow lint:

```powershell
$env:GOPROXY = "https://goproxy.cn,direct"
go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/ci.yml
```

CI runs the Go race suite on Linux. If the local environment has a working C toolchain, run the same race check explicitly:

```powershell
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
```

On this Windows machine, prepend the portable WinLibs GCC toolchain and enable cgo before running the race gate:

```powershell
$gcc = "E:\Tools\winlibs-x86_64-posix-seh-gcc-16.1.0-mingw-w64ucrt-14.0.0-r1\mingw64\bin"
$env:Path = "$gcc;$env:Path"
$env:CGO_ENABLED = "1"
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
```

If `go test -race` cannot run because the Windows host lacks a working C toolchain, record it as unverified instead of passed.

## Real Scenario Regression

For refactors or releases that touch Web delivery, provider evidence, rollback, switching, DNS, storage providers or browser rendering, run the read-only regression harness against at least one existing public site or authenticated canary:

```powershell
.\scripts\real-scenario-regression.ps1 -UseGoRun -PublicUrl https://example.com/ -SpaPath /movie/123 -RequireEdgeStaticHtml -RequireEdgeManifestAssets -OutputPath .\data\real-regression-public.json
```

For authenticated operator checks, pass `-Server`, `-Site`, `-Deployment`, `-Bucket` and a token/profile:

```powershell
$env:SUPERCDN_TOKEN = "sct_xxx"
.\scripts\real-scenario-regression.ps1 -UseGoRun -Server https://qwk.ccwu.cc -Site cyberstream -Deployment dpl_xxx -SitePath /assets/app.js -SpaPath /movie/123 -RequireEdgeStaticHtml -RequireEdgeManifestAssets -OutputPath .\data\real-regression-site.json
```

The script is read-only and retries failed steps once by default to smooth transient provider reads. Mutating canaries such as A -> B -> rollback A, bucket upload/warmup, IPFS smoke or manual `switch-apply` still need explicit operator confirmation and should be recorded separately. See [real-scenario-regression.md](real-scenario-regression.md).

## Release Steps

1. Commit all release changes.
2. Tag the release:

   ```powershell
   git tag -a vX.Y.Z -m "vX.Y.Z"
   ```

3. Push the branch and tag.
4. Observe GitHub Actions for the pushed commit:

   ```powershell
   .\scripts\github-actions-status.ps1 -Wait -IncludeJobs
   ```

   The script checks the current branch and `HEAD` SHA by default. It exits non-zero if the worktree is dirty, if `HEAD` is not the current remote branch SHA, if no run exists for that commit, if a run is still pending after the timeout, or if any matching run did not conclude with `success`. `-IncludeJobs` adds job/step summaries to the JSON so a failed gate points at the failing job without opening the browser first. Use `-AllowDirty` or `-AllowUnpushed` only for diagnostics when you explicitly know the local changes or local commit are not part of the release candidate being checked.

5. Create the GitHub Release with the validated release notes.
6. Verify the GitHub Release page contains the expected tag, title, body, and commit.

If GitHub access is unstable on this machine, use the local proxy:

```powershell
git -c http.proxy=http://127.0.0.1:10808 -c https.proxy=http://127.0.0.1:10808 push origin main --tags
```

If Go module downloads fail against `proxy.golang.org` while running `govulncheck`, set a temporary module proxy in the current shell only:

```powershell
$env:GOPROXY = "https://goproxy.cn,direct"
```

If `npm audit` fails with a TLS socket disconnect while `HTTP_PROXY` / `HTTPS_PROXY` points at `127.0.0.1:10808`, retry without those npm proxy environment variables; direct access to `registry.npmjs.org` may be more reliable on this machine.

For GitHub REST release creation, use `git credential fill` for the token and send the release body as raw UTF-8 text without BOM.

If the Actions status check hits anonymous API rate limits, set a temporary `GITHUB_TOKEN` in the current shell before rerunning `github-actions-status.ps1`.

## Post-Release

- Confirm the working tree is clean.
- Record the release URL and commit.
- Update the next-session handoff with the first concrete follow-up task.
- Do not start major refactors until the staged stable release is recoverable from the tag and release notes.
