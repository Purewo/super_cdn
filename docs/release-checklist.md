# Release Checklist

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
go test ./...
go vet ./...
go build ./cmd/...
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

If available in the environment, also run:

```powershell
govulncheck ./...
go test -race ./...
```

If `go test -race` cannot run because the Windows host lacks a working C toolchain, record it as unverified instead of passed.

## Release Steps

1. Commit all release changes.
2. Tag the release:

   ```powershell
   git tag -a vX.Y.Z -m "vX.Y.Z"
   ```

3. Push the branch and tag.
4. Create the GitHub Release with the validated release notes.
5. Verify the GitHub Release page contains the expected tag, title, body, and commit.

If GitHub access is unstable on this machine, use the local proxy:

```powershell
git -c http.proxy=http://127.0.0.1:10808 -c https.proxy=http://127.0.0.1:10808 push origin main --tags
```

For GitHub REST release creation, use `git credential fill` for the token and send the release body as raw UTF-8 text without BOM.

## Post-Release

- Confirm the working tree is clean.
- Record the release URL and commit.
- Update the next-session handoff with the first concrete follow-up task.
- Do not start major refactors until the staged stable release is recoverable from the tag and release notes.
