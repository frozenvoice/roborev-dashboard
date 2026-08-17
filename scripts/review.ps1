$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'native-command.ps1')
Push-Location $root
try {
    Invoke-NativeCommand -FilePath 'roborev' -ArgumentList @('review', 'HEAD', '--panel', 'dashboard_cross')
} finally {
    Pop-Location
}
