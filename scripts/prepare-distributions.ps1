#Requires -Version 7.0
<#
.SYNOPSIS
Build one verified official executable into Setup and a deterministic portable ZIP.
.DESCRIPTION
Creates an absent candidate atomically. Does not install, tag, or publish a release.
#>
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Version, [string]$OutputDirectory, [switch]$VerifyReproducible)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$distributionVersion = $Version
$distributionOutput = $OutputDirectory
$distributionReproducible = $VerifyReproducible
. (Join-Path $PSScriptRoot 'package-local.ps1')
. (Join-Path $PSScriptRoot 'distribution-common.ps1')
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT -or -not [Environment]::Is64BitOperatingSystem) {
    throw 'Windows amd64 is required for both release distributions.'
}
$result = Invoke-DistributionPreparation -ProjectRoot (Join-Path $PSScriptRoot '..') -DistributionVersion $distributionVersion -Destination $distributionOutput -VerifyReproducible:$distributionReproducible
Write-Host "Installer: $($result.Installer)"
Write-Host "Portable: $($result.Portable)"
Write-Host "Checksums: $(Join-Path $result.Directory 'SHA256SUMS.txt')"
Write-Host 'Publish both distribution files in one GitHub Release only after Windows verification and explicit approval.'
