param(
  [string]$Server = "",
  [string]$Token = "",
  [string]$Profile = "",
  [string]$Site = "",
  [string]$Deployment = "",
  [string]$SitePath = "",
  [string]$Bucket = "",
  [string]$BucketPath = "",
  [string]$PublicUrl = "",
  [string]$SpaPath = "",
  [string]$Resolver = "1.1.1.1:53",
  [int]$MaxAssets = 20,
  [switch]$RequireEdgeStaticHtml,
  [switch]$RequireEdgeManifestAssets,
  [switch]$RequireDirectAssets,
  [switch]$RequireBrowserRender,
  [switch]$SkipDoctor,
  [switch]$UseGoRun,
  [int]$Retries = 1,
  [string]$OutputPath = ""
)

$ErrorActionPreference = "Stop"

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path

function First-NonEmpty {
  param([string[]]$Values)
  foreach ($value in $Values) {
    if (-not [string]::IsNullOrWhiteSpace($value)) {
      return $value
    }
  }
  return ""
}

function Quote-CommandArg {
  param([string]$Value)
  if ($Value -notmatch "[\s'`"]") {
    return $Value
  }
  return "'" + ($Value -replace "'", "''") + "'"
}

function Format-CommandLine {
  param(
    [string]$File,
    [string[]]$ArgumentList
  )
  $parts = @($File)
  foreach ($arg in $ArgumentList) {
    $parts += (Quote-CommandArg $arg)
  }
  return ($parts -join " ")
}

function Redact-CommandArgs {
  param([string[]]$CommandArgs)
  $out = @()
  $redactNext = $false
  foreach ($arg in $CommandArgs) {
    if ($redactNext) {
      $out += "<redacted>"
      $redactNext = $false
      continue
    }
    $out += $arg
    if ($arg -eq "-token") {
      $redactNext = $true
    }
  }
  return $out
}

function Convert-JsonOrText {
  param([string]$Value)
  if ([string]::IsNullOrWhiteSpace($Value)) {
    return $null
  }
  try {
    return ($Value | ConvertFrom-Json -ErrorAction Stop)
  } catch {
    return $Value.Trim()
  }
}

function Resolve-CLIInvocation {
  param([string[]]$CommandArgs)
  $bin = Join-Path $RepoRoot "bin\supercdnctl.exe"
  if (-not $UseGoRun -and (Test-Path -LiteralPath $bin)) {
    return [ordered]@{
      File = $bin
      Args = $CommandArgs
    }
  }
  return [ordered]@{
    File = "go"
    Args = @("run", ".\cmd\supercdnctl", "--") + $CommandArgs
  }
}

function Build-GlobalArgs {
  $args = @()
  $effectiveServer = First-NonEmpty @($Server, $env:SUPERCDN_URL)
  $effectiveToken = First-NonEmpty @($Token, $env:SUPERCDN_TOKEN)
  $effectiveProfile = First-NonEmpty @($Profile, $env:SUPERCDN_PROFILE)
  if (-not [string]::IsNullOrWhiteSpace($effectiveServer)) {
    $args += @("-server", $effectiveServer)
  }
  if (-not [string]::IsNullOrWhiteSpace($effectiveToken)) {
    $args += @("-token", $effectiveToken)
  }
  if (-not [string]::IsNullOrWhiteSpace($effectiveProfile)) {
    $args += @("-profile", $effectiveProfile)
  }
  return $args
}

function Has-AuthContext {
  $effectiveToken = First-NonEmpty @($Token, $env:SUPERCDN_TOKEN)
  $effectiveProfile = First-NonEmpty @($Profile, $env:SUPERCDN_PROFILE)
  return (-not [string]::IsNullOrWhiteSpace($effectiveToken)) -or (-not [string]::IsNullOrWhiteSpace($effectiveProfile))
}

function Invoke-CLIRegressionStep {
  param(
    [string]$Name,
    [string[]]$CommandArgs,
    [switch]$RequiresAuth
  )
  if ($RequiresAuth -and -not (Has-AuthContext)) {
    return [ordered]@{
      name = $Name
      status = "skipped"
      reason = "token or profile is required"
      command = "supercdnctl " + ($CommandArgs -join " ")
    }
  }

  $allArgs = (Build-GlobalArgs) + $CommandArgs
  $invocation = Resolve-CLIInvocation $allArgs
  $attempts = @()
  $started = Get-Date
  $ended = $started
  $output = ""
  $exitCode = 1
  $maxAttempts = [Math]::Max(1, $Retries + 1)
  for ($attempt = 1; $attempt -le $maxAttempts; $attempt++) {
    $attemptStarted = Get-Date
    $attemptOutput = ""
    $attemptExitCode = 0
    Push-Location $RepoRoot
    try {
      $lines = & $invocation.File @($invocation.Args) 2>&1
      $attemptExitCode = $LASTEXITCODE
      $attemptOutput = ($lines | Out-String)
    } catch {
      $attemptExitCode = 1
      $attemptOutput = $_.Exception.Message
    } finally {
      Pop-Location
    }
    $attemptEnded = Get-Date
    $attemptRecord = [ordered]@{
      attempt = $attempt
      exit_code = $attemptExitCode
      duration_ms = [int64]($attemptEnded - $attemptStarted).TotalMilliseconds
    }
    if ($attemptExitCode -ne 0 -and -not [string]::IsNullOrWhiteSpace($attemptOutput)) {
      $attemptRecord["error"] = $attemptOutput.Trim()
    }
    $attempts += $attemptRecord
    $output = $attemptOutput
    $exitCode = $attemptExitCode
    $ended = $attemptEnded
    if ($attemptExitCode -eq 0) {
      break
    }
    if ($attempt -lt $maxAttempts) {
      Start-Sleep -Seconds 2
    }
  }
  $status = if ($exitCode -eq 0) { "ok" } else { "failed" }
  $step = [ordered]@{
    name = $Name
    status = $status
    exit_code = $exitCode
    command = Format-CommandLine $invocation.File (Redact-CommandArgs $invocation.Args)
    started_at_utc = $started.ToUniversalTime().ToString("o")
    duration_ms = [int64]($ended - $started).TotalMilliseconds
  }
  if ($attempts.Count -gt 1) {
    $step["attempts"] = $attempts
  }
  $parsedOutput = Convert-JsonOrText $output
  if ($null -ne $parsedOutput) {
    if ($exitCode -eq 0) {
      $step["stdout"] = $parsedOutput
    } else {
      $step["stderr"] = $parsedOutput
    }
  }
  return $step
}

function Add-ProbeRequirements {
  param([string[]]$CommandArgs)
  $out = @($CommandArgs)
  if (-not [string]::IsNullOrWhiteSpace($SpaPath)) {
    $out += @("-spa-path", $SpaPath)
  }
  if (-not [string]::IsNullOrWhiteSpace($Resolver)) {
    $out += @("-resolver", $Resolver)
  }
  if ($MaxAssets -gt 0) {
    $out += @("-max-assets", [string]$MaxAssets)
  }
  if ($RequireEdgeStaticHtml) {
    $out += "-require-edge-static-html"
  }
  if ($RequireEdgeManifestAssets) {
    $out += "-require-edge-manifest-assets"
  }
  if ($RequireDirectAssets) {
    $out += "-require-direct-assets"
  }
  if ($RequireBrowserRender) {
    $out += "-browser-render"
  }
  return $out
}

$steps = @()

if (-not $SkipDoctor) {
  $steps += Invoke-CLIRegressionStep "doctor" @("doctor") -RequiresAuth
}

if (-not [string]::IsNullOrWhiteSpace($Bucket)) {
  $cmdArgs = @("cdn-doctor", "-bucket", $Bucket)
  if (-not [string]::IsNullOrWhiteSpace($BucketPath)) {
    $cmdArgs += @("-path", $BucketPath)
  }
  $steps += Invoke-CLIRegressionStep "bucket doctor" $cmdArgs -RequiresAuth
}

if (-not [string]::IsNullOrWhiteSpace($Site)) {
  $cmdArgs = @("site-doctor", "-site", $Site)
  if (-not [string]::IsNullOrWhiteSpace($SitePath)) {
    $cmdArgs += @("-path", $SitePath)
  }
  $steps += Invoke-CLIRegressionStep "site doctor" $cmdArgs -RequiresAuth

  $probeArgs = @("probe-site", "-site", $Site)
  if (-not [string]::IsNullOrWhiteSpace($Deployment)) {
    $probeArgs += @("-deployment", $Deployment)
  }
  $probeArgs = Add-ProbeRequirements -CommandArgs $probeArgs
  $steps += Invoke-CLIRegressionStep "site probe" $probeArgs -RequiresAuth
}

if (-not [string]::IsNullOrWhiteSpace($Site) -and -not [string]::IsNullOrWhiteSpace($Deployment)) {
  $cmdArgs = @("reconcile-deployment", "-site", $Site, "-deployment", $Deployment)
  if (-not [string]::IsNullOrWhiteSpace($SpaPath)) {
    $cmdArgs += @("-spa-path", $SpaPath)
  }
  if (-not [string]::IsNullOrWhiteSpace($Resolver)) {
    $cmdArgs += @("-resolver", $Resolver)
  }
  if ($MaxAssets -gt 0) {
    $cmdArgs += @("-max-assets", [string]$MaxAssets)
  }
  $steps += Invoke-CLIRegressionStep "deployment reconcile" $cmdArgs -RequiresAuth
}

if (-not [string]::IsNullOrWhiteSpace($PublicUrl)) {
  $cmdArgs = Add-ProbeRequirements -CommandArgs @("probe-site", "-url", $PublicUrl)
  $steps += Invoke-CLIRegressionStep "public URL probe" $cmdArgs
}

if ($steps.Count -eq 0) {
  $steps += [ordered]@{
    name = "input"
    status = "skipped"
    reason = "provide -PublicUrl, -Site, -Deployment, -Bucket or auth context for a useful regression"
  }
}

$failed = @($steps | Where-Object { $_.status -eq "failed" })
$skipped = @($steps | Where-Object { $_.status -eq "skipped" })
$report = [ordered]@{
  status = if ($failed.Count -gt 0) { "failed" } else { "ok" }
  checked_at_utc = (Get-Date).ToUniversalTime().ToString("o")
  repository = $RepoRoot
  read_only = $true
  server = First-NonEmpty @($Server, $env:SUPERCDN_URL)
  site = $Site
  deployment = $Deployment
  site_path = $SitePath
  bucket = $Bucket
  bucket_path = $BucketPath
  public_url = $PublicUrl
  spa_path = $SpaPath
  resolver = $Resolver
  summary = [ordered]@{
    steps = $steps.Count
    failed = $failed.Count
    skipped = $skipped.Count
  }
  steps = $steps
}

$json = $report | ConvertTo-Json -Depth 80
if (-not [string]::IsNullOrWhiteSpace($OutputPath)) {
  $target = if ([System.IO.Path]::IsPathRooted($OutputPath)) { $OutputPath } else { Join-Path $RepoRoot $OutputPath }
  $parent = Split-Path -Parent $target
  if (-not [string]::IsNullOrWhiteSpace($parent)) {
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
  }
  Set-Content -LiteralPath $target -Value $json -Encoding utf8
}
$json
if ($failed.Count -gt 0) {
  exit 1
}
