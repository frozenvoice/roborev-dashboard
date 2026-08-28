$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')

# Keep the bootstrap contract itself deterministic and independently testable.
& (Join-Path $PSScriptRoot 'prerequisites.tests.ps1')
& (Join-Path $PSScriptRoot 'package.tests.ps1')
$tooling = Assert-DevelopmentPrerequisites -RootPath $root

Push-Location $root
try {
    # Go package tests exercise the embedded frontend handler, so build the
    # production assets before running them.  Keep one actual build here;
    # release reproducibility is covered by package.ps1.
    Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'run', 'build')
    Invoke-NativeCommand -FilePath $tooling.GoPath -ArgumentList @('test', './...')
    Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'test', '--', '--run')
} finally {
    Pop-Location
}
