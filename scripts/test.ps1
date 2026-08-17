$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$goPath = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $goPath) { $goPath = 'C:\Program Files\Go\bin\go.exe' }

Push-Location $root
try {
    & $goPath test ./...
    npm --prefix web test -- --run
    npm --prefix web run build
} finally {
    Pop-Location
}

