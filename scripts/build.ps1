$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')
$tooling = Assert-DevelopmentPrerequisites -RootPath $root

Push-Location $root
try {
    Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'run', 'build')
    New-Item -ItemType Directory -Force -Path dist | Out-Null
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
    Invoke-NativeCommand -FilePath $tooling.GoPath -ArgumentList @('build', '-trimpath', '-buildvcs=false', '-ldflags', $ldflags, '-o', 'dist\roborev-dashboard.exe', './cmd/dashboard')
    Write-Host "Built $root\dist\roborev-dashboard.exe"
} finally {
    Pop-Location
}
