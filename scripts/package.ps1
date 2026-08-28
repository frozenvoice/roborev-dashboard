[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter()]
    [string]$OutputDirectory,

    [Parameter()]
    [switch]$VerifyReproducible
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')
. (Join-Path $PSScriptRoot 'release-common.ps1')

[void](Assert-StrictSemVer -Value $Version)
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT -or -not [Environment]::Is64BitOperatingSystem) {
    throw 'Release packaging is supported on Windows amd64 only.'
}

$tooling = Assert-DevelopmentPrerequisites -RootPath $root
$commit = $null
$stage = $null
$final = $null

function Invoke-ReleaseExeVersionJson {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Executable
    )

    $start = New-Object System.Diagnostics.ProcessStartInfo
    $start.FileName = (Get-FullPathSafe -Path $Executable)
    $start.Arguments = '--version-json'
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $start
    try {
        if (-not $process.Start()) { throw 'Could not start release executable.' }
        if (-not $process.WaitForExit(15000)) {
            try { $process.Kill() } catch { }
            throw 'Release executable --version-json timed out.'
        }
        $stdout = $process.StandardOutput.ReadToEnd()
        $stderr = $process.StandardError.ReadToEnd()
        if ($process.ExitCode -ne 0) {
            throw "Release executable --version-json failed with exit code $($process.ExitCode): $stderr"
        }
        try { return ($stdout | ConvertFrom-Json -ErrorAction Stop) } catch { throw 'Release executable returned invalid version JSON.' }
    } finally {
        $process.Dispose()
    }
}

function Assert-ReleaseVersionJson {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][object]$Metadata,
        [Parameter(Mandatory = $true)][string]$Version,
        [Parameter(Mandatory = $true)][string]$Commit
    )

    [void](Assert-ExactPropertySet -Value $Metadata -Expected @('name', 'version', 'commit', 'release', 'target', 'roborevCompatibility'))
    if ($Metadata.name -ne 'roborev-dashboard' -or
        $Metadata.version -ne $Version -or
        $Metadata.commit -ne $Commit -or
        $Metadata.release -ne $true -or
        $Metadata.target -ne 'windows/amd64' -or
        $Metadata.roborevCompatibility -ne 'v0.64.0') {
        throw 'Release executable provenance does not match the requested package.'
    }
}

Push-Location $root
try {
    # Check before any generated frontend output is touched.  Ignored build
    # output does not make a clean checkout dirty, while source edits do.
    $commit = Assert-CleanGitRepository -RepositoryRoot $root -GitPath $tooling.GitPath
    $frontend = Join-Path $root 'internal\webui\dist'
    Remove-GeneratedFrontendAssets -RepositoryRoot $root -DistPath $frontend
    Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'run', 'build')
    $frontendA = @(Get-DirectoryFileManifest -Path $frontend)

    $parent = $null
    if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
        $final = Join-Path $root (Join-Path 'dist\release' $Version)
    } elseif ([IO.Path]::IsPathRooted($OutputDirectory)) {
        $final = Get-FullPathSafe -Path $OutputDirectory
    } else {
        $final = Get-FullPathSafe -Path (Join-Path $root $OutputDirectory)
    }
    $final = $final.TrimEnd('\', '/')
    if ([string]::IsNullOrEmpty($final)) { throw 'Output directory is invalid.' }
    if (Test-Path -LiteralPath $final) { throw "Refusing to overwrite existing release directory: $final" }
    $parent = Split-Path -Parent $final
    if ([string]::IsNullOrEmpty($parent)) { throw 'Output directory has no parent.' }
    if (Test-Path -LiteralPath $parent) { [void](Assert-SafeExistingParent -Path $parent) }
    else { [void](Assert-SafeExistingParent -Path $parent); New-Item -ItemType Directory -Path $parent -Force | Out-Null }

    # Staging is deliberately beside the final directory, so Directory.Move
    # remains a same-volume atomic publish even for a custom output path.
    $stage = Join-Path $parent ('.roborev-dashboard-package-' + [guid]::NewGuid().ToString('N'))
    if (Test-Path -LiteralPath $stage) { throw 'Generated staging directory unexpectedly exists.' }
    New-Item -ItemType Directory -Path $stage -Force | Out-Null
    [void](Assert-NoReparsePoint -Path $stage)

    $exeA = Join-Path $stage $script:ReleaseArtifactName
    $ldflags = "-s -w -X github.com/froze/roborev-dashboard/internal/buildinfo.version=$Version -X github.com/froze/roborev-dashboard/internal/buildinfo.commit=$commit -X github.com/froze/roborev-dashboard/internal/buildinfo.release=true -X github.com/froze/roborev-dashboard/internal/buildinfo.target=windows/amd64"
    $goBuildArguments = @('build', '-trimpath', '-buildvcs=false', '-ldflags', $ldflags, '-o', $exeA, './cmd/dashboard')
    $oldGoOS = $env:GOOS
    $oldGoArch = $env:GOARCH
    $oldCGO = $env:CGO_ENABLED
    try {
        $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
        Invoke-NativeCommand -FilePath $tooling.GoPath -ArgumentList $goBuildArguments
        if ($VerifyReproducible) {
            $frontendAJson = ConvertTo-DeterministicJsonText -Value @($frontendA)
            Remove-GeneratedFrontendAssets -RepositoryRoot $root -DistPath $frontend
            Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'run', 'build')
            $frontendB = @(Get-DirectoryFileManifest -Path $frontend)
            $frontendBJson = ConvertTo-DeterministicJsonText -Value @($frontendB)
            if ($frontendAJson -cne $frontendBJson) { throw 'Frontend production build is not reproducible.' }

            $exeB = Join-Path $stage 'roborev-dashboard.repro.exe'
            $goBuildArgumentsB = @('build', '-trimpath', '-buildvcs=false', '-ldflags', $ldflags, '-o', $exeB, './cmd/dashboard')
            Invoke-NativeCommand -FilePath $tooling.GoPath -ArgumentList $goBuildArgumentsB
            if ((Get-FileSha256Lower -Path $exeA) -cne (Get-FileSha256Lower -Path $exeB)) {
                throw 'Go release builds are not reproducible.'
            }
            Remove-Item -LiteralPath $exeB -Force -ErrorAction Stop
        }
    } finally {
        if ($null -eq $oldGoOS) { Remove-Item Env:GOOS -ErrorAction SilentlyContinue } else { $env:GOOS = $oldGoOS }
        if ($null -eq $oldGoArch) { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue } else { $env:GOARCH = $oldGoArch }
        if ($null -eq $oldCGO) { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue } else { $env:CGO_ENABLED = $oldCGO }
    }

    $metadata = Invoke-ReleaseExeVersionJson -Executable $exeA
    Assert-ReleaseVersionJson -Metadata $metadata -Version $Version -Commit $commit
    $sha256 = Get-FileSha256Lower -Path $exeA
    $size = [int64](Get-Item -LiteralPath $exeA -Force).Length
    $manifest = New-ReleaseManifest -Version $Version -Commit $commit -Size $size -Sha256 $sha256
    Write-Utf8NoBom -Path (Join-Path $stage $script:ReleaseManifestName) -Content (ConvertTo-DeterministicJsonText -Value $manifest)
    Write-Utf8NoBom -Path (Join-Path $stage $script:ReleaseHashName) -Content ($sha256 + "  " + $script:ReleaseArtifactName + "`n")

    # Verify the complete staged set, not merely the values used to create it.
    $manifestPath = Join-Path $stage $script:ReleaseManifestName
    $hashPath = Join-Path $stage $script:ReleaseHashName
    $manifestBytes = [IO.File]::ReadAllBytes($manifestPath)
    if ($manifestBytes.Length -gt 0 -and $manifestBytes[0] -eq 0xEF) { throw 'Manifest unexpectedly contains a UTF-8 BOM.' }
    $decodedManifest = ([Text.UTF8Encoding]::new($false, $true)).GetString($manifestBytes) | ConvertFrom-Json -ErrorAction Stop
    [void](Assert-ExactPropertySet -Value $decodedManifest -Expected @('name', 'version', 'commit', 'target', 'roborevCompatibility', 'size', 'sha256', 'signed'))
    if ($decodedManifest.name -cne 'roborev-dashboard' -or
        $decodedManifest.version -cne $Version -or
        $decodedManifest.commit -cne $commit -or
        $decodedManifest.target -cne 'windows/amd64' -or
        $decodedManifest.roborevCompatibility -cne 'v0.64.0' -or
        [int64]$decodedManifest.size -ne $size -or
        $decodedManifest.sha256 -cne (Get-FileSha256Lower -Path $exeA) -or
        $decodedManifest.signed -ne $false) { throw 'Staged manifest does not match the release contract.' }
    if (([IO.File]::ReadAllText($hashPath, ([Text.UTF8Encoding]::new($false, $true)))) -cne ($sha256 + "  " + $script:ReleaseArtifactName + "`n")) { throw 'Staged SHA-256 file is not deterministic.' }
    $names = @(Get-ChildItem -LiteralPath $stage -Force | Select-Object -ExpandProperty Name | Sort-Object)
    $expectedNames = @($script:ReleaseArtifactName, $script:ReleaseManifestName, $script:ReleaseHashName)
    $expectedNames = @($expectedNames | Sort-Object)
    if ((ConvertTo-DeterministicJsonText -Value @($names)) -cne (ConvertTo-DeterministicJsonText -Value @($expectedNames))) { throw 'Release staging contains unexpected files.' }

    if (Test-Path -LiteralPath $final) { throw "Refusing to overwrite existing release directory: $final" }
    [IO.Directory]::Move($stage, $final)
    $stage = $null
    Write-Host "Packaged $final"
} finally {
    Pop-Location
    if ($stage -and (Test-Path -LiteralPath $stage)) {
        # The stage path was generated under the already validated parent; do
        # not follow a reparse point while cleaning a failed package.
        Assert-NoReparseTree -Path $stage
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }
}
