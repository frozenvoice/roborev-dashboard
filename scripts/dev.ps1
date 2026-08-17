$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$goCommand = Get-Command go -ErrorAction SilentlyContinue
if ($goCommand) {
    $goPath = $goCommand.Source
} else {
    $goPath = 'C:\Program Files\Go\bin\go.exe'
    if (-not (Test-Path -LiteralPath $goPath)) { throw 'Go is required. Install it from https://go.dev/dl/' }
}

Push-Location $root
try {
    npm --prefix web run build
    & $goPath run ./cmd/dashboard @args
} finally {
    Pop-Location
}
