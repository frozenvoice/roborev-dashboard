[CmdletBinding()]
param(
    [switch]$NoInstall,
    [switch]$SilentInstall
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$utilityManifest = Join-Path $PSHOME 'Modules\Microsoft.PowerShell.Utility\Microsoft.PowerShell.Utility.psd1'
if (Test-Path -LiteralPath $utilityManifest -PathType Leaf) {
    Import-Module -Name $utilityManifest -Global -ErrorAction Stop
} else {
    Import-Module -Name Microsoft.PowerShell.Utility -Global -ErrorAction Stop
}
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = $utf8NoBom
$OutputEncoding = $utf8NoBom

$script:LocalBuildCurrentStage = 'preflight'
$script:LocalBuildRunDirectory = $null

. (Join-Path $PSScriptRoot 'local-build-common.ps1')
. (Join-Path $PSScriptRoot 'prerequisites.ps1')

function Get-LocalBuildPowerShellPath {
    $command = Get-Command powershell.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command) {
        $path = if ($command.Path) { $command.Path } else { $command.Source }
        return (Get-LocalBuildAbsolutePath $path)
    }
    $fallback = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if (Test-Path -LiteralPath $fallback -PathType Leaf) { return $fallback }
    throw 'Windows PowerShell 5.1 was not found. The local build requires the Windows PowerShell that ships with Windows.'
}

function Set-LocalBuildCurrentStage {
    param([Parameter(Mandatory = $true)][string]$Stage)
    $script:LocalBuildCurrentStage = $Stage
    if ($script:LocalBuildRunDirectory) { Set-LocalBuildStage -RunDirectory $script:LocalBuildRunDirectory -Stage $Stage }
    else { Write-Host "[local-build] $Stage" }
}

function Invoke-LocalBuildChildScript {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string]$Stage,
        [Parameter(Mandatory = $true)][string]$ScriptPath,
        [Parameter()][AllowEmptyCollection()][string[]]$Arguments = @(),
        [int]$TimeoutSeconds = 3600
    )

    $scriptPathFull = Get-LocalBuildAbsolutePath $ScriptPath
    if (-not (Test-Path -LiteralPath $scriptPathFull -PathType Leaf)) { throw "Script was not found: $scriptPathFull" }
    Set-LocalBuildStage -RunDirectory $RunDirectory -Stage $Stage
    $powershell = Get-LocalBuildPowerShellPath
    $runnerPath = Join-Path $PSScriptRoot 'local-build-child.ps1'
    if (-not (Test-Path -LiteralPath $runnerPath -PathType Leaf)) { throw "Local child runner is missing: $runnerPath" }
    $outPath = Join-Path (Join-Path $RunDirectory 'logs') ($Stage + '.out.log')
    $errPath = Join-Path (Join-Path $RunDirectory 'logs') ($Stage + '.err.log')
    $argumentsJson = ConvertTo-Json -InputObject @($Arguments) -Compress -Depth 4
    $childArguments = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', (Get-LocalBuildAbsolutePath $runnerPath),
        '-ScriptPath', $scriptPathFull, '-ArgumentsJson', $argumentsJson)
    $result = Invoke-LocalBuildProcess -FilePath $powershell -ArgumentList $childArguments -WorkingDirectory $RepositoryRoot `
        -StandardOutputPath $outPath -StandardErrorPath $errPath -TimeoutSeconds $TimeoutSeconds
    if ($result.ExitCode -ne 0) {
        $exception = New-Object System.InvalidOperationException ("$Stage failed with exit code $($result.ExitCode). See $errPath")
        $exception.Data['ExitCode'] = [int]$result.ExitCode
        $exception.Data['Stage'] = $Stage
        throw $exception
    }
    return $result
}

function Invoke-LocalPackageHelper {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string]$ExecutablePath,
        [Parameter(Mandatory = $true)][string]$Version,
        [Parameter(Mandatory = $true)]$GitState
    )

    $packageScript = Join-Path $RepositoryRoot 'scripts\package-local.ps1'
    if (-not (Test-Path -LiteralPath $packageScript -PathType Leaf)) {
        throw "Local package helper is missing: $packageScript"
    }
    $packageDirectory = Join-Path $RunDirectory 'package'
    $resultPath = Join-Path $RunDirectory 'package-result.json'
    # package-local uses the explicit `-local` SemVer label for Inno's
    # VersionInfo metadata; the local manifest itself keeps the source basis
    # version from web/package.json.
    $packageVersion = if ($Version -match '-local$') { $Version } else { $Version + '-local' }
    $arguments = @(
        '-ExecutablePath', (Get-LocalBuildAbsolutePath $ExecutablePath),
        '-OutputDirectory', (Get-LocalBuildAbsolutePath $packageDirectory),
        '-SourceRoot', (Get-LocalBuildAbsolutePath $RepositoryRoot),
        '-Version', $packageVersion,
        '-Commit', [string]$GitState.Head,
        '-ResultPath', (Get-LocalBuildAbsolutePath $resultPath)
    )

    [void](Invoke-LocalBuildChildScript -RepositoryRoot $RepositoryRoot -RunDirectory $RunDirectory -Stage 'package' `
        -ScriptPath $packageScript -Arguments $arguments -TimeoutSeconds 1800)
    if (-not (Test-Path -LiteralPath $resultPath -PathType Leaf)) {
        throw "Local package helper completed without writing its result contract: $resultPath"
    }
    try {
        $result = Get-Content -LiteralPath $resultPath -Raw -Encoding UTF8 | ConvertFrom-Json -ErrorAction Stop
    } catch {
        throw "Local package helper wrote invalid result JSON: $resultPath"
    }
    [void](Assert-ExactPropertySet -Value $result -Expected @('setupPath', 'installerPath', 'outputPath', 'sha256', 'executableSha256', 'version'))
    if ([string]::IsNullOrWhiteSpace([string]$result.setupPath) -or
        [string]::IsNullOrWhiteSpace([string]$result.sha256) -or
        [string]::IsNullOrWhiteSpace([string]$result.executableSha256) -or
        [string]$result.version -cne $packageVersion) {
        throw 'Local package helper result does not match the requested package contract.'
    }
    return $result
}

function Assert-LocalBuildExecutableMetadata {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$ExecutablePath,
        [Parameter(Mandatory = $true)][string]$ExpectedCommit,
        [Parameter(Mandatory = $true)][string]$RunDirectory
    )

    $outputPath = Join-Path (Join-Path $RunDirectory 'logs') 'version-json.out.log'
    $errorPath = Join-Path (Join-Path $RunDirectory 'logs') 'version-json.err.log'
    $result = Invoke-LocalBuildProcess -FilePath $ExecutablePath -ArgumentList @('--version-json') -TimeoutSeconds 30 `
        -StandardOutputPath $outputPath -StandardErrorPath $errorPath
    if ($result.ExitCode -ne 0) { throw "Local executable --version-json failed with exit code $($result.ExitCode). See $errorPath" }
    try { $metadata = $result.StandardOutput | ConvertFrom-Json -ErrorAction Stop } catch { throw 'Local executable returned invalid --version-json output.' }
    [void](Assert-ExactPropertySet -Value $metadata -Expected @('name', 'version', 'commit', 'release', 'target', 'roborevCompatibility'))
    if ($metadata.name -cne 'roborev-dashboard' -or
        $metadata.version -cne 'dev' -or
        $metadata.commit -cne $ExpectedCommit -or
        $metadata.release -ne $false -or
        $metadata.target -cne 'windows/amd64' -or
        $metadata.roborevCompatibility -cne 'v0.64.0') {
        throw 'Local executable provenance does not match the current development checkout.'
    }
    return $metadata
}

function Resolve-LocalPackageSetupPath {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string]$PackageDirectory,
        [Parameter()][DateTime]$NotBefore = [DateTime]::MinValue
    )

    [void](Assert-NoReparseTree -Path (Get-LocalBuildAbsolutePath $RunDirectory))
    $setupPath = Join-Path $PackageDirectory 'RoboRev-Dashboard-Setup.exe'
    if (-not (Test-Path -LiteralPath $setupPath -PathType Leaf)) {
        throw "Local package helper did not produce the expected Setup.exe: $setupPath"
    }
    $setupPath = Get-LocalBuildAbsolutePath $setupPath
    if (-not (Test-LocalBuildPathWithin -Path $setupPath -Root $RunDirectory)) {
        throw "Local package helper returned a Setup.exe outside this run's staging directory: $setupPath"
    }
    if (-not (Test-Path -LiteralPath $setupPath -PathType Leaf)) { throw "Fresh local Setup.exe is missing: $setupPath" }
    [void](Assert-NoReparsePoint -Path $setupPath)
    $setupItem = Get-Item -LiteralPath $setupPath -Force
    if ($NotBefore -ne [DateTime]::MinValue -and $setupItem.LastWriteTimeUtc -lt $NotBefore.ToUniversalTime().AddSeconds(-2)) {
        throw "Local package helper returned an older Setup.exe; refusing to run a stale installer: $setupPath"
    }
    return $setupPath
}

function Publish-LocalBuildInstaller {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$RunDirectory,
        [Parameter(Mandatory = $true)][string]$SetupPath,
        [Parameter(Mandatory = $true)][object]$Manifest
    )

    $root = Get-LocalBuildAbsolutePath $RepositoryRoot
    $setup = Get-LocalBuildAbsolutePath $SetupPath
    $destinationDirectory = Join-Path $root 'dist\installer'
    [void](Assert-SafeExistingParent -Path $destinationDirectory)
    New-Item -ItemType Directory -Path $destinationDirectory -Force | Out-Null
    [void](Assert-NoReparsePoint -Path $destinationDirectory)
    $temporary = Join-Path $destinationDirectory ('.RoboRev-Dashboard-Setup-' + [guid]::NewGuid().ToString('N') + '.tmp')
    $temporaryHash = $temporary + '.sha256'
    $destination = Join-Path $destinationDirectory $script:LocalBuildArtifactName
    $destinationHash = $destination + '.sha256'
    if (Test-Path -LiteralPath $destination) {
        [void](Assert-NoReparsePoint -Path $destination)
        if (Test-Path -LiteralPath $destination -PathType Container) { throw "Published local installer path is a directory: $destination" }
    }
    if (Test-Path -LiteralPath $destinationHash) {
        [void](Assert-NoReparsePoint -Path $destinationHash)
        if (Test-Path -LiteralPath $destinationHash -PathType Container) { throw "Published local installer hash path is a directory: $destinationHash" }
    }
    try {
        Copy-Item -LiteralPath $setup -Destination $temporary -Force -ErrorAction Stop
        Move-Item -LiteralPath $temporary -Destination $destination -Force -ErrorAction Stop
        Write-LocalBuildUtf8NoBom -Path $temporaryHash -Content ((Get-LocalBuildSha256 $setup) + "  " + $script:LocalBuildArtifactName + "`n")
        Move-Item -LiteralPath $temporaryHash -Destination $destinationHash -Force -ErrorAction Stop
    } finally {
        if (Test-Path -LiteralPath $temporary) { Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue }
        if (Test-Path -LiteralPath $temporaryHash) { Remove-Item -LiteralPath $temporaryHash -Force -ErrorAction SilentlyContinue }
    }
    if ((Get-LocalBuildSha256 $destination) -cne (Get-LocalBuildSha256 $setup)) { throw 'Published local installer hash does not match the verified staged installer.' }
    if (([IO.File]::ReadAllText($destinationHash, (New-Object System.Text.UTF8Encoding($false))) -cne ((Get-LocalBuildSha256 $destination) + "  " + $script:LocalBuildArtifactName + "`n"))) { throw 'Published local installer SHA-256 sidecar does not match the verified Setup.exe.' }
    [void](Assert-NoReparsePoint -Path $destination)
    $manifestPath = Join-Path $destinationDirectory $script:LocalBuildManifestName
    Write-LocalBuildUtf8NoBom -Path $manifestPath -Content (($Manifest | ConvertTo-Json -Compress -Depth 8) + "`n")
    return [pscustomobject]([ordered]@{ SetupPath = $destination; ManifestPath = $manifestPath })
}

function Get-LocalInstalledDashboardExecutable {
    [CmdletBinding()]
    param()
    $root = Get-LocalBuildInstallRoot
    $candidates = @(
        (Join-Path $root 'roborev-dashboard.exe'),
        (Join-Path $root 'current\roborev-dashboard.exe')
    )
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return (Get-LocalBuildAbsolutePath $candidate) }
    }
    return $null
}

function Invoke-LocalDashboardInstaller {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$SetupPath,
        [Parameter(Mandatory = $true)][string]$LogPath,
        [Parameter(Mandatory = $true)][string]$ResultPath,
        [switch]$Silent
    )

    # /DASHBOARDNOLAUNCH is a private [Code] parameter implemented by the
    # checked-in Inno script.  It keeps Setup's normal consent/progress UI but
    # leaves launch to this wrapper after the installed hash and instance
    # status have been verified.
    $resultFullPath = Get-LocalBuildAbsolutePath $ResultPath
    $resultParent = Split-Path -Parent $resultFullPath
    if (-not (Test-Path -LiteralPath $resultParent -PathType Container)) { throw "Installer result directory does not exist: $resultParent" }
    [void](Assert-SafeExistingParent -Path $resultParent)
    [void](Assert-NoReparsePoint -Path $resultParent)
    if (Test-Path -LiteralPath $resultFullPath) {
        [void](Assert-NoReparsePoint -Path $resultFullPath)
        Remove-Item -LiteralPath $resultFullPath -Force -ErrorAction Stop
    }
    $arguments = @('/NORESTART', '/LOG=' + (Get-LocalBuildAbsolutePath $LogPath), '/DASHBOARDRESULT=' + $resultFullPath, '/DASHBOARDNOLAUNCH')
    if ($Silent) { $arguments = @('/VERYSILENT', '/SUPPRESSMSGBOXES') + $arguments }
    $result = Invoke-LocalBuildProcess -FilePath $SetupPath -ArgumentList $arguments `
        -WorkingDirectory (Split-Path -Parent (Get-LocalBuildAbsolutePath $SetupPath)) `
        -StandardOutputPath ($LogPath + '.stdout') -StandardErrorPath ($LogPath + '.stderr') -TimeoutSeconds 3600
    if (-not (Test-Path -LiteralPath $resultFullPath -PathType Leaf)) {
        $exception = New-Object System.InvalidOperationException ("Dashboard installer did not write its result state. See $LogPath")
        $exception.Data['InstallerExitCode'] = [int]$result.ExitCode
        $exception.Data['ExitCode'] = 1
        throw $exception
    }
    [void](Assert-NoReparsePoint -Path $resultFullPath)
    try { $installerState = Get-Content -LiteralPath $resultFullPath -Raw -Encoding UTF8 | ConvertFrom-Json -ErrorAction Stop } catch {
        $exception = New-Object System.InvalidOperationException ("Dashboard installer wrote invalid result state. See $LogPath")
        $exception.Data['InstallerExitCode'] = [int]$result.ExitCode
        $exception.Data['ExitCode'] = 1
        throw $exception
    }
    [void](Assert-ExactPropertySet -Value $installerState -Expected @('state', 'message'))
    $state = [string]$installerState.state
    $message = [string]$installerState.message
    $allowedMessages = @{
        ready = @('setup-ready')
        failed = @('mutex', 'precondition', 'verification')
        installing = @('copy-started')
        succeeded = @('verified')
    }
    if (-not $allowedMessages.ContainsKey($state) -or $allowedMessages[$state] -notcontains $message) {
        throw "Dashboard installer returned an unknown result state: $state"
    }
    # Inno's documented code 2 is only a clean pre-install cancel when Setup
    # successfully wrote ready/setup-ready and never entered installing.
    if ($result.ExitCode -eq 2 -and $state -eq 'ready') {
        return [pscustomobject]@{ Canceled = $true; Partial = $false; ExitCode = 2; State = $state; Message = $message }
    }
    if (($result.ExitCode -eq 2 -or $result.ExitCode -eq 5) -and $state -eq 'installing') {
        return [pscustomobject]@{ Canceled = $true; Partial = $true; ExitCode = [int]$result.ExitCode; State = $state; Message = $message }
    }
    if ($result.ExitCode -eq 0 -and $state -eq 'succeeded') {
        return [pscustomobject]@{ Canceled = $false; Partial = $false; ExitCode = 0; State = $state; Message = $message }
    }
    $detail = if ([string]::IsNullOrWhiteSpace($message)) { " See $LogPath" } else { " $message See $LogPath" }
    $exception = New-Object System.InvalidOperationException ("Dashboard installer failed with exit code $($result.ExitCode).$detail")
    $exception.Data['InstallerExitCode'] = [int]$result.ExitCode
    $exception.Data['ExitCode'] = 1
    throw $exception
}

function Wait-LocalInstalledDashboardReady {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$ExecutablePath,
        [int]$TimeoutSeconds = 30
    )

    $exe = Get-LocalBuildAbsolutePath $ExecutablePath
    $deadline = [Diagnostics.Stopwatch]::StartNew()
    while ($deadline.Elapsed.TotalSeconds -lt $TimeoutSeconds) {
        $probe = Invoke-LocalBuildProcess -FilePath $exe -ArgumentList @('--installed-status') -TimeoutSeconds 5
        if ($probe.ExitCode -eq 0) {
            try { $status = $probe.StandardOutput | ConvertFrom-Json -ErrorAction Stop } catch { $status = $null }
            $uri = $null
            $validLoopback = $false
            if ($status -and -not [string]::IsNullOrWhiteSpace([string]$status.url)) {
                try {
                    $uri = New-Object System.Uri ([string]$status.url)
                    $validLoopback = $uri.IsAbsoluteUri -and $uri.Scheme -eq 'http' -and $uri.IsLoopback -and
                        $uri.Port -gt 0 -and $uri.Port -le 65535 -and ($uri.AbsolutePath -eq '/' -or [string]::IsNullOrEmpty($uri.AbsolutePath)) -and
                        [string]::IsNullOrEmpty($uri.Query) -and [string]::IsNullOrEmpty($uri.Fragment)
                } catch { $validLoopback = $false }
            }
            if ($status -and [bool]$status.running -and [int64]$status.pid -gt 0 -and
                -not [string]::IsNullOrWhiteSpace([string]$status.instanceId) -and $validLoopback) {
                $reportedPath = [string]$status.executablePath
                if ([string]::IsNullOrWhiteSpace($reportedPath) -or
                    -not (Get-LocalBuildAbsolutePath $reportedPath).Equals($exe, [StringComparison]::OrdinalIgnoreCase)) {
                    throw "Installed Dashboard reported an unexpected executable path: $reportedPath"
                }
                return $status
            }
        }
        Start-Sleep -Milliseconds 500
    }
    throw "Installed Dashboard did not report ready through --installed-status within $TimeoutSeconds seconds."
}

function Invoke-LocalBuild {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [switch]$NoInstall,
        [switch]$SilentInstall
    )

    Assert-LocalBuildOptions -NoInstall:$NoInstall -SilentInstall:$SilentInstall
    $root = Get-LocalBuildAbsolutePath $RepositoryRoot
    $lock = $null
    $transcriptStarted = $false
    $run = $null
    $stage = 'preflight'
    $startedAt = Get-Date
    try {
        if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'The local Windows installer flow is supported on Windows only.' }
        if (-not (Test-Path -LiteralPath $root -PathType Container)) { throw "Repository root does not exist: $root" }
        $lock = Enter-LocalBuildLock -RepositoryRoot $root
        $run = New-LocalBuildRunDirectory -RepositoryRoot $root
        $script:LocalBuildRunDirectory = $run
        [void](Move-LocalBuildPublishedArtifacts -RepositoryRoot $root -RunDirectory $run)
        $staleFailure = Join-Path $root 'dist\local-build\last-failure.txt'
        if (Test-Path -LiteralPath $staleFailure -PathType Leaf) { Remove-Item -LiteralPath $staleFailure -Force -ErrorAction Stop }
        $logPath = Join-Path (Join-Path $run 'logs') 'build-local.log'
        Start-Transcript -LiteralPath $logPath -ErrorAction Stop | Out-Null
        $transcriptStarted = $true
        Set-LocalBuildCurrentStage 'preflight'
        $version = Get-LocalBuildVersion -RepositoryRoot $root

        Set-LocalBuildCurrentStage 'prerequisites'
        $installScript = Join-Path $root 'scripts\install.ps1'
        Invoke-LocalBuildChildScript -RepositoryRoot $root -RunDirectory $run -Stage 'prerequisites' -ScriptPath $installScript -TimeoutSeconds 3600 | Out-Null

        # install.ps1 refreshes only its child process PATH. Rebuild the
        # parent inventory from machine/user/current PATH before any later
        # command resolves tools, and retain the resolved Git executable.
        $env:Path = Merge-DevelopmentProcessPath `
            -MachinePath ([Environment]::GetEnvironmentVariable('Path', 'Machine')) `
            -UserPath ([Environment]::GetEnvironmentVariable('Path', 'User')) `
            -CurrentPath $env:Path
        $tooling = Assert-DevelopmentPrerequisites -RootPath $root
        $gitPath = $tooling.GitPath
        $git = Get-LocalBuildGitState -RepositoryRoot $root -GitPath $gitPath
        $sourceBefore = Get-LocalBuildSourceFingerprint -RepositoryRoot $root -GitPath $gitPath
        Set-LocalBuildCurrentStage 'tests'
        $testScript = Join-Path $root 'scripts\test.ps1'
        Invoke-LocalBuildChildScript -RepositoryRoot $root -RunDirectory $run -Stage 'tests' -ScriptPath $testScript -TimeoutSeconds 3600 | Out-Null

        # Re-read HEAD immediately before compiling so a concurrent source edit
        # cannot be mislabeled as the earlier checkout in the local manifest.
        $buildGit = Get-LocalBuildGitState -RepositoryRoot $root -GitPath $gitPath
        if ($buildGit.Head -cne $git.Head) { throw 'Git HEAD changed during the local build. Retry from the intended checkout.' }
        $git = $buildGit

        Set-LocalBuildCurrentStage 'build'
        $builtExecutable = Join-Path $run 'roborev-dashboard.exe'
        $buildScript = Join-Path $root 'scripts\build.ps1'
        Invoke-LocalBuildChildScript -RepositoryRoot $root -RunDirectory $run -Stage 'build' -ScriptPath $buildScript `
            -Arguments @('-OutputPath', $builtExecutable, '-SkipFrontendBuild') -TimeoutSeconds 3600 | Out-Null
        if (-not (Test-Path -LiteralPath $builtExecutable -PathType Leaf)) { throw "Build completed without producing $builtExecutable" }
        Assert-LocalBuildExecutableMetadata -ExecutablePath $builtExecutable -ExpectedCommit $git.Head -RunDirectory $run | Out-Null

        Set-LocalBuildCurrentStage 'package-tools'
        $prepareScript = Join-Path $root 'scripts\prepare-inno.ps1'
        if (Test-Path -LiteralPath $prepareScript -PathType Leaf) {
            Invoke-LocalBuildChildScript -RepositoryRoot $root -RunDirectory $run -Stage 'package-tools' -ScriptPath $prepareScript `
                -Arguments @('-RootPath', $root) -TimeoutSeconds 1800 | Out-Null
        } else { throw "Inno Setup preparation script is missing: $prepareScript" }

        $packageGit = Get-LocalBuildGitState -RepositoryRoot $root -GitPath $gitPath
        if ($packageGit.Head -cne $git.Head) { throw 'Git HEAD changed before packaging. Retry from the intended checkout.' }
        $git = $packageGit
        Set-LocalBuildCurrentStage 'package'
        $packageResult = Invoke-LocalPackageHelper -RepositoryRoot $root -RunDirectory $run -ExecutablePath $builtExecutable -Version $version -GitState $git
        $stagedSetup = Resolve-LocalPackageSetupPath -RunDirectory $run -PackageDirectory (Join-Path $run 'package') -NotBefore $startedAt
        $reportedSetup = Get-LocalBuildAbsolutePath ([string]$packageResult.setupPath)
        if (-not $reportedSetup.Equals($stagedSetup, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Local package helper result points outside the expected staging path: $reportedSetup"
        }
        foreach ($reportedName in @('installerPath', 'outputPath')) {
            $reportedPath = Get-LocalBuildAbsolutePath ([string]$packageResult.$reportedName)
            if (-not $reportedPath.Equals($reportedSetup, [StringComparison]::OrdinalIgnoreCase)) {
                throw "Local package helper result $reportedName does not identify the staged Setup.exe."
            }
        }
        if ([string]$packageResult.sha256 -cne (Get-LocalBuildSha256 $stagedSetup)) {
            throw 'Local package helper result SHA-256 does not match the staged Setup.exe.'
        }
        if ([string]$packageResult.executableSha256 -cne (Get-LocalBuildSha256 $builtExecutable)) {
            throw 'Local package helper result executable SHA-256 does not match the staged Dashboard executable.'
        }
        $sourceAfter = Get-LocalBuildSourceFingerprint -RepositoryRoot $root -GitPath $gitPath
        if ($sourceAfter.Digest -cne $sourceBefore.Digest) {
            throw 'Source files changed during the local build. Retry after the checkout is stable.'
        }

        Set-LocalBuildCurrentStage 'package-verify'
        $exeHash = Get-LocalBuildSha256 $builtExecutable
        $setupHash = Get-LocalBuildSha256 $stagedSetup
        $manifest = New-LocalBuildManifest -Version $version -Commit $git.Head -Dirty ([bool]$git.Dirty) -ExecutablePath $builtExecutable -InstallerPath $stagedSetup
        $runManifestPath = Join-Path $run $script:LocalBuildManifestName
        Write-LocalBuildUtf8NoBom -Path $runManifestPath -Content (($manifest | ConvertTo-Json -Compress -Depth 8) + "`n")
        if ((Get-LocalBuildSha256 $builtExecutable) -cne $exeHash -or (Get-LocalBuildSha256 $stagedSetup) -cne $setupHash) { throw 'Local build artifacts changed during package verification.' }
        $published = Publish-LocalBuildInstaller -RepositoryRoot $root -RunDirectory $run -SetupPath $stagedSetup -Manifest $manifest

        if ($NoInstall) {
            Set-LocalBuildCurrentStage 'complete'
            Write-Host "Local installer ready: $($published.SetupPath)"
            Write-Host "Local manifest: $($published.ManifestPath)"
            return [pscustomobject]@{ SetupPath = $published.SetupPath; ManifestPath = $published.ManifestPath; RunDirectory = $run; Installed = $false }
        }

        Set-LocalBuildCurrentStage 'install'
        $installerLog = Join-Path (Join-Path $run 'logs') 'installer.log'
        $installerResult = Join-Path (Join-Path $run 'logs') 'installer-result.json'
        $outcome = Invoke-LocalDashboardInstaller -SetupPath $published.SetupPath -LogPath $installerLog -ResultPath $installerResult -Silent:$SilentInstall
        if ($outcome.Canceled) {
            $cancelStage = if ($outcome.Partial) { 'install-interrupted' } else { 'install-canceled' }
            Set-LocalBuildCurrentStage $cancelStage
            $cancelMessage = if ($outcome.Partial) {
                'Dashboard installation was canceled or aborted after installation work began; partial installation state is possible. Inspect the Setup log before retrying.'
            } else {
                'Dashboard installation was canceled before installation changes began. The previous installation was left untouched.'
            }
            $cancelException = New-Object System.InvalidOperationException $cancelMessage
            $cancelException.Data['ExitCode'] = [int]$outcome.ExitCode
            $cancelException.Data['Canceled'] = $true
            $cancelException.Data['Partial'] = [bool]$outcome.Partial
            throw $cancelException
        }

        Set-LocalBuildCurrentStage 'verify-install'
        $installed = Get-LocalInstalledDashboardExecutable
        if (-not $installed) { throw "Installer completed but the installed Dashboard executable was not found under $(Get-LocalBuildInstallRoot)." }
        if ((Get-LocalBuildSha256 $installed) -cne $exeHash) { throw "Installed Dashboard hash does not match this local build. Installed=$((Get-LocalBuildSha256 $installed)) Built=$exeHash" }
        Set-LocalBuildCurrentStage 'launch'
        Start-Process -FilePath $installed -ArgumentList @('--installed') -WorkingDirectory (Split-Path -Parent $installed) -ErrorAction Stop | Out-Null
        $status = Wait-LocalInstalledDashboardReady -ExecutablePath $installed
        Set-LocalBuildCurrentStage 'complete'
        Write-Host "Dashboard installed and ready: $installed (PID $($status.pid), URL $($status.url))"
        return [pscustomobject]@{ SetupPath = $published.SetupPath; ManifestPath = $published.ManifestPath; RunDirectory = $run; Installed = $installed; HashMatched = $true; Status = $status }
    } catch {
        $exitCode = 1
        if ($_.Exception.Data.Contains('ExitCode')) { $exitCode = [int]$_.Exception.Data['ExitCode'] }
        if ($run) {
            try {
                $stage = $script:LocalBuildCurrentStage
                Set-LocalBuildStage -RunDirectory $run -Stage $stage -ExitCode $exitCode
                Write-Host (Write-LocalBuildFailureMarker -RepositoryRoot $root -RunDirectory $run -Stage $stage -Message $_.Exception.Message -ExitCode $exitCode)
            } catch { Write-Host "Could not write local-build failure marker: $($_.Exception.Message)" }
        }
        throw
    } finally {
        if ($transcriptStarted) { try { Stop-Transcript | Out-Null } catch { } }
        if ($lock) { $lock.Dispose() }
        $script:LocalBuildRunDirectory = $null
    }
}

if ($MyInvocation.InvocationName -ne '.') {
    $repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
    try { Invoke-LocalBuild -RepositoryRoot $repoRoot -NoInstall:$NoInstall -SilentInstall:$SilentInstall | Out-Null; exit 0 }
    catch {
        try { [Console]::Error.WriteLine($_.Exception.Message) } catch { Write-Host $_.Exception.Message }
        $code = if ($_.Exception.Data.Contains('ExitCode')) { [int]$_.Exception.Data['ExitCode'] } else { 1 }
        exit $code
    }
}
