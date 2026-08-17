$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    roborev review HEAD --panel dashboard_cross
} finally {
    Pop-Location
}

