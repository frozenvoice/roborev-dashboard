#Requires -Version 7.0
<#
.SYNOPSIS
    Build one checked-out commit into a Windows Setup and a no-install portable ZIP.

.DESCRIPTION
    package.ps1 owns the verified release executable. The installer and portable
    archive use those exact bytes. No app is installed or started; this script
    does not create a tag or publish a GitHub release.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Version,
    [switch]$VerifyReproducible
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
. (Join-Path $PSScriptRoot 'release-common.ps1')
. (Join-Path $PSScriptRoot 'package-local.ps1')

[void](Assert-StrictSemVer -Value $Version)
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT -or -not [Environment]::Is64BitOperatingSystem) {
    throw 'Windows amd64 is required for both release distributions.'
}

$releaseRoot = Join-Path $root 'dist\release'
$final = Join-Path $releaseRoot $Version
if (Test-Path -LiteralPath $final) { throw "Refusing to overwrite an existing release candidate: $final" }
[void](Assert-SafeExistingParent -Path $releaseRoot)
New-Item -ItemType Directory -Path $releaseRoot -Force | Out-Null
$stage = Join-Path $releaseRoot ('.dual-distribution-' + [guid]::NewGuid().ToString('N'))
$promoted = $false

try {
    # This validates a clean checkout, stamps provenance and checks the three
    # original executable/manifest/hash artifacts.
    & (Join-Path $PSScriptRoot 'package.ps1') -Version $Version -OutputDirectory $stage -VerifyReproducible:$VerifyReproducible

    $exe = Join-Path $stage 'roborev-dashboard.exe'
    $manifestPath = Join-Path $stage 'roborev-dashboard.manifest.json'
    $exeHashPath = Join-Path $stage 'roborev-dashboard.sha256'
    foreach ($path in @($exe, $manifestPath, $exeHashPath)) {
        if (!(Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing release source artifact: $path" }
    }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json -ErrorAction Stop
    if ([string]$manifest.version -cne $Version -or [string]$manifest.target -cne 'windows/amd64' -or
        [string]$manifest.commit -notmatch '^[0-9a-f]{40}$') {
        throw 'Release executable has mismatched version, target or commit metadata.'
    }
    $exeHash = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant()
    if ([string]$manifest.sha256 -cne $exeHash) { throw 'Release manifest executable hash mismatch.' }

    $setup = Invoke-LocalInstallerPackage -Executable $exe -ProjectRoot $root -PackageOutputDirectory $stage -PackageVersion $Version -BuildCommit ([string]$manifest.commit) -ReleasePackage
    if ($setup.Version -cne $Version -or $setup.ExecutableSha256 -cne $exeHash) {
        throw 'Installer does not contain the exact release executable and version.'
    }

    $portableName = "RoboRev-Dashboard-$Version-win-x64-portable.zip"
    $portable = Join-Path $stage $portableName
    if (Test-Path -LiteralPath $portable) { throw "Refusing to overwrite $portable" }
    $zip = [IO.Compression.ZipFile]::Open($portable, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($name in @('roborev-dashboard.exe', 'roborev-dashboard.manifest.json', 'roborev-dashboard.sha256')) {
            [void][IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
                $zip, (Join-Path $stage $name), $name, [IO.Compression.CompressionLevel]::Optimal)
        }
    } finally { $zip.Dispose() }

    $readback = [IO.Compression.ZipFile]::OpenRead($portable)
    try {
        $names = @($readback.Entries | ForEach-Object { $_.FullName } | Sort-Object)
        $expected = @('roborev-dashboard.exe', 'roborev-dashboard.manifest.json', 'roborev-dashboard.sha256')
        if (($names -join '|') -cne ($expected -join '|')) { throw 'Portable ZIP contents are incomplete or unexpected.' }
        $entry = @($readback.Entries | Where-Object { $_.FullName -ceq 'roborev-dashboard.exe' })
        if ($entry.Count -ne 1) { throw 'Portable ZIP must contain one executable.' }
        $stream = $entry[0].Open()
        try { $zipExeHash = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($stream)).ToLowerInvariant() }
        finally { $stream.Dispose() }
    } finally { $readback.Dispose() }
    if ($zipExeHash -cne $exeHash) { throw 'Portable ZIP executable differs from the installer/release executable.' }

    $setupFile = Join-Path $stage 'RoboRev-Dashboard-Setup.exe'
    $releaseAssets = @($portable, $setupFile)
    foreach ($path in $releaseAssets) {
        if (!(Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing release asset: $path" }
    }
    $hashLines = foreach ($file in ($releaseAssets | Sort-Object)) {
        $sha = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant()
        "$sha  $([IO.Path]::GetFileName($file))"
    }
    [IO.File]::WriteAllText((Join-Path $stage 'SHA256SUMS.txt'),
        (($hashLines -join [Environment]::NewLine) + [Environment]::NewLine),
        [Text.UTF8Encoding]::new($false))

    [void](Assert-NoReparseTree -Path $stage)
    if (Test-Path -LiteralPath $final) { throw "Release candidate appeared while building: $final" }
    [IO.Directory]::Move($stage, $final)
    $promoted = $true
    Write-Host "Installer: $(Join-Path $final 'RoboRev-Dashboard-Setup.exe')"
    Write-Host "Portable: $(Join-Path $final $portableName)"
    Write-Host "Checksums: $(Join-Path $final 'SHA256SUMS.txt')"
    Write-Host 'Upload Setup.exe and portable.zip as TWO assets of ONE GitHub Release after Windows verification.'
} finally {
    if (!$promoted -and (Test-Path -LiteralPath $stage)) {
        [void](Assert-NoReparseTree -Path $stage)
        Remove-Item -LiteralPath $stage -Recurse -Force
    }
}
