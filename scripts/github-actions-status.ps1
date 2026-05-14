param(
  [string]$Owner = "",
  [string]$Repo = "",
  [string]$Remote = "origin",
  [string]$Branch = "",
  [string]$Sha = "",
  [string]$Token = "",
  [string]$ApiBase = "https://api.github.com",
  [switch]$Wait,
  [switch]$IncludeJobs,
  [switch]$AllowDirty,
  [switch]$AllowUnpushed,
  [int]$TimeoutSeconds = 1800,
  [int]$IntervalSeconds = 15,
  [switch]$AllowMissing,
  [switch]$AllowPending
)

$ErrorActionPreference = "Stop"

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

function Invoke-GitValue {
  param([string[]]$GitArgs)
  Push-Location $RepoRoot
  try {
    $value = (& git @GitArgs 2>$null)
    if ($LASTEXITCODE -ne 0) {
      return ""
    }
    return ([string]$value).Trim()
  } finally {
    Pop-Location
  }
}

function Resolve-GitHubRepository {
  param([string]$RemoteName)
  $url = Invoke-GitValue -GitArgs @("remote", "get-url", $RemoteName)
  if ([string]::IsNullOrWhiteSpace($url)) {
    throw "git remote '$RemoteName' was not found"
  }
  if ($url -notmatch "github\.com[:/](?<owner>[^/]+)/(?<repo>[^/.]+)(\.git)?$") {
    throw "remote '$RemoteName' is not a GitHub repository URL: $url"
  }
  return @{
    Owner = $Matches["owner"]
    Repo = $Matches["repo"]
  }
}

function Test-WorktreeDirty {
  Push-Location $RepoRoot
  try {
    $status = (& git status --porcelain)
    if ($LASTEXITCODE -ne 0) {
      throw "git status failed"
    }
    return @($status).Count -gt 0
  } finally {
    Pop-Location
  }
}

function Get-RemoteBranchSha {
  param(
    [string]$RemoteName,
    [string]$BranchName
  )
  Push-Location $RepoRoot
  try {
    $ref = "refs/heads/$BranchName"
    $line = (& git ls-remote $RemoteName $ref 2>$null)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($line)) {
      return ""
    }
    return ([string]$line).Split("`t")[0].Trim()
  } finally {
    Pop-Location
  }
}

function Invoke-GitHubRunsRequest {
  param(
    [string]$OwnerValue,
    [string]$RepoValue,
    [string]$BranchValue,
    [string]$ShaValue
  )
  $base = $ApiBase.TrimEnd("/")
  $branchQuery = [System.Uri]::EscapeDataString($BranchValue)
  $shaQuery = [System.Uri]::EscapeDataString($ShaValue)
  $uri = "$base/repos/$OwnerValue/$RepoValue/actions/runs?branch=$branchQuery&head_sha=$shaQuery&per_page=20"
  $headers = @{
    "Accept" = "application/vnd.github+json"
    "X-GitHub-Api-Version" = "2026-03-10"
    "User-Agent" = "supercdn-release-check"
  }
  $effectiveToken = $Token
  if ([string]::IsNullOrWhiteSpace($effectiveToken)) {
    $effectiveToken = $env:GITHUB_TOKEN
  }
  if (-not [string]::IsNullOrWhiteSpace($effectiveToken)) {
    $headers["Authorization"] = "Bearer $effectiveToken"
  }
  return Invoke-RestMethod -Method Get -Uri $uri -Headers $headers
}

function Invoke-GitHubJobsRequest {
  param(
    [string]$OwnerValue,
    [string]$RepoValue,
    [int64]$RunID
  )
  $base = $ApiBase.TrimEnd("/")
  $uri = "$base/repos/$OwnerValue/$RepoValue/actions/runs/$RunID/jobs?per_page=100"
  $headers = @{
    "Accept" = "application/vnd.github+json"
    "X-GitHub-Api-Version" = "2026-03-10"
    "User-Agent" = "supercdn-release-check"
  }
  $effectiveToken = $Token
  if ([string]::IsNullOrWhiteSpace($effectiveToken)) {
    $effectiveToken = $env:GITHUB_TOKEN
  }
  if (-not [string]::IsNullOrWhiteSpace($effectiveToken)) {
    $headers["Authorization"] = "Bearer $effectiveToken"
  }
  return Invoke-RestMethod -Method Get -Uri $uri -Headers $headers
}

function Convert-ToStepSummary {
  param($Step)
  return [ordered]@{
    name = $Step.name
    status = $Step.status
    conclusion = $Step.conclusion
    started_at = $Step.started_at
    completed_at = $Step.completed_at
  }
}

function Convert-ToJobSummary {
  param($Job)
  return [ordered]@{
    id = $Job.id
    name = $Job.name
    status = $Job.status
    conclusion = $Job.conclusion
    url = $Job.html_url
    started_at = $Job.started_at
    completed_at = $Job.completed_at
    steps = @($Job.steps | ForEach-Object { Convert-ToStepSummary $_ })
  }
}

function Convert-ToRunSummary {
  param($Run)
  return [ordered]@{
    id = $Run.id
    name = $Run.name
    workflow_id = $Run.workflow_id
    head_branch = $Run.head_branch
    head_sha = $Run.head_sha
    status = $Run.status
    conclusion = $Run.conclusion
    event = $Run.event
    url = $Run.html_url
    created_at = $Run.created_at
    updated_at = $Run.updated_at
  }
}

function Add-JobsToRunSummary {
  param(
    [System.Collections.IDictionary]$Summary,
    [string]$OwnerValue,
    [string]$RepoValue,
    [int64]$RunID
  )
  try {
    $jobsResponse = Invoke-GitHubJobsRequest $OwnerValue $RepoValue $RunID
    $Summary["jobs"] = @($jobsResponse.jobs | ForEach-Object { Convert-ToJobSummary $_ })
  } catch {
    $Summary["jobs_error"] = $_.Exception.Message
  }
  return $Summary
}

function Build-StatusReport {
  param(
    [string]$OwnerValue,
    [string]$RepoValue,
    [string]$BranchValue,
    [string]$ShaValue,
    $Response
  )
  $runs = @($Response.workflow_runs | Where-Object { $_.head_sha -eq $ShaValue })
  $state = "success"
  if ($runs.Count -eq 0) {
    $state = "missing"
  } elseif (@($runs | Where-Object { $_.status -ne "completed" }).Count -gt 0) {
    $state = "pending"
  } elseif (@($runs | Where-Object { $_.conclusion -ne "success" }).Count -gt 0) {
    $state = "failure"
  }
  return [ordered]@{
    status = $state
    repository = "$OwnerValue/$RepoValue"
    branch = $BranchValue
    head_sha = $ShaValue
    remote_branch_sha = $script:RemoteBranchSha
    head_pushed = ($script:RemoteBranchSha -eq $ShaValue)
    checked_at_utc = (Get-Date).ToUniversalTime().ToString("o")
    worktree_dirty = $script:WorktreeDirty
    run_count = $runs.Count
    runs = @($runs | ForEach-Object {
      $summary = Convert-ToRunSummary $_
      if ($IncludeJobs) {
        $summary = Add-JobsToRunSummary $summary $OwnerValue $RepoValue $_.id
      }
      $summary
    })
  }
}

if ([string]::IsNullOrWhiteSpace($Owner) -or [string]::IsNullOrWhiteSpace($Repo)) {
  $resolved = Resolve-GitHubRepository $Remote
  if ([string]::IsNullOrWhiteSpace($Owner)) { $Owner = $resolved.Owner }
  if ([string]::IsNullOrWhiteSpace($Repo)) { $Repo = $resolved.Repo }
}
if ([string]::IsNullOrWhiteSpace($Branch)) {
  $Branch = Invoke-GitValue -GitArgs @("branch", "--show-current")
}
if ([string]::IsNullOrWhiteSpace($Sha)) {
  $Sha = Invoke-GitValue -GitArgs @("rev-parse", "HEAD")
}
if ([string]::IsNullOrWhiteSpace($Branch)) {
  throw "branch is required"
}
if ([string]::IsNullOrWhiteSpace($Sha)) {
  throw "sha is required"
}
if ($IntervalSeconds -lt 5) {
  throw "-IntervalSeconds must be at least 5"
}

$script:WorktreeDirty = Test-WorktreeDirty
$script:RemoteBranchSha = Get-RemoteBranchSha $Remote $Branch
$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
do {
  $response = Invoke-GitHubRunsRequest $Owner $Repo $Branch $Sha
  $report = Build-StatusReport $Owner $Repo $Branch $Sha $response
  if (-not $Wait -or $report.status -eq "success" -or $report.status -eq "failure" -or $report.status -eq "missing" -or (Get-Date) -ge $deadline) {
    break
  }
  Start-Sleep -Seconds $IntervalSeconds
} while ($true)

$report | ConvertTo-Json -Depth 8

if ($script:WorktreeDirty -and -not $AllowDirty) {
  exit 5
}
if ($script:RemoteBranchSha -ne $Sha -and -not $AllowUnpushed) {
  exit 6
}
switch ($report.status) {
  "success" { exit 0 }
  "missing" {
    if ($AllowMissing) { exit 0 }
    exit 2
  }
  "pending" {
    if ($AllowPending) { exit 0 }
    exit 3
  }
  default { exit 4 }
}
