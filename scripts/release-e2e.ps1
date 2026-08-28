[CmdletBinding()]
param(
    [Parameter()]
    [string]$ArtifactPath,

    [Parameter()]
    [string]$Version
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')
if ([string]::IsNullOrWhiteSpace($ArtifactPath)) {
    $ArtifactPath = Join-Path $root 'dist\release\0.0.0-releasecheck.1\roborev-dashboard.exe'
} elseif (-not [IO.Path]::IsPathRooted($ArtifactPath)) {
    $ArtifactPath = Join-Path $root $ArtifactPath
}
$artifact = (Resolve-Path -LiteralPath $ArtifactPath -ErrorAction Stop).Path
if (-not (Test-Path -LiteralPath $artifact -PathType Leaf)) {
    throw "Release E2E artifact is not a regular file: $artifact"
}

# The Go harness performs the strict provenance/version and temporary-environment
# checks. Keep this entry point thin so child environment state is restored even
# if the test process fails.
$oldArtifact = [Environment]::GetEnvironmentVariable('RELEASE_E2E_ARTIFACT_PATH', 'Process')
$oldVersion = [Environment]::GetEnvironmentVariable('RELEASE_E2E_EXPECTED_VERSION', 'Process')
$tooling = $null
Push-Location $root
try {
	$tooling = Assert-DevelopmentPrerequisites -RootPath $root -SkipFrontendShims
	$env:RELEASE_E2E_ARTIFACT_PATH = $artifact
    if ([string]::IsNullOrWhiteSpace($Version)) {
        Remove-Item Env:RELEASE_E2E_EXPECTED_VERSION -ErrorAction SilentlyContinue
    } else {
        $env:RELEASE_E2E_EXPECTED_VERSION = $Version
    }
	Invoke-NativeCommand -FilePath $tooling.GoPath -ArgumentList @('test', './internal/releasee2e', '-count=1', '-run', '^TestReleaseArtifactE2E$')
} finally {
    if ($null -eq $oldArtifact) {
        Remove-Item Env:RELEASE_E2E_ARTIFACT_PATH -ErrorAction SilentlyContinue
    } else {
        $env:RELEASE_E2E_ARTIFACT_PATH = $oldArtifact
    }
    if ($null -eq $oldVersion) {
        Remove-Item Env:RELEASE_E2E_EXPECTED_VERSION -ErrorAction SilentlyContinue
	} else {
		$env:RELEASE_E2E_EXPECTED_VERSION = $oldVersion
	}
	Pop-Location
}
