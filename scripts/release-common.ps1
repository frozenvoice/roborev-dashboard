# Shared, side-effect-light helpers for the Windows release scripts.
# This file is safe to dot-source from tests.  It does not invoke Git, Go, or
# npm until a caller explicitly asks it to do so.

Set-StrictMode -Version Latest

$script:ReleaseArtifactName = 'roborev-dashboard.exe'
$script:ReleaseManifestName = 'roborev-dashboard.manifest.json'
$script:ReleaseHashName = 'roborev-dashboard.sha256'
$script:ReleaseTarget = 'windows/amd64'
$script:ReleaseRoboRevCompatibility = 'v0.64.0'

function Test-StrictSemVer {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Value
    )

    if ($Value.Length -eq 0 -or $Value.Trim() -ne $Value) { return $false }
    if ($Value -match '[^0-9A-Za-z.+-]') { return $false }

    $coreAndMetadata = $Value -split '\+', 2
    if ($coreAndMetadata.Count -eq 2 -and -not (Test-SemVerIdentifiers -Value $coreAndMetadata[1] -Prerelease:$false)) {
        return $false
    }
    $core = $coreAndMetadata[0]

    $coreAndPrerelease = $core -split '-', 2
    if ($coreAndPrerelease.Count -eq 2 -and -not (Test-SemVerIdentifiers -Value $coreAndPrerelease[1] -Prerelease:$true)) {
        return $false
    }
    $coreParts = $coreAndPrerelease[0] -split '\.'
    if ($coreParts.Count -ne 3) { return $false }
    foreach ($part in $coreParts) {
        if ($part -notmatch '^(0|[1-9][0-9]*)$') { return $false }
    }
    return $true
}

function Test-SemVerIdentifiers {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Value,

        [Parameter(Mandatory = $true)]
        [bool]$Prerelease
    )

    if ($Value.Length -eq 0) { return $false }
    foreach ($identifier in ($Value -split '\.')) {
        if ($identifier -notmatch '^[0-9A-Za-z-]+$') { return $false }
        if ($Prerelease -and $identifier -match '^[0-9]+$' -and $identifier.Length -gt 1 -and $identifier.StartsWith('0')) {
            return $false
        }
    }
    return $true
}

function Assert-StrictSemVer {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Value
    )

    if (-not (Test-StrictSemVer -Value $Value)) {
        throw "Release version '$Value' is not a strict SemVer 2.0.0 value."
    }
    return $Value
}

function Get-FullPathSafe {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    try {
        return [IO.Path]::GetFullPath($Path)
    } catch {
        throw "Path is invalid: $Path"
    }
}

function Test-PathWithin {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [string]$Root
    )

    $fullPath = (Get-FullPathSafe -Path $Path).TrimEnd('\', '/')
    $fullRoot = (Get-FullPathSafe -Path $Root).TrimEnd('\', '/')
    if ($fullPath.Equals($fullRoot, [StringComparison]::OrdinalIgnoreCase)) { return $true }
    return $fullPath.StartsWith($fullRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
        $fullPath.StartsWith($fullRoot + [IO.Path]::AltDirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)
}

function Assert-NoReparsePoint {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Refusing to operate through reparse point: $Path"
    }
    return $item
}

function Assert-SafeExistingParent {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $current = Get-FullPathSafe -Path $Path
    $nearest = $null
    while ($true) {
        if (Test-Path -LiteralPath $current -PathType Container) {
            $nearest = $current
            break
        }
        $parent = Split-Path -Parent $current
        if ([string]::IsNullOrEmpty($parent) -or $parent -eq $current) {
            throw "Could not resolve a safe parent for: $Path"
        }
        $current = $parent
    }
    # Validate every existing ancestor. A junction farther up the path can
    # redirect a seemingly harmless child even when the child itself is not a
    # reparse point.
    $current = $nearest
    while ($true) {
        [void](Assert-NoReparsePoint -Path $current)
        $parent = Split-Path -Parent $current
        if ([string]::IsNullOrEmpty($parent) -or $parent -eq $current) { break }
        $current = $parent
    }
    return $nearest
}

function Assert-NoReparseTree {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    [void](Assert-SafeExistingParent -Path $Path)
    foreach ($item in @(Get-ChildItem -LiteralPath $Path -Force -Recurse -ErrorAction Stop)) {
        [void](Assert-NoReparsePoint -Path $item.FullName)
    }
}

function Remove-GeneratedFrontendAssets {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$RepositoryRoot,

        [Parameter(Mandatory = $true)]
        [string]$DistPath
    )

    $root = Get-FullPathSafe -Path $RepositoryRoot
    $dist = Get-FullPathSafe -Path $DistPath
    if (-not (Test-PathWithin -Path $dist -Root $root)) {
        throw "Frontend output is outside the repository: $dist"
    }
    if (Test-Path -LiteralPath $root) { [void](Assert-SafeExistingParent -Path $root) }
    if (-not (Test-Path -LiteralPath $dist -PathType Container)) {
        [void](Assert-SafeExistingParent -Path $dist)
        New-Item -ItemType Directory -Path $dist -Force | Out-Null
        return
    }
    [void](Assert-SafeExistingParent -Path $dist)

    # First inspect every generated child.  A reparse point anywhere in the
    # tree fails closed before any deletion begins.
    $items = @(Get-ChildItem -LiteralPath $dist -Force -Recurse -ErrorAction Stop)
    foreach ($item in $items) {
        [void](Assert-NoReparsePoint -Path $item.FullName)
    }

    foreach ($item in @(Get-ChildItem -LiteralPath $dist -Force -ErrorAction Stop)) {
        if ($item.Name -eq 'placeholder.txt' -and -not $item.PSIsContainer) { continue }
        if (-not (Test-PathWithin -Path $item.FullName -Root $dist)) {
            throw "Refusing to remove frontend path outside output directory: $($item.FullName)"
        }
        Remove-Item -LiteralPath $item.FullName -Recurse -Force -ErrorAction Stop
    }
}

function Get-FileSha256Lower {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
}

function Get-DirectoryFileManifest {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $root = Get-FullPathSafe -Path $Path
    [void](Assert-SafeExistingParent -Path $root)
    $records = @()
    $allItems = @(Get-ChildItem -LiteralPath $root -Force -Recurse -ErrorAction Stop)
    foreach ($item in $allItems) {
        [void](Assert-NoReparsePoint -Path $item.FullName)
    }
    foreach ($item in @($allItems | Where-Object { -not $_.PSIsContainer })) {
        $relative = $item.FullName.Substring($root.Length).TrimStart('\', '/').Replace('\', '/')
        $records += [pscustomobject]([ordered]@{
            path = $relative
            size = [int64]$item.Length
            sha256 = Get-FileSha256Lower -Path $item.FullName
        })
    }
    return @($records | Sort-Object -Property path)
}

function ConvertTo-DeterministicJsonText {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [object]$Value
    )

    # Ordered dictionaries/PSCustomObjects preserve the contract's field
    # order in Windows PowerShell and PowerShell 7 alike.
    $json = ConvertTo-Json -InputObject $Value -Compress -Depth 8
    return ($json.TrimEnd("`r", "`n") + "`n")
}

function Assert-ExactPropertySet {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [object]$Value,

        [Parameter(Mandatory = $true)]
        [string[]]$Expected
    )

    $actual = @($Value.PSObject.Properties | ForEach-Object { [string]$_.Name })
    if ($actual.Count -ne $Expected.Count) {
        throw "Unexpected property count: got $($actual.Count), expected $($Expected.Count)."
    }
    foreach ($name in $Expected) {
        if (@($actual | Where-Object { $_ -ceq $name }).Count -ne 1) {
            throw "Unexpected or duplicate property: $name"
        }
    }
    return $true
}

function Write-Utf8NoBom {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,

        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Content
    )

    $parent = Split-Path -Parent (Get-FullPathSafe -Path $Path)
    if ($parent) { [void](Assert-SafeExistingParent -Path $parent) }
    $encoding = New-Object System.Text.UTF8Encoding($false)
    [IO.File]::WriteAllText((Get-FullPathSafe -Path $Path), $Content, $encoding)
}

function Get-GitCommit {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$RepositoryRoot,

        [Parameter()]
        [string]$GitPath = 'git'
    )

    $output = & $GitPath -C (Get-FullPathSafe -Path $RepositoryRoot) rev-parse --verify HEAD 2>$null
    if ($LASTEXITCODE -ne 0) { throw 'Git HEAD could not be resolved.' }
    $commit = ([string]::Join("`n", @($output))).Trim()
    if ($commit -notmatch '^[0-9a-f]{40}$') { throw "Git HEAD is not a 40-character lowercase SHA: $commit" }
    return $commit
}

function Assert-CleanGitRepository {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$RepositoryRoot,

        [Parameter()]
        [string]$GitPath = 'git'
    )

    $root = Get-FullPathSafe -Path $RepositoryRoot
    $output = & $GitPath -C $root status --porcelain=v1 --untracked-files=all 2>$null
    if ($LASTEXITCODE -ne 0) { throw 'Git working tree status could not be read.' }
    if (-not [string]::IsNullOrWhiteSpace(([string]::Join("`n", @($output))))) {
        throw 'Release packaging requires a clean Git working tree.'
    }
    return (Get-GitCommit -RepositoryRoot $root -GitPath $GitPath)
}

function New-ReleaseManifest {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Version,
        [Parameter(Mandatory = $true)][string]$Commit,
        [Parameter(Mandatory = $true)][long]$Size,
        [Parameter(Mandatory = $true)][string]$Sha256
    )

    [void](Assert-StrictSemVer -Value $Version)
    if ($Commit -notmatch '^[0-9a-f]{40}$') { throw 'Manifest commit must be a lowercase 40-character SHA.' }
    if ($Sha256 -notmatch '^[0-9a-f]{64}$') { throw 'Manifest hash must be a lowercase SHA-256.' }
    if ($Size -le 0) { throw 'Manifest size must be positive.' }
    return [pscustomobject]([ordered]@{
        name = $script:ReleaseArtifactName.Replace('.exe', '')
        version = $Version
        commit = $Commit
        target = $script:ReleaseTarget
        roborevCompatibility = $script:ReleaseRoboRevCompatibility
        size = $Size
        sha256 = $Sha256
        signed = $false
    })
}
