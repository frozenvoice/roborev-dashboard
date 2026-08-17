$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'native-command.ps1')
$goPath = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $goPath) { $goPath = 'C:\Program Files\Go\bin\go.exe' }

Push-Location $root
try {
    Invoke-NativeCommand -FilePath $goPath -ArgumentList @('test', './...')
    Invoke-NativeCommand -FilePath 'npm' -ArgumentList @('--prefix', 'web', 'test', '--', '--run')
    Invoke-NativeCommand -FilePath 'npm' -ArgumentList @('--prefix', 'web', 'run', 'build')
} finally {
    Pop-Location
}
