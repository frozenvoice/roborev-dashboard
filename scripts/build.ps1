$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')
$tooling = Assert-DevelopmentPrerequisites -RootPath $root

Push-Location $root
try {
    Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'run', 'build')
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    Invoke-NativeCommand -FilePath $tooling.GoPath -ArgumentList @('build', '-trimpath', '-ldflags', '-s -w', '-o', 'dist\roborev-dashboard.exe', './cmd/dashboard')
    Write-Host "Built $root\dist\roborev-dashboard.exe"
} finally {
    Pop-Location
}
