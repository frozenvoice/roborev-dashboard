$ErrorActionPreference = 'Stop'
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $scriptRoot 'release-common.ps1')

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "Assertion failed: $Message" }
}

function Assert-Equal {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -cne $Expected) { throw "Assertion failed: $Message (actual '$Actual', expected '$Expected')" }
}

foreach ($valid in @('0.0.0', '1.2.3', '10.20.30-alpha.1', '1.2.3+build.7', '1.2.3-rc.1+build.7')) {
    Assert-True (Test-StrictSemVer -Value $valid) "valid SemVer $valid"
}
foreach ($invalid in @('', 'dev', 'v1.2.3', '01.2.3', '1.2', '1.2.3-', '1.2.3-01', '1.2.3+', '1.2.3/unsafe', '1.2.3-rc_1', ' 1.2.3')) {
    Assert-True (-not (Test-StrictSemVer -Value $invalid)) "invalid SemVer $invalid"
}

$commit = [string]::new('a', 40)
$sha = [string]::new('b', 64)
$manifest = New-ReleaseManifest -Version '0.0.0-releasecheck.1' -Commit $commit -Size 17 -Sha256 $sha
$manifestJson = ConvertTo-DeterministicJsonText -Value $manifest
Assert-Equal $manifestJson "{`"name`":`"roborev-dashboard`",`"version`":`"0.0.0-releasecheck.1`",`"commit`":`"$commit`",`"target`":`"windows/amd64`",`"roborevCompatibility`":`"v0.64.0`",`"size`":17,`"sha256`":`"$sha`",`"signed`":false}`n" 'manifest field order and JSON'
Assert-True (-not $manifestJson.Contains("`r")) 'manifest uses LF line ending'
Assert-Equal (ConvertTo-DeterministicJsonText -Value @('one')) "[`"one`"]`n" 'single-element array topology is preserved'
$expectedManifestProperties = @('name', 'version', 'commit', 'target', 'roborevCompatibility', 'size', 'sha256', 'signed')
Assert-True (Assert-ExactPropertySet -Value $manifest -Expected $expectedManifestProperties) 'manifest has exact property set'
$unknownManifest = [pscustomobject]([ordered]@{
    name = 'roborev-dashboard'; version = '0.0.0-releasecheck.1'; commit = $commit; target = 'windows/amd64'
    roborevCompatibility = 'v0.64.0'; size = 17; sha256 = $sha; signed = $false; unexpected = 'reject'
})
$unknownRejected = $false
try { Assert-ExactPropertySet -Value $unknownManifest -Expected $expectedManifestProperties | Out-Null } catch { $unknownRejected = $true }
Assert-True $unknownRejected 'manifest unknown property is rejected'

$tempRoot = Join-Path ([IO.Path]::GetTempPath()) ('roborev-dashboard-package-tests-' + [guid]::NewGuid().ToString('N'))
try {
    $repoRoot = Join-Path $tempRoot 'repo'
    $frontend = Join-Path $repoRoot 'internal\webui\dist'
    New-Item -ItemType Directory -Path $frontend -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $frontend 'placeholder.txt'), 'keep')
    New-Item -ItemType Directory -Path (Join-Path $frontend 'assets') -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $frontend 'assets\old.js'), 'old')
    Remove-GeneratedFrontendAssets -RepositoryRoot $repoRoot -DistPath $frontend
    Assert-True (Test-Path -LiteralPath (Join-Path $frontend 'placeholder.txt') -PathType Leaf) 'tracked placeholder is preserved'
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $frontend 'assets'))) 'stale frontend assets are removed'

    [IO.File]::WriteAllText((Join-Path $frontend 'index.html'), 'index')
    [IO.File]::WriteAllText((Join-Path $frontend 'main.js'), 'main')
    $files = @(Get-DirectoryFileManifest -Path $frontend)
    Assert-Equal $files.Count 3 'frontend manifest includes files'
    Assert-Equal $files[0].path 'index.html' 'frontend manifest is sorted by relative path'
    Assert-Equal $files[1].path 'main.js' 'frontend manifest preserves relative names'
    Assert-Equal $files[2].path 'placeholder.txt' 'frontend manifest includes placeholder'
    Assert-True ($files[1].sha256 -match '^[0-9a-f]{64}$') 'frontend manifest hash is lowercase SHA-256'

    $hashPath = Join-Path $tempRoot 'hash.txt'
    [IO.File]::WriteAllText($hashPath, 'abc')
    Assert-Equal (Get-FileSha256Lower -Path $hashPath) 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad' 'SHA-256 helper'
    Assert-True (Test-PathWithin -Path (Join-Path $tempRoot 'repo\file') -Root $repoRoot) 'path containment'
    Assert-True (-not (Test-PathWithin -Path (Join-Path $tempRoot 'repo-sibling') -Root $repoRoot)) 'path boundary'
    $safeNearest = Assert-SafeExistingParent -Path (Join-Path $repoRoot 'new\nested\output')
    Assert-Equal $safeNearest $repoRoot 'safe parent resolves through all missing descendants'

    $utfPath = Join-Path $tempRoot 'manifest.json'
    Write-Utf8NoBom -Path $utfPath -Content $manifestJson
    $bytes = [IO.File]::ReadAllBytes($utfPath)
    Assert-True ($bytes.Length -gt 3 -and $bytes[0] -ne 0xEF -and $bytes[0] -ne 0xFF) 'UTF-8 output has no BOM'
} finally {
    if (Test-Path -LiteralPath $tempRoot) {
        $resolvedTemp = (Resolve-Path -LiteralPath $tempRoot -ErrorAction Stop).Path
        if (-not $resolvedTemp.StartsWith(([IO.Path]::GetTempPath()).TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing to clean an unexpected package test path.' }
        Remove-Item -LiteralPath $resolvedTemp -Recurse -Force -ErrorAction Stop
    }
}

Write-Host 'Release package helper tests passed.'
