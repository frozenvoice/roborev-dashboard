<#!
.SYNOPSIS
    Compile the current local Dashboard executable into the per-user Setup.exe.

The script is deliberately dot-sourceable.  Direct invocation accepts a
payload/executable and writes only a newly compiled installer plus its hash
sidecar.  The caller decides when it is safe to launch Setup; this script never
installs, uninstalls, stops, or starts the Dashboard.
#>
[CmdletBinding()]
param(
    [Parameter()]
    [string]$PayloadDirectory,

    [Parameter()]
    [string]$ExecutablePath,

    [Parameter()]
    [string]$SourceRoot,

    [Parameter()]
    [string]$OutputDirectory,

    [Parameter()]
    [string]$Version,

    [Parameter()]
    [string]$Commit = 'unknown',

    [Parameter()]
    [string]$ToolCacheDirectory,

    [Parameter()]
    [string]$ResultPath,

    [Parameter()]
    [switch]$KeepStaging,
    # Retain the exact release version instead of appending the development -local label.
    [switch]$ReleasePackage
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# Reuse the release path and SemVer guards.  This keeps the local package
# entry point subject to the same ancestor/reparse checks as the release
# package helper while remaining safe to dot-source from focused tests.
. (Join-Path $PSScriptRoot 'release-common.ps1')

$script:LocalInstallerArtifactName = 'RoboRev-Dashboard-Setup.exe'
$script:LocalDashboardExecutableName = 'roborev-dashboard.exe'

function Get-LocalFullPath {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)
    try { return [IO.Path]::GetFullPath($Path) } catch { throw "Invalid path: $Path" }
}

function Assert-LocalDirectory {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)
    $full = Get-LocalFullPath -Path $Path
    if (-not (Test-Path -LiteralPath $full -PathType Container)) { throw "Directory does not exist: $full" }
    [void](Assert-SafeExistingParent -Path $full)
    [void](Assert-NoReparsePoint -Path $full)
    return $full
}

function Assert-LocalParent {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)
    $full = Get-LocalFullPath -Path $Path
    $parent = Split-Path -Parent $full
    if ([string]::IsNullOrWhiteSpace($parent)) { throw "Path has no parent: $full" }
    [void](Assert-SafeExistingParent -Path $parent)
    if (-not (Test-Path -LiteralPath $parent -PathType Container)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    [void](Assert-SafeExistingParent -Path $parent)
    return $full
}

function Test-LocalSemVer {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
    return Test-StrictSemVer -Value $Value
}

function Get-LocalVersionFromPackageJson {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$ProjectRoot)
    $packagePath = Join-Path (Assert-LocalDirectory -Path (Join-Path $ProjectRoot 'web')) 'package.json'
    $json = Get-Content -LiteralPath $packagePath -Raw -Encoding UTF8 | ConvertFrom-Json -ErrorAction Stop
    $base = [string]$json.version
    if (-not (Test-LocalSemVer -Value $base)) { throw "web/package.json version is not strict SemVer: $base" }
    return $base
}

function ConvertTo-LocalInstallerVersion {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$BaseVersion)
    if (-not (Test-LocalSemVer -Value $BaseVersion)) { throw "Invalid local package version: $BaseVersion" }
    $parts = $BaseVersion -split '\+', 2
    $localCore = $parts[0] + '-local'
    if ($parts.Count -eq 2) { return ($localCore + '+' + $parts[1]) }
    return $localCore
}

function ConvertFrom-LocalInstallerVersion {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$LocalVersion)
    if (-not (Test-LocalSemVer -Value $LocalVersion)) { throw "Invalid local package version: $LocalVersion" }
    $parts = $LocalVersion -split '\+', 2
    if ($parts[0] -notmatch '-local$') { throw "PackageVersion must use the local -local label: $LocalVersion" }
    $baseCore = $parts[0].Substring(0, $parts[0].Length - '-local'.Length)
    if ($parts.Count -eq 2) { return ($baseCore + '+' + $parts[1]) }
    return $baseCore
}

function ConvertTo-LocalVersionInfoVersion {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$BaseVersion)
    $core = ($BaseVersion -split '[+-]', 2)[0]
    return ($core + '.0')
}

function New-LocalManifestText {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Version,
        [Parameter(Mandatory = $true)][string]$Commit,
        [Parameter(Mandatory = $true)][int64]$Size,
        [Parameter(Mandatory = $true)][string]$Sha256,
        [bool]$IsLocal = $true
    )
    if ($Commit -notmatch '^[0-9a-f]{40}$') { $Commit = 'unknown' }
    $manifest = [ordered]@{
        name = 'roborev-dashboard'
        version = $Version
        commit = $Commit
        target = 'windows/amd64'
        roborevCompatibility = 'v0.64.0'
        size = $Size
        sha256 = $Sha256.ToLowerInvariant()
        signed = $false
        local = $IsLocal
    }
    return (($manifest | ConvertTo-Json -Compress) + "`n")
}

function Assert-LocalPayloadAllowlist {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Directory)
    $allowed = @('roborev-dashboard.exe', 'LICENSE', 'roborev-dashboard.manifest.json', 'roborev-dashboard.sha256')
    foreach ($item in @(Get-ChildItem -LiteralPath $Directory -Force)) {
        if ($allowed -notcontains $item.Name) { throw "Local installer payload contains an unexpected item: $($item.Name)" }
        if ($item.PSIsContainer) { throw "Local installer payload cannot contain directories: $($item.Name)" }
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw "Local installer payload contains a reparse point: $($item.Name)" }
    }
}

function Invoke-LocalInstallerPackage {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string]$ProjectRoot,
        [Parameter(Mandatory = $true)][string]$PackageOutputDirectory,
        [Parameter()][string]$PackageVersion,
        [Parameter()][string]$BuildCommit = 'unknown',
        [Parameter()][string]$InnoCache,
        [Parameter()][string]$ResultManifest,
        [Parameter()][switch]$PreserveStaging,
        [Parameter()][switch]$ReleasePackage
    )

    $project = Assert-LocalDirectory -Path $ProjectRoot
    $exe = Get-LocalFullPath -Path $Executable
    if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Dashboard executable does not exist: $exe" }
    if ([IO.Path]::GetFileName($exe) -cne $script:LocalDashboardExecutableName) { throw "Dashboard executable must be named $($script:LocalDashboardExecutableName)." }
    $outDir = Assert-LocalParent -Path $PackageOutputDirectory
    if (-not (Test-Path -LiteralPath $outDir -PathType Container)) { New-Item -ItemType Directory -Path $outDir -Force | Out-Null }
    $outDir = Assert-LocalDirectory -Path $outDir

    $sourceVersion = Get-LocalVersionFromPackageJson -ProjectRoot $project
    if ($ReleasePackage) {
        if ([string]::IsNullOrWhiteSpace($PackageVersion) -or -not (Test-LocalSemVer -Value $PackageVersion)) {
            throw 'Release installer requires an explicit strict SemVer -PackageVersion.'
        }
        if ($PackageVersion -cne $sourceVersion) {
            throw "Release installer version '$PackageVersion' does not match web/package.json '$sourceVersion'."
        }
        $baseVersion = $PackageVersion
    } else {
        if ([string]::IsNullOrWhiteSpace($PackageVersion)) {
            $PackageVersion = ConvertTo-LocalInstallerVersion -BaseVersion $sourceVersion
        } elseif (-not (Test-LocalSemVer -Value $PackageVersion)) {
            throw "PackageVersion is not strict SemVer: $PackageVersion"
        }
        if ($PackageVersion -match '(^|-)local(\+|$)') {
            $baseVersion = ConvertFrom-LocalInstallerVersion -LocalVersion $PackageVersion
        } else {
            $baseVersion = $PackageVersion
            $PackageVersion = ConvertTo-LocalInstallerVersion -BaseVersion $baseVersion
        }
        if ($baseVersion -cne $sourceVersion) {
            throw "PackageVersion '$PackageVersion' does not match web/package.json basis version '$sourceVersion'."
        }
    }
    $versionInfo = ConvertTo-LocalVersionInfoVersion -BaseVersion $baseVersion
    [void](Assert-NoReparsePoint -Path $exe)
    $sha = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant()
    $size = [int64](Get-Item -LiteralPath $exe -Force).Length

    $runId = [guid]::NewGuid().ToString('N')
    $stageRoot = Join-Path $outDir ('.local-installer-' + $runId)
    $payload = Join-Path $stageRoot 'payload'
    $compileOut = Join-Path $stageRoot 'compiled'
    try {
        if (Test-Path -LiteralPath $stageRoot) { throw "Refusing to reuse an existing package staging path: $stageRoot" }
        New-Item -ItemType Directory -Path $payload -Force | Out-Null
        New-Item -ItemType Directory -Path $compileOut -Force | Out-Null
        [void](Assert-NoReparseTree -Path $stageRoot)
        $payloadExe = Join-Path $payload $script:LocalDashboardExecutableName
        Copy-Item -LiteralPath $exe -Destination $payloadExe -Force
        $payloadSha = (Get-FileHash -LiteralPath $payloadExe -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($payloadSha -cne $sha) { throw 'Payload executable hash changed while staging the installer.' }
        $licensePath = Join-Path $project 'LICENSE'
        $includeLicense = Test-Path -LiteralPath $licensePath -PathType Leaf
        if ($includeLicense) { Copy-Item -LiteralPath $licensePath -Destination (Join-Path $payload 'LICENSE') -Force }
        $manifestPath = Join-Path $payload 'roborev-dashboard.manifest.json'
        $hashPath = Join-Path $payload 'roborev-dashboard.sha256'
        [IO.File]::WriteAllText($manifestPath, (New-LocalManifestText -Version $PackageVersion -Commit $BuildCommit -Size $size -Sha256 $sha -IsLocal:(-not $ReleasePackage)), [Text.UTF8Encoding]::new($false))
        [IO.File]::WriteAllText($hashPath, "$sha  $($script:LocalDashboardExecutableName)`n", [Text.UTF8Encoding]::new($false))
        Assert-LocalPayloadAllowlist -Directory $payload

        . (Join-Path $PSScriptRoot 'native-command.ps1')
        . (Join-Path $PSScriptRoot 'prepare-inno.ps1')
        $iscc = Invoke-PrepareInno -ProjectRoot $project -ToolCache $InnoCache
        $iss = Join-Path $project 'installer\RoboRevDashboard.iss'
        if (-not (Test-Path -LiteralPath $iss -PathType Leaf)) { throw "Installer script does not exist: $iss" }
        $args = @(
            '/Qp',
            (('/O' + $compileOut)),
            (('/DAppVersion=' + $PackageVersion)),
            (('/DVersionInfoVersion=' + $versionInfo)),
            (('/DSourceDir=' + $payload)),
            (('/DOutputDir=' + $compileOut)),
            (('/DExeSha256=' + $sha)),
            (('/DBaseVersion=' + $baseVersion)),
            $iss
        )
        if ($includeLicense) {
            $args = @($args[0..($args.Count - 2)] + '/DIncludeLicense=1' + $args[$args.Count - 1])
        }
        Invoke-NativeCommand -FilePath $iscc -ArgumentList $args | Out-Null
        $compiled = Join-Path $compileOut $script:LocalInstallerArtifactName
        if (-not (Test-Path -LiteralPath $compiled -PathType Leaf)) { throw "Inno Setup did not produce $script:LocalInstallerArtifactName" }
        $compiledHash = (Get-FileHash -LiteralPath $compiled -Algorithm SHA256).Hash.ToLowerInvariant()
        $final = Join-Path $outDir $script:LocalInstallerArtifactName
        $finalHash = $final + '.sha256'
        $publishStage = Join-Path $stageRoot 'publish'
        New-Item -ItemType Directory -Path $publishStage -Force | Out-Null
        Copy-Item -LiteralPath $compiled -Destination (Join-Path $publishStage $script:LocalInstallerArtifactName) -Force
        [IO.File]::WriteAllText((Join-Path $publishStage ($script:LocalInstallerArtifactName + '.sha256')), "$compiledHash  $($script:LocalInstallerArtifactName)`n", [Text.UTF8Encoding]::new($false))
        Move-Item -LiteralPath (Join-Path $publishStage $script:LocalInstallerArtifactName) -Destination $final -Force
        Move-Item -LiteralPath (Join-Path $publishStage ($script:LocalInstallerArtifactName + '.sha256')) -Destination $finalHash -Force
        if (-not [string]::IsNullOrWhiteSpace($ResultManifest)) {
            $resultFile = Assert-LocalParent -Path $ResultManifest
            $result = [ordered]@{
                setupPath = $final
                installerPath = $final
                outputPath = $final
                sha256 = $compiledHash
                executableSha256 = $sha
                version = $PackageVersion
            }
            [IO.File]::WriteAllText($resultFile, (($result | ConvertTo-Json -Compress) + "`n"), [Text.UTF8Encoding]::new($false))
        }
        Write-Host "Installer: $final"
        Write-Host "Installer SHA-256: $compiledHash"
        return [pscustomobject][ordered]@{
            InstallerPath = $final
            InstallerSha256 = $compiledHash
            ExecutableSha256 = $sha
            Version = $PackageVersion
            PayloadDirectory = $payload
            StagingDirectory = $stageRoot
        }
    } finally {
        if ((-not $PreserveStaging) -and (Test-Path -LiteralPath $stageRoot)) {
            [void](Assert-SafeExistingParent -Path $outDir)
            if (-not (Test-PathWithin -Path $stageRoot -Root $outDir)) {
                throw "Refusing to remove staging path outside the package output directory: $stageRoot"
            }
            [void](Assert-NoReparseTree -Path $stageRoot)
            Remove-Item -LiteralPath $stageRoot -Recurse -Force -ErrorAction Stop
        }
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    if ([string]::IsNullOrWhiteSpace($SourceRoot)) { $SourceRoot = Split-Path -Parent $PSScriptRoot }
    if ([string]::IsNullOrWhiteSpace($OutputDirectory)) { $OutputDirectory = Join-Path $SourceRoot 'dist\installer' }
    if ([string]::IsNullOrWhiteSpace($ExecutablePath)) {
        if ([string]::IsNullOrWhiteSpace($PayloadDirectory)) { throw 'Provide -ExecutablePath or -PayloadDirectory.' }
        $ExecutablePath = Join-Path $PayloadDirectory $script:LocalDashboardExecutableName
    }
    Write-Output (Invoke-LocalInstallerPackage -Executable $ExecutablePath -ProjectRoot $SourceRoot -PackageOutputDirectory $OutputDirectory -PackageVersion $Version -BuildCommit $Commit -InnoCache $ToolCacheDirectory -ResultManifest $ResultPath -PreserveStaging:$KeepStaging -ReleasePackage:$ReleasePackage)
}
