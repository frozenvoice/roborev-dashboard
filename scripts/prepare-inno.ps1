<#!
.SYNOPSIS
    Resolve the pinned Inno Setup compiler used for local Dashboard packages.

This file is intentionally safe to dot-source.  Dot-sourcing exposes the
validation helpers to contract tests and to package-local.ps1 without running
the downloader.  Direct invocation calls Invoke-PrepareInno.
#>
[CmdletBinding()]
param(
    [Parameter()]
    [string]$RootPath,

    [Parameter()]
    [string]$CacheDirectory
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$script:InnoSetupVersion = '6.7.3'
$script:InnoSetupPackageUrl = 'https://github.com/jrsoftware/issrc/releases/download/is-6_7_3/innosetup-6.7.3.exe'
$script:InnoSetupPackageSha256 = '9C73C3BAE7ED48D44112A0F48E66742C00090BDB5BEF71D9D3C056C66E97B732'
$script:InnoSetupCompilerSha256 = '0A8757031B33777E4C9CBFFEE40F11A5062B36D25CBE144C1DB73B6102B80AD7'
# The official 6.7.3 compiler payload contains 119 stable files.  Inno's
# bootstrapper creates unins000.dat/.exe/.msg in the destination and those
# three files are installation-path-specific, so they are deliberately omitted
# from this compiler payload digest.
# This digest is over the
# deterministic UTF-8 lines: relative-path<TAB>length<TAB>sha256, sorted by
# ordinal relative path, followed by LF.  Checking only ISCC.exe permits a damaged or
# mixed-version compiler directory to be used accidentally.
$script:InnoSetupCompilerFileCount = 119
$script:InnoSetupCompilerTreeSha256 = '90727b1f2a9315c9f779774884f0cee5b187173efa96dd48dabc415be537a4be'
$script:InnoSetupPublisherPattern = '(^|, )CN=Pyrsys B\.V\.(,|$)'

function Get-InnoSetupSpec {
    [CmdletBinding()]
    param()

    [pscustomobject][ordered]@{
        Version = $script:InnoSetupVersion
        PackageUrl = $script:InnoSetupPackageUrl
        PackageSha256 = $script:InnoSetupPackageSha256
        CompilerSha256 = $script:InnoSetupCompilerSha256
        CompilerFileCount = $script:InnoSetupCompilerFileCount
        CompilerTreeSha256 = $script:InnoSetupCompilerTreeSha256
        PublisherPattern = $script:InnoSetupPublisherPattern
    }
}

function Get-InnoCompilerTreeLines {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$CompilerDirectory
    )

    $root = [IO.Path]::GetFullPath($CompilerDirectory).TrimEnd('\', '/')
    if (-not (Test-Path -LiteralPath $root -PathType Container)) {
        throw "Inno compiler directory does not exist: $root"
    }
    $rootItem = Get-Item -LiteralPath $root -Force -ErrorAction Stop
    if (($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Refusing a reparse-point compiler directory: $root"
    }

    $records = New-Object System.Collections.ArrayList
    $pending = New-Object System.Collections.ArrayList
    [void]$pending.Add($root)
    while ($pending.Count -gt 0) {
        $directory = [string]$pending[$pending.Count - 1]
        $pending.RemoveAt($pending.Count - 1)
        foreach ($child in @(Get-ChildItem -LiteralPath $directory -Force)) {
            if (($child.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Refusing a reparse-point compiler item: $($child.FullName)"
            }
            if ($child.PSIsContainer) {
                [void]$pending.Add($child.FullName)
                continue
            }
            if (($child.Name -eq 'unins000.dat') -or ($child.Name -eq 'unins000.exe') -or ($child.Name -eq 'unins000.msg')) { continue }
            [void]$records.Add([pscustomobject]@{
                    Relative = $child.FullName.Substring($root.Length + 1).Replace('\', '/')
                    FullName = $child.FullName
                    Length = $child.Length
                })
        }
    }
    # A filesystem enumeration is not a digest contract.  Use an ordinal
    # path comparer so the same payload hashes identically across locales and
    # PowerShell versions.
    $ordered = New-Object 'System.Collections.Generic.SortedDictionary[string,object]' ([StringComparer]::Ordinal)
    foreach ($record in $records) {
        $ordered.Add([string]$record.Relative, $record)
    }
    foreach ($record in $ordered.Values) {
        $hash = (Get-FileHash -LiteralPath $record.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        "{0}`t{1}`t{2}" -f $record.Relative, $record.Length, $hash
    }
}

function Get-InnoCompilerTreeSha256 {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$CompilerDirectory
    )

    $lines = @(Get-InnoCompilerTreeLines -CompilerDirectory $CompilerDirectory)
    $encoding = [Text.UTF8Encoding]::new($false)
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        foreach ($line in $lines) {
            $bytes = $encoding.GetBytes($line + "`n")
            [void]$sha.TransformBlock($bytes, 0, $bytes.Length, $bytes, 0)
        }
        [void]$sha.TransformFinalBlock([byte[]]@(), 0, 0)
        return ([BitConverter]::ToString($sha.Hash)).Replace('-', '').ToLowerInvariant()
    } finally {
        $sha.Dispose()
    }
}

function Assert-InnoCompilerPayload {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$CompilerDirectory
    )

    $spec = Get-InnoSetupSpec
    $compiler = [IO.Path]::GetFullPath($CompilerDirectory).TrimEnd('\', '/')
    $iscc = Join-Path $compiler 'ISCC.exe'
    if (-not (Test-Path -LiteralPath $iscc -PathType Leaf)) {
        throw "Pinned Inno compiler payload is incomplete: ISCC.exe is missing from '$compiler'."
    }
    $isccHash = (Get-FileHash -LiteralPath $iscc -Algorithm SHA256).Hash.ToUpperInvariant()
    if ($isccHash -cne $spec.CompilerSha256) {
        throw "Pinned Inno compiler hash mismatch at '$iscc'."
    }

    $count = @(Get-InnoCompilerTreeLines -CompilerDirectory $compiler).Count
    if ($count -ne [int]$spec.CompilerFileCount) {
        throw "Pinned Inno compiler payload has $count files; expected $($spec.CompilerFileCount)."
    }
    $treeHash = Get-InnoCompilerTreeSha256 -CompilerDirectory $compiler
    if ($treeHash -cne $spec.CompilerTreeSha256) {
        throw "Pinned Inno compiler payload tree hash mismatch at '$compiler'."
    }
    return (Get-FullPath -Path $iscc)
}

function Get-FullPath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )
    return [IO.Path]::GetFullPath($Path)
}

function Assert-InnoSafeAncestors {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)
    $cursor = Get-FullPath -Path $Path
    while ($true) {
        if (Test-Path -LiteralPath $cursor) {
            $item = Get-Item -LiteralPath $cursor -Force -ErrorAction Stop
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "Refusing to operate through a reparse-point ancestor: $cursor"
            }
        }
        $parent = Split-Path -Parent $cursor
        if ([string]::IsNullOrEmpty($parent) -or $parent -eq $cursor) { break }
        $cursor = $parent
    }
}

function Test-InnoPathWithin {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Root
    )
    $fullPath = (Get-FullPath -Path $Path).TrimEnd('\', '/')
    $fullRoot = (Get-FullPath -Path $Root).TrimEnd('\', '/')
    return $fullPath.Equals($fullRoot, [StringComparison]::OrdinalIgnoreCase) -or
        $fullPath.StartsWith($fullRoot + '\', [StringComparison]::OrdinalIgnoreCase) -or
        $fullPath.StartsWith($fullRoot + '/', [StringComparison]::OrdinalIgnoreCase)
}

function Assert-InnoPackageSignature {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$PackagePath
    )

    $signature = Get-AuthenticodeSignature -LiteralPath $PackagePath
    if ($signature.Status -ne 'Valid' -or [string]$signature.SignerCertificate.Subject -notmatch $script:InnoSetupPublisherPattern) {
        throw "Pinned Inno Setup package publisher verification failed for '$PackagePath'."
    }
}

function Invoke-PrepareInno {
    [CmdletBinding()]
    param(
        [Parameter()]
        [string]$ProjectRoot,

        [Parameter()]
        [string]$ToolCache
    )

    if ([string]::IsNullOrWhiteSpace($ProjectRoot)) {
        $ProjectRoot = Split-Path -Parent $PSScriptRoot
    }
    $project = Get-FullPath -Path $ProjectRoot
    if (-not (Test-Path -LiteralPath $project -PathType Container)) {
        throw "Project root does not exist: $project"
    }
    if ([string]::IsNullOrWhiteSpace($ToolCache)) {
        $ToolCache = Join-Path $project 'dist\tools\innosetup-6.7.3'
    }
    $cache = Get-FullPath -Path $ToolCache
    $parent = Split-Path -Parent $cache
    $package = Join-Path $parent 'innosetup-6.7.3.exe'
    Assert-InnoSafeAncestors -Path $parent

    if (Test-Path -LiteralPath $cache -PathType Container) {
        # Never replace an existing compiler tree in place.  A failed cache is
        # ambiguous and must be repaired by deleting it deliberately.
        return Assert-InnoCompilerPayload -CompilerDirectory $cache
    }

    if (-not (Test-Path -LiteralPath $parent -PathType Container)) {
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
    }
    if (-not (Test-Path -LiteralPath $package -PathType Leaf)) {
        $partial = $package + '.partial-' + [guid]::NewGuid().ToString('N')
        try {
            Invoke-WebRequest -UseBasicParsing -Uri $script:InnoSetupPackageUrl -OutFile $partial
            $partialHash = (Get-FileHash -LiteralPath $partial -Algorithm SHA256).Hash.ToUpperInvariant()
            if ($partialHash -cne $script:InnoSetupPackageSha256) {
                throw "Pinned Inno Setup package hash mismatch at '$partial'."
            }
            Assert-InnoPackageSignature -PackagePath $partial
            Move-Item -LiteralPath $partial -Destination $package -Force
        } finally {
            if (Test-Path -LiteralPath $partial -PathType Leaf) {
                Remove-Item -LiteralPath $partial -Force -ErrorAction SilentlyContinue
            }
        }
    }
    $packageHash = (Get-FileHash -LiteralPath $package -Algorithm SHA256).Hash.ToUpperInvariant()
    if ($packageHash -cne $script:InnoSetupPackageSha256) {
        throw "Pinned Inno Setup package hash mismatch at '$package'."
    }
    Assert-InnoPackageSignature -PackagePath $package

    $stage = $cache + '.partial-' + [guid]::NewGuid().ToString('N')
    try {
        New-Item -ItemType Directory -Path $stage -Force | Out-Null
        $arguments = @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/CURRENTUSER', '/PORTABLE=1', '/SP-', '/NOICONS', '/TASKS=', ('/DIR="' + $stage + '"'))
        $process = Start-Process -FilePath $package -ArgumentList $arguments -WindowStyle Hidden -Wait -PassThru
        if ($process.ExitCode -ne 0) {
            throw "Pinned Inno Setup preparation failed with exit code $($process.ExitCode)."
        }
        [void](Assert-InnoCompilerPayload -CompilerDirectory $stage)
        if (Test-Path -LiteralPath $cache) {
            throw "Inno compiler cache appeared during preparation; refusing to replace it: $cache"
        }
        [IO.Directory]::Move($stage, $cache)
        $stage = $null
        return Assert-InnoCompilerPayload -CompilerDirectory $cache
    } catch {
        throw "Prepared Inno Setup payload failed complete-tree validation: $($_.Exception.Message)"
    } finally {
        if ($stage -and (Test-Path -LiteralPath $stage)) {
            $safeToRemove = $false
            try {
                if (Test-InnoPathWithin -Path $stage -Root $parent) {
                    [void](Get-InnoCompilerTreeLines -CompilerDirectory $stage)
                    $safeToRemove = $true
                }
            } catch {
                Write-Warning "Leaving failed Inno staging directory for safe inspection: $stage ($($_.Exception.Message))"
            }
            if ($safeToRemove) {
                Remove-Item -LiteralPath (Get-FullPath -Path $stage) -Recurse -Force -ErrorAction SilentlyContinue
            }
        }
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    Write-Output (Invoke-PrepareInno -ProjectRoot $RootPath -ToolCache $CacheDirectory)
}
