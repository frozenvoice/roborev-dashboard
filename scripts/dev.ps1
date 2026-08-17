$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    $goPath = 'C:\Program Files\Go\bin\go.exe'
    if (-not (Test-Path -LiteralPath $goPath)) { throw 'Go is required. Install it from https://go.dev/dl/' }
    $go = Get-Item -LiteralPath $goPath
}

Push-Location $root
try {
    npm --prefix web run build
    & $go.Source run ./cmd/dashboard @args
} finally {
    Pop-Location
}

