param(
  [string]$Config = "configs/config.local.json",
  [string]$ServerUrl = "http://127.0.0.1:8080",
  [switch]$Full,
  [switch]$SkipWorker,
  [switch]$SkipLinuxBuild,
  [string]$LiveSiteUrl = "",
  [string]$SpaPath = ""
)

$ErrorActionPreference = "Stop"

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$ConfigPath = if ([System.IO.Path]::IsPathRooted($Config)) { $Config } else { Join-Path $RepoRoot $Config }

function Invoke-Step {
  param(
    [string]$Name,
    [scriptblock]$Block
  )
  Write-Host ""
  Write-Host "==> $Name"
  & $Block
}

function Invoke-External {
  param(
    [string]$Name,
    [string]$File,
    [string[]]$ArgumentList,
    [string]$WorkingDirectory = $RepoRoot
  )
  Invoke-Step $Name {
    Push-Location $WorkingDirectory
    try {
      & $File @ArgumentList
      if ($LASTEXITCODE -ne 0) {
        throw "$File exited with code $LASTEXITCODE"
      }
    } finally {
      Pop-Location
    }
  }
}

function Test-Healthz {
  param([string]$BaseUrl)
  try {
    $resp = Invoke-WebRequest -Uri ($BaseUrl.TrimEnd("/") + "/healthz") -UseBasicParsing -TimeoutSec 2
    return $resp.StatusCode -eq 200
  } catch {
    return $false
  }
}

function Read-AdminToken {
  param([string]$Path)
  if (-not (Test-Path -LiteralPath $Path)) {
    return ""
  }
  $cfg = Get-Content -Raw -LiteralPath $Path | ConvertFrom-Json
  return [string]$cfg.server.admin_token
}

Invoke-Step "gofmt check" {
  Push-Location $RepoRoot
  try {
    $files = @(gofmt -l cmd internal)
    if ($files.Count -gt 0) {
      $files | ForEach-Object { Write-Host $_ }
      throw "gofmt found unformatted files"
    }
  } finally {
    Pop-Location
  }
}

Invoke-External "go test ./..." "go" @("test", "./...")
Invoke-External "go vet ./..." "go" @("vet", "./...")
Invoke-External "build Windows server" "go" @("build", "-o", ".\bin\supercdn.exe", ".\cmd\supercdn")
Invoke-External "build Windows CLI" "go" @("build", "-o", ".\bin\supercdnctl.exe", ".\cmd\supercdnctl")

if (-not $SkipLinuxBuild) {
  Invoke-Step "build Linux amd64 binaries" {
    Push-Location $RepoRoot
    $oldGOOS = $env:GOOS
    $oldGOARCH = $env:GOARCH
    try {
      $env:GOOS = "linux"
      $env:GOARCH = "amd64"
      & go build -o .\bin\supercdn-linux-amd64 .\cmd\supercdn
      if ($LASTEXITCODE -ne 0) { throw "go build server linux exited with code $LASTEXITCODE" }
      & go build -o .\bin\supercdnctl-linux-amd64 .\cmd\supercdnctl
      if ($LASTEXITCODE -ne 0) { throw "go build CLI linux exited with code $LASTEXITCODE" }
    } finally {
      if ($null -eq $oldGOOS) { Remove-Item Env:\GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGOOS }
      if ($null -eq $oldGOARCH) { Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGOARCH }
      Pop-Location
    }
  }
}

if (-not $SkipWorker) {
  Invoke-External "worker npm test" "npm" @("test") (Join-Path $RepoRoot "worker")
  Invoke-External "worker TypeScript check" "npx" @("tsc", "--noEmit") (Join-Path $RepoRoot "worker")
}

$serverProcess = $null
$serverStarted = $false
$stdout = Join-Path $env:TEMP ("supercdn-foundation-{0}.out.log" -f $PID)
$stderr = Join-Path $env:TEMP ("supercdn-foundation-{0}.err.log" -f $PID)

try {
  if (Test-Path -LiteralPath $ConfigPath) {
    Invoke-Step "service healthz" {
      if (Test-Healthz $ServerUrl) {
        Write-Host "existing server healthz ok"
        return
      }
      $exe = Join-Path $RepoRoot "bin\supercdn.exe"
      $serverProcess = Start-Process -FilePath $exe -ArgumentList @("-config", $ConfigPath) -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr
      $serverStarted = $true
      $ready = $false
      for ($i = 0; $i -lt 30; $i++) {
        if ($serverProcess.HasExited) {
          $out = if (Test-Path -LiteralPath $stdout) { Get-Content -Raw -LiteralPath $stdout } else { "" }
          $err = if (Test-Path -LiteralPath $stderr) { Get-Content -Raw -LiteralPath $stderr } else { "" }
          if ($out) { Write-Host $out }
          if ($err) { Write-Host $err }
          throw "server exited with code $($serverProcess.ExitCode)"
        }
        if (Test-Healthz $ServerUrl) {
          $ready = $true
          break
        }
        Start-Sleep -Milliseconds 500
      }
      if (-not $ready) {
        throw "server did not become ready at $ServerUrl"
      }
      Write-Host "healthz ok"
    }

    if ($Full) {
      $adminToken = Read-AdminToken $ConfigPath
      if ([string]::IsNullOrWhiteSpace($adminToken)) {
        throw "admin token is required for -Full checks"
      }
      Invoke-External "cloudflare status" (Join-Path $RepoRoot "bin\supercdnctl.exe") @("-server", $ServerUrl, "-token", $adminToken, "cloudflare-status", "-all")
      Invoke-External "overseas_accel write probe" (Join-Path $RepoRoot "bin\supercdnctl.exe") @("-server", $ServerUrl, "-token", $adminToken, "health-check", "-libraries", "overseas_accel", "-write-probe", "-force")
      Invoke-External "overseas_r2 e2e probe" (Join-Path $RepoRoot "bin\supercdnctl.exe") @("-server", $ServerUrl, "-token", $adminToken, "e2e-probe", "-profile", "overseas_r2")
    }
  } else {
    Write-Host ""
    Write-Host "==> service healthz"
    Write-Host "skipped: config not found at $ConfigPath"
  }

  if (-not [string]::IsNullOrWhiteSpace($LiveSiteUrl)) {
    $probeArgs = @("probe-site", "-url", $LiveSiteUrl, "-max-assets", "20")
    if (-not [string]::IsNullOrWhiteSpace($SpaPath)) {
      $probeArgs += @("-spa-path", $SpaPath)
    }
    Invoke-External "live site probe" (Join-Path $RepoRoot "bin\supercdnctl.exe") $probeArgs
  }
} finally {
  if ($serverStarted -and $serverProcess -and -not $serverProcess.HasExited) {
    Stop-Process -Id $serverProcess.Id -Force
  }
  Remove-Item -LiteralPath $stdout, $stderr -Force -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Foundation check passed."
