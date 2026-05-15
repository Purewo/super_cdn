# 发布检查清单

[English](release-checklist.md) | 简体中文

这份清单用于发布前确认本地验证、CI、真实场景证据、tag 和 GitHub Release 都完整。它不替代具体 release note。

## 范围

每次阶段性发布至少确认：

- 工作区干净；
- 文档和命令覆盖同步；
- 本地 foundation check 通过；
- GitHub Actions 对目标 commit 通过；
- release note 记录已知边界；
- tag 指向已验证 commit；
- GitHub Release body 包含最终证据。

## 本地验证

普通检查：

```powershell
.\scripts\foundation-check.ps1
```

完整 Windows 本地 gate：

```powershell
$gcc = "E:\Tools\winlibs-x86_64-posix-seh-gcc-16.1.0-mingw-w64ucrt-14.0.0-r1\mingw64\bin"
$env:Path = "$gcc;$env:Path"
$env:CGO_ENABLED = "1"
$env:GOPROXY = "https://goproxy.cn,direct"
.\scripts\foundation-check.ps1 -SkipLinuxBuild -Race
```

期望覆盖：

- gofmt；
- PowerShell 语法检查；
- GitHub Actions workflow lint；
- `go test ./...`；
- `go test -race ./...`；
- `go vet ./...`；
- `govulncheck ./...`；
- Windows server/CLI build；
- OpenAPI lint；
- Worker test/typecheck/audit；
- 本地服务 `/healthz`。

## 真实场景回归

Web 交付、边缘路由、回滚、切换、DNS、存储 provider 或浏览器渲染变化后，运行只读真实场景回归：

```powershell
.\scripts\real-scenario-regression.ps1 `
  -UseGoRun `
  -PublicUrl https://example.com/ `
  -SpaPath /movie/123 `
  -RequireEdgeStaticHtml `
  -RequireEdgeManifestAssets `
  -OutputPath .\data\real-regression-public.json
```

如果涉及真实写入风险，在本地 gate 和只读 probe 通过后再安排 provider canary。

## 发布步骤

1. 更新 release note，例如 `docs/release-v0.5.0.md`。
2. 更新 README、命令文档、边界文档和 handoff 文档。
3. 运行本地验证。
4. 提交并推送到 `main`。
5. 等待 GitHub Actions 对该 commit 成功：

```powershell
.\scripts\github-actions-status.ps1 -Wait -IncludeJobs
```

6. 创建 annotated tag：

```powershell
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
```

7. 创建 GitHub Release，并写入：

- tag；
- commit SHA；
- GitHub Actions run id 和结论；
- 本地 foundation check 结果；
- 真实场景回归结果；
- 已知边界。

## 发布后

发布后检查：

- `git status --short` 为空；
- `origin/main` 指向 release commit；
- tag 可从远端读取；
- GitHub Release 可访问；
- release note 中没有把阶段稳定误写成公开 GA；
- 下一阶段入口文档明确。
