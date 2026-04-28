param(
  [string]$Name = "SuperCDN",
  [string]$Binary = "$PSScriptRoot\..\bin\supercdn.exe",
  [string]$Config = "$PSScriptRoot\..\configs\config.local.json"
)

$ErrorActionPreference = "Stop"
$binaryPath = (Resolve-Path $Binary).Path
$configPath = (Resolve-Path $Config).Path
$cmd = "`"$binaryPath`" -config `"$configPath`""
sc.exe create $Name binPath= $cmd start= auto | Write-Host
sc.exe description $Name "Super CDN origin/control service" | Write-Host
Write-Host "Installed service $Name"
