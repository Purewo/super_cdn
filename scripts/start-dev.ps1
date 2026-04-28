param(
  [string]$Config = "configs/config.local.json"
)

$ErrorActionPreference = "Stop"
$env:SUPERCDN_CONFIG = $Config
go run ./cmd/supercdn -config $Config
