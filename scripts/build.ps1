[CmdletBinding()]
param(
    # Local builds use a per-run staging path so a running development or
    # installed Dashboard is never replaced while its process has the file
    # open.  The default remains dist\roborev-dashboard.exe for existing users.
    [string]$OutputPath,

    # test.ps1 already performs the production frontend build.  Local
    # orchestration can reuse those embedded assets and avoid building twice.
    [switch]$SkipFrontendBuild
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')
$tooling = Assert-DevelopmentPrerequisites -RootPath $root

Push-Location $root
try {
    if (-not $SkipFrontendBuild) {
        Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'run', 'build')
    }
    if ([string]::IsNullOrWhiteSpace($OutputPath)) {
        $destination = Join-Path $root 'dist\roborev-dashboard.exe'
    } elseif ([IO.Path]::IsPathRooted($OutputPath)) {
        $destination = [IO.Path]::GetFullPath($OutputPath)
    } else {
        $destination = [IO.Path]::GetFullPath((Join-Path $root $OutputPath))
    }
    if ([IO.Path]::GetExtension($destination) -ine '.exe') {
        throw "Build output must be an .exe path: $destination"
    }
    $destinationParent = Split-Path -Parent $destination
    if ([string]::IsNullOrWhiteSpace($destinationParent)) { throw "Build output has no parent directory: $destination" }
    New-Item -ItemType Directory -Force -Path $destinationParent | Out-Null
    $buildCommit = 'unknown'
    try {
        $candidateCommit = (& $tooling.GitPath @('rev-parse', 'HEAD') 2>$null | Out-String).Trim()
        if ($LASTEXITCODE -eq 0 -and $candidateCommit -match '^[0-9a-f]{40}$') {
            $buildCommit = $candidateCommit
        }
    } catch {
        # A developer build remains useful when Git metadata is unavailable.
    }
    $ldflags = '-s -w -X github.com/froze/roborev-dashboard/internal/buildinfo.commit=' + $buildCommit + ' -X github.com/froze/roborev-dashboard/internal/buildinfo.version=dev -X github.com/froze/roborev-dashboard/internal/buildinfo.release=false'
    Invoke-NativeCommand -FilePath $tooling.GoPath -ArgumentList @('build', '-trimpath', '-buildvcs=false', '-ldflags', $ldflags, '-o', $destination, './cmd/dashboard')
    Write-Host "Built $destination"
} finally {
    Pop-Location
}
