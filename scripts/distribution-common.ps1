# Shared release distribution contracts. Dot-sourcing performs no build or execution.
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'release-common.ps1')
. (Join-Path $PSScriptRoot 'artifact-contract.ps1')
Add-Type -AssemblyName System.IO.Compression.FileSystem
Add-Type -AssemblyName System.IO.Compression

function Get-StreamSha256Lower {
    param([Parameter(Mandatory)][IO.Stream]$Stream)
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try { return [BitConverter]::ToString($algorithm.ComputeHash($Stream)).Replace('-', '').ToLowerInvariant() }
    finally { $algorithm.Dispose() }
}

function Get-DistributionLockName {
    param([Parameter(Mandatory)][string]$ProjectRoot)
    $stream = [IO.MemoryStream]::new([Text.Encoding]::UTF8.GetBytes($ProjectRoot.ToLowerInvariant()))
    try { return ('Global\RoboRevDashboard.Distributions.' + (Get-StreamSha256Lower -Stream $stream)) }
    finally { $stream.Dispose() }
}

function Resolve-DistributionGit {
    # Several installations may expose git.exe. Select PATH priority before
    # reading Source, so the typed GitPath argument always receives one string.
    $command = Get-Command git -CommandType Application -All -ErrorAction Stop | Select-Object -First 1
    $path = [string]$command.Source
    if ([string]::IsNullOrWhiteSpace($path) -or ![IO.Path]::IsPathRooted($path)) { throw 'Git must resolve to one absolute application path.' }
    $path = Get-FullPathSafe -Path $path
    if (!(Test-Path -LiteralPath $path -PathType Leaf)) { throw 'Resolved Git application does not exist.' }
    [void](Assert-SafeExistingParent -Path $path)
    [void](Assert-NoReparsePoint -Path $path)
    return $path
}

function Assert-OfficialReleaseSource {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$Executable, [Parameter(Mandatory)][string]$Version,
        [Parameter(Mandatory)][string]$Commit)
    [void](Assert-StrictSemVer -Value $Version)
    if ($Commit -cnotmatch '^[0-9a-f]{40}$') { throw 'Release source requires a lowercase commit SHA.' }
    $exe = Get-FullPathSafe -Path $Executable
    [void](Assert-SafeExistingParent -Path $exe)
    $directory = Split-Path -Parent $exe
    if ([IO.Path]::GetFileName($exe) -cne 'roborev-dashboard.exe') { throw 'Unexpected release executable name.' }
    foreach ($name in @('roborev-dashboard.exe', 'roborev-dashboard.manifest.json', 'roborev-dashboard.sha256')) {
        $path = Join-Path $directory $name
        if (!(Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing official release source: $path" }
        [void](Assert-NoReparsePoint -Path $path)
    }
    $manifestPath = Join-Path $directory 'roborev-dashboard.manifest.json'
    $bytes = [IO.File]::ReadAllBytes($manifestPath)
    if ($bytes.Length -gt 65536 -or ($bytes.Length -gt 0 -and $bytes[0] -eq 0xEF)) { throw 'Invalid release manifest encoding or size.' }
    $json = ([Text.UTF8Encoding]::new($false, $true)).GetString($bytes)
    $sha = Get-FileSha256Lower -Path $exe
    $size = [int64](Get-Item -LiteralPath $exe).Length
    $manifest = New-ReleaseManifest -Version $Version -Commit $Commit -Size $size -Sha256 $sha
    # Exact canonical text rejects duplicate/extra fields and wrong JSON types on
    # Windows PowerShell 5.1 too. It is the unchanged package.ps1 eight-field format.
    if ($json -cne (ConvertTo-DeterministicJsonText -Value $manifest)) { throw 'Official release manifest differs from the requested executable contract.' }
    $sidecarPath = Join-Path $directory 'roborev-dashboard.sha256'
    if ((Get-Item -LiteralPath $sidecarPath).Length -gt 256) { throw 'Official SHA-256 sidecar is oversized.' }
    $sidecar = [IO.File]::ReadAllText($sidecarPath, [Text.UTF8Encoding]::new($false, $true))
    if ($sidecar -cne "$sha  roborev-dashboard.exe`n") { throw 'Official release SHA-256 sidecar mismatch.' }
    return $manifest
}

function New-DeterministicPortableZip {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$SourceDirectory, [Parameter(Mandatory)][string]$ZipPath)
    if (Test-Path -LiteralPath $ZipPath) { throw "Refusing to overwrite portable ZIP: $ZipPath" }
    [void](Assert-SafeExistingParent -Path $ZipPath)
    $archive = [IO.Compression.ZipFile]::Open($ZipPath, [IO.Compression.ZipArchiveMode]::Create)
    try {
        foreach ($name in @('roborev-dashboard.exe', 'roborev-dashboard.manifest.json', 'roborev-dashboard.sha256')) {
            $path = Join-Path $SourceDirectory $name
            [void](Assert-NoReparsePoint -Path $path)
            $entry = $archive.CreateEntry($name, [IO.Compression.CompressionLevel]::NoCompression)
            $entry.LastWriteTime = [DateTimeOffset]::new(1980, 1, 1, 0, 0, 0, [TimeSpan]::Zero)
            $entry.ExternalAttributes = 0
            $source = [IO.File]::OpenRead($path)
            $destination = $entry.Open()
            try { $source.CopyTo($destination) } finally { $destination.Dispose(); $source.Dispose() }
        }
    } finally { $archive.Dispose() }
    Assert-PortableZip -SourceDirectory $SourceDirectory -ZipPath $ZipPath
}

function Assert-PortableZip {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$SourceDirectory, [Parameter(Mandatory)][string]$ZipPath)
    $archive = [IO.Compression.ZipFile]::OpenRead($ZipPath)
    try {
        $expected = @('roborev-dashboard.exe', 'roborev-dashboard.manifest.json', 'roborev-dashboard.sha256')
        if ($archive.Entries.Count -ne $expected.Count) { throw 'Portable ZIP must contain exactly the official three files.' }
        for ($i = 0; $i -lt $expected.Count; $i++) {
            $entry = $archive.Entries[$i]
            $path = Join-Path $SourceDirectory $expected[$i]
            if ($entry.FullName -cne $expected[$i] -or $entry.Length -ne (Get-Item -LiteralPath $path).Length -or
                $entry.ExternalAttributes -ne 0 -or $entry.LastWriteTime.DateTime -ne [DateTime]::new(1980, 1, 1)) {
                throw "Portable ZIP entry contract mismatch: $($entry.FullName)"
            }
            $stream = $entry.Open()
            try { $sha = Get-StreamSha256Lower -Stream $stream }
            finally { $stream.Dispose() }
            if ($sha -cne (Get-FileSha256Lower -Path $path)) { throw "Portable ZIP entry hash mismatch: $($entry.FullName)" }
        }
    } finally { $archive.Dispose() }
}

function Assert-DistributionFileSet {
    param([Parameter(Mandatory)][string]$Directory, [Parameter(Mandatory)][string]$Version)
    [void](Assert-NoReparseTree -Path $Directory)
    $expected = @('roborev-dashboard.exe', 'roborev-dashboard.manifest.json', 'roborev-dashboard.sha256',
        'RoboRev-Dashboard-Setup.exe', "RoboRev-Dashboard-$Version-win-x64-portable.zip", 'SHA256SUMS.txt')
    $items = @(Get-ChildItem -LiteralPath $Directory -Force)
    if ($items.Count -ne $expected.Count) { throw 'Distribution candidate must contain exactly six files.' }
    foreach ($item in $items) {
        if ($item.PSIsContainer -or @($expected | Where-Object { $_ -ceq $item.Name }).Count -ne 1) {
            throw "Unexpected distribution candidate item: $($item.Name)"
        }
    }
}

function Invoke-DistributionPreparation {
    [CmdletBinding()]
    param([Parameter(Mandatory)][string]$ProjectRoot, [Parameter(Mandatory)][string]$DistributionVersion,
        [string]$Destination, [switch]$VerifyReproducible,
        [scriptblock]$PackageBuilder, [scriptblock]$InstallerBuilder, [scriptblock]$SourceGuard)
    [void](Assert-StrictSemVer -Value $DistributionVersion)
    $project = Get-FullPathSafe -Path $ProjectRoot
    [void](Assert-SafeExistingParent -Path $project)
    if ([string]::IsNullOrWhiteSpace($Destination)) { $Destination = Join-Path 'dist\release' $DistributionVersion }
    $final = if ([IO.Path]::IsPathRooted($Destination)) { Get-FullPathSafe -Path $Destination } else { Get-FullPathSafe -Path (Join-Path $project $Destination) }
    $final = $final.TrimEnd('\', '/')
    if (Test-Path -LiteralPath $final) { throw "Refusing to overwrite an existing release candidate: $final" }
    $parent = Split-Path -Parent $final
    if ([string]::IsNullOrWhiteSpace($parent)) { throw 'Distribution output has no parent.' }
    [void](Assert-SafeExistingParent -Path $parent)
    if (!$PackageBuilder) { $PackageBuilder = { param($p, $v, $s, $r) & (Join-Path $p 'scripts/package.ps1') -Version $v -OutputDirectory $s -VerifyReproducible:$r } }
    if (!$InstallerBuilder) { $InstallerBuilder = { param($e, $p, $s, $v, $c) Invoke-LocalInstallerPackage -Executable $e -ProjectRoot $p -PackageOutputDirectory $s -PackageVersion $v -BuildCommit $c -ReleasePackage } }
    if (!$SourceGuard) { $SourceGuard = { param($p) Assert-CleanGitRepository -RepositoryRoot $p -GitPath (Resolve-DistributionGit) } }
    $mutex = [Threading.Mutex]::new($false, (Get-DistributionLockName -ProjectRoot $project))
    $locked = $false
    $stage = $null
    $promoted = $false
    try {
        try { $locked = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $locked = $true }
        if (!$locked) { throw 'Another distribution build owns this checkout.' }
        $commit = & $SourceGuard $project
        if (Test-Path -LiteralPath $final) { throw 'Distribution output appeared before build.' }
        New-Item -ItemType Directory -Path $parent -Force | Out-Null
        [void](Assert-SafeExistingParent -Path $parent)
        # package.ps1 requires an absent output; its atomic rename creates this stage.
        $stage = Join-Path $parent ('.dual-distribution-' + [guid]::NewGuid().ToString('N'))
        if (Test-Path -LiteralPath $stage) { throw 'Distribution staging path already exists.' }
        & $PackageBuilder $project $DistributionVersion $stage ([bool]$VerifyReproducible) | Out-Null
        $exe = Join-Path $stage 'roborev-dashboard.exe'
        $manifest = Assert-OfficialReleaseSource -Executable $exe -Version $DistributionVersion -Commit $commit
        $setup = & $InstallerBuilder $exe $project $stage $DistributionVersion $commit
        if ($setup.Version -cne $DistributionVersion -or $setup.ExecutableSha256 -cne $manifest.sha256) { throw 'Installer staging contract mismatch.' }
        $setupPath = Join-Path $stage 'RoboRev-Dashboard-Setup.exe'
        $setupSha = Get-FileSha256Lower -Path $setupPath
        if ($setup.InstallerSha256 -cne $setupSha) { throw 'Installer result hash mismatch.' }
        $setupSidecar = $setupPath + '.sha256'
        [void](Assert-NoReparsePoint -Path $setupSidecar)
        if ([IO.File]::ReadAllText($setupSidecar, [Text.UTF8Encoding]::new($false, $true)) -cne "$setupSha  RoboRev-Dashboard-Setup.exe`n") { throw 'Installer SHA-256 sidecar mismatch.' }
        Remove-Item -LiteralPath $setupSidecar -Force
        $portableName = "RoboRev-Dashboard-$DistributionVersion-win-x64-portable.zip"
        $portable = Join-Path $stage $portableName
        New-DeterministicPortableZip -SourceDirectory $stage -ZipPath $portable
        if ($VerifyReproducible) {
            $reproZip = Join-Path $stage 'portable.repro.zip'
            New-DeterministicPortableZip -SourceDirectory $stage -ZipPath $reproZip
            if ((Get-FileSha256Lower -Path $portable) -cne (Get-FileSha256Lower -Path $reproZip)) { throw 'Portable ZIP is not reproducible.' }
            Remove-Item -LiteralPath $reproZip -Force
        }
        $checksumText = "$(Get-FileSha256Lower -Path $portable)  $portableName`n$setupSha  RoboRev-Dashboard-Setup.exe`n"
        Write-Utf8NoBom -Path (Join-Path $stage 'SHA256SUMS.txt') -Content $checksumText
        [void](Assert-OfficialReleaseSource -Executable $exe -Version $DistributionVersion -Commit $commit)
        Assert-PortableZip -SourceDirectory $stage -ZipPath $portable
        Assert-DistributionFileSet -Directory $stage -Version $DistributionVersion
        if ((& $SourceGuard $project) -cne $commit) { throw 'Checkout HEAD changed during distribution build.' }
        [void](Assert-SafeExistingParent -Path $parent)
        if (Test-Path -LiteralPath $final) { throw 'Release candidate appeared while building.' }
        [IO.Directory]::Move($stage, $final)
        $promoted = $true
        return [pscustomobject]@{ Directory = $final; Installer = Join-Path $final 'RoboRev-Dashboard-Setup.exe'; Portable = Join-Path $final $portableName; ExecutableSha256 = $manifest.sha256; Version = $DistributionVersion; Commit = $commit }
    } finally {
        try {
            if (!$promoted -and $stage -and (Test-Path -LiteralPath $stage)) {
                [void](Assert-SafeExistingParent -Path $parent)
                if (!(Test-PathWithin -Path $stage -Root $parent) -or [IO.Path]::GetFileName($stage) -cnotmatch '^\.dual-distribution-[0-9a-f]{32}$') { throw 'Unsafe distribution cleanup path.' }
                [void](Assert-NoReparseTree -Path $stage)
                Remove-Item -LiteralPath $stage -Recurse -Force
            }
        } finally {
            if ($locked) { $mutex.ReleaseMutex() }
            $mutex.Dispose()
        }
    }
}
