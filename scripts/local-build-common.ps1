# Shared helpers for the Windows local build/install flow.
#
# These functions deliberately do not call Git, Go, npm, RoboRev, or Inno
# Setup while being dot-sourced.  The orchestration script chooses when to run
# each external command and supplies a typed executable plus argument array.

Set-StrictMode -Version Latest

# Reuse the release path/reparse guards for local staging as well. This file
# is side-effect-light when dot-sourced.
. (Join-Path $PSScriptRoot 'release-common.ps1')

$script:LocalBuildManifestName = 'roborev-dashboard.local.manifest.json'
$script:LocalBuildArtifactName = 'RoboRev-Dashboard-Setup.exe'
$script:LocalBuildInstallRelativePath = 'current\roborev-dashboard.exe'

function Get-LocalBuildAbsolutePath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    try { return [IO.Path]::GetFullPath($Path) }
    catch { throw "Path is invalid: $Path" }
}

function Test-LocalBuildPathWithin {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Root
    )

    $fullPath = (Get-LocalBuildAbsolutePath $Path).TrimEnd('\', '/')
    $fullRoot = (Get-LocalBuildAbsolutePath $Root).TrimEnd('\', '/')
    if ($fullPath.Equals($fullRoot, [StringComparison]::OrdinalIgnoreCase)) { return $true }
    return $fullPath.StartsWith($fullRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase) -or
        $fullPath.StartsWith($fullRoot + [IO.Path]::AltDirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)
}

function ConvertTo-LocalBuildWindowsArgument {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Value
    )

    # CommandLineToArgvW-compatible quoting for Windows PowerShell 5.1.  A
    # ProcessStartInfo on .NET Framework has only one Arguments string; every
    # argument that needs it is quoted so spaces, Unicode, backslashes, and
    # quotes survive the child process boundary without shell interpretation.
    # Leave simple switches unquoted: cmd.exe's /c parser treats a quoted /c as
    # part of the command text rather than as its switch.
    if ($Value.Length -gt 0 -and $Value -notmatch '[\s"]') { return $Value }
    if ($Value.Length -eq 0) { return '""' }
    $builder = New-Object System.Text.StringBuilder
    [void]$builder.Append('"')
    $slashes = 0
    foreach ($character in $Value.ToCharArray()) {
        if ($character -eq '\') {
            $slashes++
            continue
        }
        if ($character -eq '"') {
            [void]$builder.Append(('\' * (($slashes * 2) + 1)))
            [void]$builder.Append('"')
            $slashes = 0
            continue
        }
        if ($slashes -gt 0) { [void]$builder.Append(('\' * $slashes)); $slashes = 0 }
        [void]$builder.Append($character)
    }
    if ($slashes -gt 0) { [void]$builder.Append(('\' * ($slashes * 2))) }
    [void]$builder.Append('"')
    return $builder.ToString()
}

function ConvertTo-LocalBuildArgumentString {
    [CmdletBinding()]
    param(
        [Parameter()][AllowEmptyCollection()][string[]]$ArgumentList = @()
    )

    return [string]::Join(' ', @($ArgumentList | ForEach-Object { ConvertTo-LocalBuildWindowsArgument -Value ([string]$_) }))
}

function Write-LocalBuildUtf8NoBom {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Content
    )

    $fullPath = Get-LocalBuildAbsolutePath $Path
    $parent = Split-Path -Parent $fullPath
    if ($parent) {
        if (Test-Path -LiteralPath $parent -PathType Container) {
            [void](Assert-SafeExistingParent -Path $parent)
        } else {
            [void](Assert-SafeExistingParent -Path $parent)
            New-Item -ItemType Directory -Path $parent -Force | Out-Null
            [void](Assert-NoReparsePoint -Path $parent)
        }
    }
    if (Test-Path -LiteralPath $fullPath -PathType Leaf) { [void](Assert-NoReparsePoint -Path $fullPath) }
    [IO.File]::WriteAllText($fullPath, $Content, (New-Object System.Text.UTF8Encoding($false)))
}

function Get-LocalBuildSha256 {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$Path)
    $fullPath = Get-LocalBuildAbsolutePath $Path
    $algorithm = [Security.Cryptography.SHA256]::Create()
    $stream = $null
    try {
        $stream = [IO.File]::OpenRead($fullPath)
        return ([BitConverter]::ToString($algorithm.ComputeHash($stream))).Replace('-', '').ToLowerInvariant()
    } finally {
        if ($stream) { $stream.Dispose() }
        $algorithm.Dispose()
    }
}

function New-LocalBuildRunDirectory {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $root = Get-LocalBuildAbsolutePath $RepositoryRoot
    $base = Join-Path $root 'dist\local-build'
    [void](Assert-SafeExistingParent -Path $base)
    if (Test-Path -LiteralPath $base -PathType Container) { [void](Assert-NoReparsePoint -Path $base) }
    New-Item -ItemType Directory -Path $base -Force | Out-Null
    for ($attempt = 0; $attempt -lt 10; $attempt++) {
        $name = '{0:yyyyMMdd-HHmmss}-{1}' -f (Get-Date), ([guid]::NewGuid().ToString('N').Substring(0, 8))
        $path = Join-Path $base $name
        try {
            New-Item -ItemType Directory -Path $path -ErrorAction Stop | Out-Null
            New-Item -ItemType Directory -Path (Join-Path $path 'logs') -ErrorAction Stop | Out-Null
            New-Item -ItemType Directory -Path (Join-Path $path 'package') -ErrorAction Stop | Out-Null
            [void](Assert-NoReparsePoint -Path $path)
            return $path
        } catch {
            if (Test-Path -LiteralPath $path) { continue }
            throw
        }
    }
    throw 'Could not create a unique local build staging directory.'
}

function Enter-LocalBuildLock {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $root = Get-LocalBuildAbsolutePath $RepositoryRoot
    $lockRoot = Join-Path $root 'dist\local-build'
    [void](Assert-SafeExistingParent -Path $lockRoot)
    New-Item -ItemType Directory -Path $lockRoot -Force | Out-Null
    [void](Assert-NoReparsePoint -Path $lockRoot)
    $path = Join-Path $lockRoot 'build.lock'
    if (Test-Path -LiteralPath $path) { [void](Assert-NoReparsePoint -Path $path) }
    try {
        $stream = [IO.File]::Open($path, [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
    } catch {
        throw "Another local build is already running for this checkout: $path"
    }
    $bytes = [Text.Encoding]::UTF8.GetBytes("PID=$PID`r`nStarted=$([DateTime]::UtcNow.ToString('o'))`r`n")
    $stream.SetLength(0)
    $stream.Write($bytes, 0, $bytes.Length)
    $stream.Flush()
    return $stream
}

function Set-LocalBuildStage {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string]$Stage,
        [int]$ExitCode = 0
    )

    $record = [pscustomobject]([ordered]@{
        stage = $Stage
        exitCode = $ExitCode
        atUtc = [DateTime]::UtcNow.ToString('o')
    })
    Write-LocalBuildUtf8NoBom -Path (Join-Path $RunDirectory 'stage.json') -Content (($record | ConvertTo-Json -Compress) + "`n")
    Write-Host "[local-build] $Stage"
}

function Move-LocalBuildPublishedArtifacts {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$RunDirectory
    )

    # The stable installer directory is a generated publication surface. Move
    # only its known local-build names into this immutable run before any
    # prerequisite/test work starts, so a failed run can never leave an older
    # Setup.exe looking current. The installed application directory is not
    # touched here; Setup remains the only component allowed to stop/update it.
    $root = Get-LocalBuildAbsolutePath $RepositoryRoot
    $run = Get-LocalBuildAbsolutePath $RunDirectory
    $stableDirectory = Join-Path $root 'dist\installer'
    if (-not (Test-Path -LiteralPath $stableDirectory -PathType Container)) { return @() }
    [void](Assert-SafeExistingParent -Path $stableDirectory)
    [void](Assert-NoReparsePoint -Path $stableDirectory)
    $previousDirectory = Join-Path $run 'previous'
    [void](Assert-SafeExistingParent -Path $previousDirectory)
    New-Item -ItemType Directory -Path $previousDirectory -Force | Out-Null
    [void](Assert-NoReparsePoint -Path $previousDirectory)

    $moved = New-Object System.Collections.Generic.List[string]
    foreach ($name in @(
        'RoboRev-Dashboard-Setup.exe',
        'RoboRev-Dashboard-Setup.exe.sha256',
        'roborev-dashboard.local.manifest.json'
    )) {
        $source = Join-Path $stableDirectory $name
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) { continue }
        [void](Assert-NoReparsePoint -Path $source)
        $destination = Join-Path $previousDirectory $name
        Move-Item -LiteralPath $source -Destination $destination -Force -ErrorAction Stop
        [void]$moved.Add($destination)
    }
    return @($moved)
}

function Get-LocalBuildVersion {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $path = Join-Path (Get-LocalBuildAbsolutePath $RepositoryRoot) 'web\package.json'
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Frontend package manifest is missing: $path" }
    try { $package = Get-Content -LiteralPath $path -Raw -Encoding UTF8 | ConvertFrom-Json -ErrorAction Stop }
    catch { throw "Frontend package manifest is invalid: $path" }
    $version = [string]$package.version
    if ([string]::IsNullOrWhiteSpace($version)) { throw "Frontend package manifest has no version: $path" }
    if ($version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$') {
        throw "Frontend package version is not a strict SemVer: $version"
    }
    return $version
}

function Get-LocalBuildGitState {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter()][string]$GitPath = 'git'
    )

    $root = Get-LocalBuildAbsolutePath $RepositoryRoot
    $head = (& $GitPath -C $root rev-parse --verify HEAD 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $head -notmatch '^[0-9a-f]{40}$') { throw 'Git HEAD could not be resolved for the local build.' }
    $status = (& $GitPath -C $root status --porcelain=v1 --untracked-files=all 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Git working tree status could not be read for the local build.' }
    return [pscustomobject]([ordered]@{
        Head = $head
        Dirty = -not [string]::IsNullOrWhiteSpace($status)
        Status = $status
    })
}

function Get-LocalBuildSourceFingerprint {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$GitPath
    )

    $root = Get-LocalBuildAbsolutePath $RepositoryRoot
    # --exclude-standard omits normal generated output; these two caches are
    # intentionally excluded as well because they may be created by the
    # prerequisite/build tools during this run without being source edits.
    $output = & $GitPath -C $root ls-files --cached --others --exclude-standard --exclude=.cache --exclude=.gocache-runtime -z 2>$null
    if ($LASTEXITCODE -ne 0) { throw 'Git source inventory could not be read for the local build.' }
    $raw = [string]$output
    $paths = @($raw -split "`0" | Where-Object { -not [string]::IsNullOrEmpty($_) })
    $lines = New-Object System.Collections.Generic.List[string]
    foreach ($relative in $paths) {
        $relative = ([string]$relative).Replace('/', '\')
        $full = Get-LocalBuildAbsolutePath (Join-Path $root $relative)
        if (-not (Test-LocalBuildPathWithin -Path $full -Root $root)) { throw "Git source inventory escaped the repository: $relative" }
        if (-not (Test-Path -LiteralPath $full)) {
            # A dirty checkout can intentionally delete a tracked source file.
            # Keep that state in the digest so it is detected if it changes,
            # while allowing the later build/test stages to decide whether the
            # missing input is actually required.
            [void]$lines.Add(('{0}`tMISSING' -f $relative.Replace('\', '/')))
            continue
        }
        if (-not (Test-Path -LiteralPath $full -PathType Leaf)) { throw "Git source inventory contains a non-file path: $relative" }
        [void](Assert-NoReparsePoint -Path $full)
        $item = Get-Item -LiteralPath $full -Force
        [void]$lines.Add(('{0}`t{1}`t{2}' -f $relative.Replace('\', '/'), [int64]$item.Length, (Get-LocalBuildSha256 $full)))
    }
    $canonical = [string]::Join("`n", @($lines | Sort-Object)) + "`n"
    $sha = [Security.Cryptography.SHA256]::Create()
    try {
        $digest = ([BitConverter]::ToString($sha.ComputeHash([Text.UTF8Encoding]::new($false).GetBytes($canonical)))).Replace('-', '').ToLowerInvariant()
    } finally { $sha.Dispose() }
    return [pscustomobject]([ordered]@{ Digest = $digest; Count = $lines.Count })
}

function Invoke-LocalBuildProcess {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter()][AllowEmptyCollection()][string[]]$ArgumentList = @(),
        [Parameter()][string]$WorkingDirectory,
        [Parameter()][string]$StandardOutputPath,
        [Parameter()][string]$StandardErrorPath,
        [int]$TimeoutSeconds = 1800
    )

    $file = Get-LocalBuildAbsolutePath $FilePath
    if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw "Executable was not found: $file" }
    $start = New-Object System.Diagnostics.ProcessStartInfo
    $start.FileName = $file
    $start.Arguments = ConvertTo-LocalBuildArgumentString -ArgumentList $ArgumentList
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    try {
        $utf8 = New-Object System.Text.UTF8Encoding($false)
        $start.StandardOutputEncoding = $utf8
        $start.StandardErrorEncoding = $utf8
    } catch {
        # Windows PowerShell 5.1 on an older .NET Framework may not expose
        # the encoding properties; process execution remains safe and the
        # output is still captured without dropping the failure code.
    }
    if ($WorkingDirectory) { $start.WorkingDirectory = Get-LocalBuildAbsolutePath $WorkingDirectory }
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $start
    $stdoutTask = $null
    $stderrTask = $null
    try {
        if (-not $process.Start()) { throw "Could not start executable: $file" }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit([Math]::Max(1, $TimeoutSeconds) * 1000)) {
            try { $process.Kill() } catch { }
            throw "Process timed out after $TimeoutSeconds seconds: $file"
        }
        # WaitForExit() without a timeout ensures asynchronous stream drains are
        # complete before their contents are persisted.
        $process.WaitForExit()
        $stdout = [string]$stdoutTask.Result
        $stderr = [string]$stderrTask.Result
        if ($StandardOutputPath) { Write-LocalBuildUtf8NoBom -Path $StandardOutputPath -Content $stdout }
        if ($StandardErrorPath) { Write-LocalBuildUtf8NoBom -Path $StandardErrorPath -Content $stderr }
        return [pscustomobject]([ordered]@{
            FilePath = $file
            ArgumentList = [string[]]$ArgumentList
            ExitCode = [int]$process.ExitCode
            StandardOutput = $stdout
            StandardError = $stderr
            StandardOutputPath = $StandardOutputPath
            StandardErrorPath = $StandardErrorPath
        })
    } finally { $process.Dispose() }
}

function Invoke-LocalBuildRequiredProcess {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter()][AllowEmptyCollection()][string[]]$ArgumentList = @(),
        [Parameter()][string]$WorkingDirectory,
        [Parameter()][string]$StandardOutputPath,
        [Parameter()][string]$StandardErrorPath,
        [int]$TimeoutSeconds = 1800
    )

    $result = Invoke-LocalBuildProcess @PSBoundParameters
    if ($result.ExitCode -ne 0) {
        $exception = New-Object System.InvalidOperationException ("Native command '$FilePath' failed with exit code $($result.ExitCode).")
        $exception.Data['ExitCode'] = [int]$result.ExitCode
        $exception.Data['FilePath'] = $FilePath
        throw $exception
    }
    return $result
}

function Assert-LocalBuildOptions {
    [CmdletBinding()]
    param([switch]$NoInstall, [switch]$SilentInstall)
    if ($NoInstall -and $SilentInstall) { throw '-NoInstall and -SilentInstall cannot be used together.' }
}

function Get-LocalBuildInstallRoot {
    [CmdletBinding()]
    param()
    $localAppData = [Environment]::GetFolderPath('LocalApplicationData')
    if ([string]::IsNullOrWhiteSpace($localAppData)) { throw 'Windows LocalApplicationData is unavailable.' }
    return Join-Path $localAppData 'Programs\RoboRev Dashboard'
}

function New-LocalBuildManifest {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Version,
        [Parameter(Mandatory = $true)][string]$Commit,
        [Parameter(Mandatory = $true)][bool]$Dirty,
        [Parameter(Mandatory = $true)][string]$ExecutablePath,
        [Parameter(Mandatory = $true)][string]$InstallerPath
    )

    $exe = Get-LocalBuildAbsolutePath $ExecutablePath
    $setup = Get-LocalBuildAbsolutePath $InstallerPath
    if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) { throw "Local build executable is missing: $exe" }
    if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) { throw "Local installer is missing: $setup" }
    $exeItem = Get-Item -LiteralPath $exe -Force
    $setupItem = Get-Item -LiteralPath $setup -Force
    return [pscustomobject]([ordered]@{
        name = 'roborev-dashboard'
        version = $Version
        buildVersion = 'dev'
        commit = $Commit
        dirty = $Dirty
        release = $false
        target = 'windows/amd64'
        executable = [pscustomobject]([ordered]@{ name = $exeItem.Name; size = [int64]$exeItem.Length; sha256 = Get-LocalBuildSha256 $exe })
        installer = [pscustomobject]([ordered]@{ name = $setupItem.Name; size = [int64]$setupItem.Length; sha256 = Get-LocalBuildSha256 $setup })
        signed = $false
    })
}

function Write-LocalBuildFailureMarker {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string]$Stage,
        [Parameter(Mandatory = $true)][string]$Message,
        [int]$ExitCode = 1
    )

    $base = Join-Path (Get-LocalBuildAbsolutePath $RepositoryRoot) 'dist\local-build'
    $marker = @(
        'RoboRev Dashboard local build failed.',
        "Failed at: $Stage",
        "Exit code: $ExitCode",
        "Cause: $Message",
        "Run directory: $(Get-LocalBuildAbsolutePath $RunDirectory)",
        "Stage record: $(Join-Path (Get-LocalBuildAbsolutePath $RunDirectory) 'stage.json')"
    ) -join [Environment]::NewLine
    Write-LocalBuildUtf8NoBom -Path (Join-Path $base 'last-failure.txt') -Content ($marker + [Environment]::NewLine)
    return $marker
}
