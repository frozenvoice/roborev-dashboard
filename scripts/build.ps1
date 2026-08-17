$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$goPath = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $goPath) { $goPath = 'C:\Program Files\Go\bin\go.exe' }

Push-Location $root
try {
    npm --prefix web run build
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    & $goPath build -trimpath -ldflags '-s -w' -o dist\roborev-dashboard.exe ./cmd/dashboard
    Write-Host "Built $root\dist\roborev-dashboard.exe"
} finally {
    Pop-Location
}

