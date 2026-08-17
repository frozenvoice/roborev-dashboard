$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'native-command.ps1')
$goPath = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $goPath) { $goPath = 'C:\Program Files\Go\bin\go.exe' }

Push-Location $root
try {
    Invoke-NativeCommand -FilePath 'npm' -ArgumentList @('--prefix', 'web', 'run', 'build')
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    Invoke-NativeCommand -FilePath $goPath -ArgumentList @('build', '-trimpath', '-ldflags', '-s -w', '-o', 'dist\roborev-dashboard.exe', './cmd/dashboard')
    Write-Host "Built $root\dist\roborev-dashboard.exe"
} finally {
    Pop-Location
}
