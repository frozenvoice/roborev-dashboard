$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')
$tooling = Assert-DevelopmentPrerequisites -RootPath $root

Push-Location $root
try {
    Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'run', 'build')
    Invoke-NativeCommand -FilePath $tooling.GoPath -ArgumentList (@('run', './cmd/dashboard') + $args)
} finally {
    Pop-Location
}
