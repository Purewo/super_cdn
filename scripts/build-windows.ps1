$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path ".\bin" | Out-Null
go build -o .\bin\supercdn.exe .\cmd\supercdn
go build -o .\bin\supercdnctl.exe .\cmd\supercdnctl
Write-Host "Built .\bin\supercdn.exe and .\bin\supercdnctl.exe"
