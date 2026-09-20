$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $scriptRoot 'local-build-common.ps1')
. (Join-Path $scriptRoot 'build-local.ps1')

function Assert-LocalBuildTrue {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "Assertion failed: $Message" }
}

function Assert-LocalBuildEqual {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -cne $Expected) { throw "Assertion failed: $Message (actual '$Actual', expected '$Expected')" }
}

# The production wrapper must import the real prerequisite function boundary
# before this test later replaces it with deterministic doubles.
Assert-LocalBuildTrue ($null -ne (Get-Command Merge-DevelopmentProcessPath -CommandType Function -ErrorAction SilentlyContinue)) 'local wrapper imports PATH refresh helper'
Assert-LocalBuildTrue ($null -ne (Get-Command Assert-DevelopmentPrerequisites -CommandType Function -ErrorAction SilentlyContinue)) 'local wrapper imports prerequisite assertion helper'

# The quoting helper is exercised with the cases that previously caused local
# wrappers to corrupt paths: spaces, Korean characters, quotes, and trailing
# backslashes.  This is a pure test; it does not alter a process environment.
$unicodeSegment = -join @([char]0xC791, [char]0xC5C5, [char]0x0020, [char]0xD3F4, [char]0xB354)
$quotedInput = 'E:\' + $unicodeSegment + '\RoboRev Dashboard\file.txt'
$quoted = ConvertTo-LocalBuildWindowsArgument -Value $quotedInput
Assert-LocalBuildEqual $quoted ('"' + $quotedInput + '"') 'Unicode and spaces remain one argv item'
$quotedQuote = ConvertTo-LocalBuildWindowsArgument -Value 'a"b\'
Assert-LocalBuildEqual $quotedQuote '"a\"b\\"' 'quotes and trailing backslashes use Windows argv escaping'
$unicodePath = -join @([char]0xD55C, [char]0xAE00, [char]0x0020, [char]0xACBD, [char]0xB85C)
Assert-LocalBuildEqual (ConvertTo-LocalBuildArgumentString -ArgumentList @('first', $unicodePath, '')) ('first "' + $unicodePath + '" ""') 'argument arrays are serialized without shell concatenation'

Assert-LocalBuildOptions
$conflictRejected = $false
try { Assert-LocalBuildOptions -NoInstall -SilentInstall } catch { $conflictRejected = $true }
Assert-LocalBuildTrue $conflictRejected 'NoInstall and SilentInstall conflict is rejected'

$tempRoot = Join-Path ([IO.Path]::GetTempPath()) ('roborev-dashboard-local-build-tests-' + [guid]::NewGuid().ToString('N'))
try {
    $repoRoot = Join-Path $tempRoot ('repo ' + $unicodePath)
    New-Item -ItemType Directory -Path (Join-Path $repoRoot 'web') -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $repoRoot 'dist\local-build') -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $repoRoot 'web\package.json'), '{"name":"fixture","version":"0.1.0"}', (New-Object System.Text.UTF8Encoding($false)))

    $version = Get-LocalBuildVersion -RepositoryRoot $repoRoot
    Assert-LocalBuildEqual $version '0.1.0' 'version comes from web/package.json'

    $fakeGitPath = (Get-Command git -CommandType Application | Select-Object -First 1).Source
    & $fakeGitPath -C $repoRoot init --quiet
    if ($LASTEXITCODE -ne 0) { throw 'Could not initialize the local-build fingerprint fixture repository.' }
    $fingerprintA = Get-LocalBuildSourceFingerprint -RepositoryRoot $repoRoot -GitPath $fakeGitPath
    $trackedFingerprintFile = Join-Path $repoRoot 'tracked-source.txt'
    [IO.File]::WriteAllText($trackedFingerprintFile, 'one', (New-Object System.Text.UTF8Encoding($false)))
    & $fakeGitPath -C $repoRoot add -- tracked-source.txt
    if ($LASTEXITCODE -ne 0) { throw 'Could not stage the tracked fingerprint fixture.' }
    $fingerprintTracked = Get-LocalBuildSourceFingerprint -RepositoryRoot $repoRoot -GitPath $fakeGitPath
    Remove-Item -LiteralPath $trackedFingerprintFile -Force
    $fingerprintDeleted = Get-LocalBuildSourceFingerprint -RepositoryRoot $repoRoot -GitPath $fakeGitPath
    Assert-LocalBuildTrue ($fingerprintTracked.Digest -cne $fingerprintDeleted.Digest) 'source fingerprint records intentional tracked deletion'
    $fingerprintFile = Join-Path $repoRoot 'source.txt'
    [IO.File]::WriteAllText($fingerprintFile, 'one', (New-Object System.Text.UTF8Encoding($false)))
    $fingerprintB = Get-LocalBuildSourceFingerprint -RepositoryRoot $repoRoot -GitPath $fakeGitPath
    Assert-LocalBuildTrue ($fingerprintA.Digest -cne $fingerprintB.Digest) 'source fingerprint detects an untracked source edit'

    $runA = New-LocalBuildRunDirectory -RepositoryRoot $repoRoot
    $runB = New-LocalBuildRunDirectory -RepositoryRoot $repoRoot
    Assert-LocalBuildTrue (-not $runA.Equals($runB, [StringComparison]::OrdinalIgnoreCase)) 'each run receives a unique staging directory'
    Set-LocalBuildStage -RunDirectory $runA -Stage 'tests'
    Assert-LocalBuildTrue (Test-Path -LiteralPath (Join-Path $runA 'stage.json') -PathType Leaf) 'stage state is persisted per run'
    $packageChildResult = Invoke-LocalBuildChildScript -RepositoryRoot $repoRoot -RunDirectory $runA -Stage 'package-tests-child' `
        -ScriptPath (Join-Path $scriptRoot 'package.tests.ps1') -TimeoutSeconds 120
    Assert-LocalBuildEqual $packageChildResult.ExitCode 0 'package tests pass through the fresh typed PowerShell 5.1 child driver'

    $lock = Enter-LocalBuildLock -RepositoryRoot $repoRoot
    try {
        $lockRejected = $false
        try { Enter-LocalBuildLock -RepositoryRoot $repoRoot | Out-Null } catch { $lockRejected = $true }
        Assert-LocalBuildTrue $lockRejected 'concurrent local builds are rejected by a shared checkout lock'
    } finally { $lock.Dispose() }

    $exePath = Join-Path $runA 'roborev-dashboard.exe'
    $setupPath = Join-Path (Join-Path $runA 'package') 'RoboRev-Dashboard-Setup.exe'
    [IO.File]::WriteAllText($exePath, 'current-exe', (New-Object System.Text.UTF8Encoding($false)))
    [IO.File]::WriteAllText($setupPath, 'current-setup', (New-Object System.Text.UTF8Encoding($false)))
    $manifest = New-LocalBuildManifest -Version $version -Commit ([string]::new('a', 40)) -Dirty $true -ExecutablePath $exePath -InstallerPath $setupPath
    Assert-LocalBuildTrue (-not [bool]$manifest.release -and [bool]$manifest.dirty -and $manifest.buildVersion -ceq 'dev') 'local manifest identifies dirty development output'
    Assert-LocalBuildEqual $manifest.executable.sha256 (Get-LocalBuildSha256 $exePath) 'manifest stores executable hash'
    Assert-LocalBuildEqual $manifest.installer.sha256 (Get-LocalBuildSha256 $setupPath) 'manifest stores installer hash'

    # A stale Setup.exe cannot satisfy the package freshness check, even if it
    # happens to have the expected name.  The production run directory is
    # unique, and this test protects the explicit old-installer guard.
    $stale = Join-Path (Join-Path $runB 'package') 'RoboRev-Dashboard-Setup.exe'
    [IO.File]::WriteAllText($stale, 'old', (New-Object System.Text.UTF8Encoding($false)))
    (Get-Item -LiteralPath $stale).LastWriteTimeUtc = [DateTime]::UtcNow.AddHours(-2)
    $staleRejected = $false
    try { Resolve-LocalPackageSetupPath -RunDirectory $runB -PackageDirectory (Join-Path $runB 'package') -NotBefore ([DateTime]::UtcNow.AddMinutes(-1)) | Out-Null } catch { $staleRejected = $true }
    Assert-LocalBuildTrue $staleRejected 'stale Setup.exe is rejected'

    $cmd = Join-Path $env:SystemRoot 'System32\cmd.exe'
    if (Test-Path -LiteralPath $cmd -PathType Leaf) {
        $failureCode = $false
        try {
            Invoke-LocalBuildRequiredProcess -FilePath $cmd -ArgumentList @('/c', 'exit', '23') -WorkingDirectory $repoRoot `
                -StandardOutputPath (Join-Path $runA 'logs\failure.out.log') -StandardErrorPath (Join-Path $runA 'logs\failure.err.log') | Out-Null
        } catch {
            $failureCode = $_.Exception.Data.Contains('ExitCode') -and [int]$_.Exception.Data['ExitCode'] -eq 23
        }
        Assert-LocalBuildTrue $failureCode 'native child failure code is retained for the top-level wrapper'
    }

    # A failing test/build child is injected through the helper boundary only;
    # the production command line has no test bypass or failure-injection flag.
    $failingScript = Join-Path $tempRoot 'deterministic-failure.ps1'
    [IO.File]::WriteAllText($failingScript, "`$ErrorActionPreference = 'Stop'`r`nexit 41`r`n", (New-Object System.Text.UTF8Encoding($false)))
    $childFailureCode = $false
    try {
        Invoke-LocalBuildChildScript -RepositoryRoot $repoRoot -RunDirectory $runA -Stage 'tests' -ScriptPath $failingScript -TimeoutSeconds 30 | Out-Null
    } catch {
        $childFailureCode = $_.Exception.Data.Contains('ExitCode') -and [int]$_.Exception.Data['ExitCode'] -eq 41
    }
    Assert-LocalBuildTrue $childFailureCode 'injected child failure is deterministic and keeps its exit code'

    # The package worker's checked result contract is consumed through a typed
    # ResultPath, rather than scraping child console output or guessing among
    # legacy parameter/property aliases.
    $fixtureScripts = Join-Path $repoRoot 'scripts'
    New-Item -ItemType Directory -Path $fixtureScripts -Force | Out-Null
    $fixturePackage = @'
[CmdletBinding()]
param([string]$ExecutablePath, [string]$OutputDirectory, [string]$SourceRoot, [string]$Version, [string]$Commit, [string]$ResultPath)
$setup = Join-Path $OutputDirectory 'RoboRev-Dashboard-Setup.exe'
[IO.File]::WriteAllText($setup, 'fixture-setup', (New-Object System.Text.UTF8Encoding($false)))
$result = [ordered]@{ setupPath = $setup; installerPath = $setup; outputPath = $setup; sha256 = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant(); executableSha256 = (Get-FileHash -LiteralPath $ExecutablePath -Algorithm SHA256).Hash.ToLowerInvariant(); version = $Version }
[IO.File]::WriteAllText($ResultPath, (($result | ConvertTo-Json -Compress) + "`n"), (New-Object System.Text.UTF8Encoding($false)))
'@
    [IO.File]::WriteAllText((Join-Path $fixtureScripts 'package-local.ps1'), $fixturePackage, (New-Object System.Text.UTF8Encoding($false)))
    $packageContract = Invoke-LocalPackageHelper -RepositoryRoot $repoRoot -RunDirectory $runA -ExecutablePath $exePath -Version $version -GitState ([pscustomobject]@{ Head = [string]::new('a', 40) })
    Assert-LocalBuildEqual $packageContract.version ($version + '-local') 'package helper consumes the fixed result contract version'
    Assert-LocalBuildEqual ([string]$packageContract.setupPath) (Join-Path (Join-Path $runA 'package') 'RoboRev-Dashboard-Setup.exe') 'package helper consumes setupPath from ResultPath'
    if (Test-Path -LiteralPath $cmd -PathType Leaf) {
        $nativeFailingScript = Join-Path $tempRoot 'native-failure.ps1'
        $nativePath = (Join-Path $scriptRoot 'native-command.ps1').Replace("'", "''")
        $cmdPath = $cmd.Replace("'", "''")
        $nativeText = ". '$nativePath'`r`nInvoke-NativeCommand -FilePath '$cmdPath' -ArgumentList @('/c', 'exit 23')`r`n"
        # Windows PowerShell 5.1 needs a BOM when a script literal embeds a
        # non-ASCII checkout path; argument transport itself remains Unicode.
        [IO.File]::WriteAllText($nativeFailingScript, $nativeText, (New-Object System.Text.UTF8Encoding($true)))
        $nativeChildCode = $false
        try {
            Invoke-LocalBuildChildScript -RepositoryRoot $repoRoot -RunDirectory $runA -Stage 'build' -ScriptPath $nativeFailingScript -TimeoutSeconds 30 | Out-Null
        } catch {
            $nativeChildCode = $_.Exception.Data.Contains('ExitCode') -and [int]$_.Exception.Data['ExitCode'] -eq 23
        }
    Assert-LocalBuildTrue $nativeChildCode 'native-command failure code survives the child PowerShell boundary'
    }

    # Exercise the whole orchestration boundary with injected stage doubles.
    # These hooks live only in this test scope; the production command line has
    # no failure-injection or test-bypass switch.
    $pipelineRoot = Join-Path $tempRoot 'pipeline fixture'
    New-Item -ItemType Directory -Path $pipelineRoot -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $pipelineRoot 'scripts') -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $pipelineRoot 'scripts\prepare-inno.ps1'), '', (New-Object System.Text.UTF8Encoding($false)))
    $publishedPath = Join-Path $pipelineRoot 'dist\installer\RoboRev-Dashboard-Setup.exe'
    New-Item -ItemType Directory -Path (Split-Path -Parent $publishedPath) -Force | Out-Null
    [IO.File]::WriteAllText($publishedPath, 'previous-setup', (New-Object System.Text.UTF8Encoding($false)))
    [IO.File]::WriteAllText(($publishedPath + '.sha256'), 'previous-hash`n', (New-Object System.Text.UTF8Encoding($false)))
    [IO.File]::WriteAllText((Join-Path (Split-Path -Parent $publishedPath) 'roborev-dashboard.local.manifest.json'), '{"version":"old"}', (New-Object System.Text.UTF8Encoding($false)))
    $previousHash = Get-LocalBuildSha256 $publishedPath
    $script:MockFailStage = ''
    $script:MockInstallerCalls = 0
    $realNewManifest = (Get-Item Function:\New-LocalBuildManifest).ScriptBlock
    $realInstaller = (Get-Item Function:\Invoke-LocalDashboardInstaller).ScriptBlock
    Set-Item Function:\Get-LocalBuildVersion -Value { param([string]$RepositoryRoot) '0.1.0' }
    Set-Item Function:\Merge-DevelopmentProcessPath -Value { param([string]$MachinePath, [string]$UserPath, [string]$CurrentPath) $CurrentPath }
    Set-Item Function:\Assert-DevelopmentPrerequisites -Value { param([string]$RootPath) [pscustomobject]@{ GitPath = 'git.exe' } }
    Set-Item Function:\Get-LocalBuildGitState -Value { param([string]$RepositoryRoot, [string]$GitPath) [pscustomobject]@{ Head = [string]::new('a', 40); Dirty = $true; Status = ' M fixture' } }
    Set-Item Function:\Get-LocalBuildSourceFingerprint -Value { param([string]$RepositoryRoot, [string]$GitPath) [pscustomobject]@{ Digest = 'fixture-digest'; Count = 1 } }
    Set-Item Function:\Assert-LocalBuildExecutableMetadata -Value { param([string]$ExecutablePath, [string]$ExpectedCommit, [string]$RunDirectory) [pscustomobject]@{ version = 'dev'; release = $false } }
    Set-Item Function:\Invoke-LocalBuildChildScript -Value {
        param([string]$RepositoryRoot, [string]$RunDirectory, [string]$Stage, [string]$ScriptPath, [string[]]$Arguments, [int]$TimeoutSeconds)
        if ($script:MockFailStage -eq $Stage) {
            $errorObject = New-Object System.InvalidOperationException ("mock $Stage failure")
            $errorObject.Data['ExitCode'] = @{ tests = 61; build = 62; 'package-tools' = 63 }[$Stage]
            throw $errorObject
        }
        if ($Stage -eq 'build') {
            $outputIndex = [Array]::IndexOf($Arguments, '-OutputPath')
            [IO.File]::WriteAllText([string]$Arguments[$outputIndex + 1], 'new-executable', (New-Object System.Text.UTF8Encoding($false)))
        }
        [pscustomobject]@{ ExitCode = 0 }
    }
    Set-Item Function:\Invoke-LocalPackageHelper -Value {
        param([string]$RepositoryRoot, [string]$RunDirectory, [string]$ExecutablePath, [string]$Version, $GitState)
        if ($script:MockFailStage -eq 'package') {
            $errorObject = New-Object System.InvalidOperationException 'mock package failure'
            $errorObject.Data['ExitCode'] = 63
            throw $errorObject
        }
        $setup = Join-Path (Join-Path $RunDirectory 'package') 'RoboRev-Dashboard-Setup.exe'
        [IO.File]::WriteAllText($setup, 'new-setup', (New-Object System.Text.UTF8Encoding($false)))
        [pscustomobject]@{
            setupPath = $setup
            installerPath = $setup
            outputPath = $setup
            sha256 = Get-LocalBuildSha256 $setup
            executableSha256 = Get-LocalBuildSha256 $ExecutablePath
            version = $Version + '-local'
        }
    }
    Set-Item Function:\New-LocalBuildManifest -Value {
        param([string]$Version, [string]$Commit, [bool]$Dirty, [string]$ExecutablePath, [string]$InstallerPath)
        if ($script:MockFailStage -eq 'verify') {
            $errorObject = New-Object System.InvalidOperationException 'mock verify failure'
            $errorObject.Data['ExitCode'] = 64
            throw $errorObject
        }
        & $realNewManifest -Version $Version -Commit $Commit -Dirty:$Dirty -ExecutablePath $ExecutablePath -InstallerPath $InstallerPath
    }
    Set-Item Function:\Invoke-LocalDashboardInstaller -Value {
        param([string]$SetupPath, [string]$LogPath, [switch]$Silent)
        $script:MockInstallerCalls++
        [pscustomobject]@{ Canceled = $false; Partial = $false; ExitCode = 0 }
    }

    foreach ($failure in @(
        [pscustomobject]@{ Name = 'tests'; Code = 61 },
        [pscustomobject]@{ Name = 'build'; Code = 62 },
        [pscustomobject]@{ Name = 'package'; Code = 63 },
        [pscustomobject]@{ Name = 'verify'; Code = 64 }
    )) {
        $script:MockFailStage = $failure.Name
        $script:MockInstallerCalls = 0
        $caught = $false
        try { Invoke-LocalBuild -RepositoryRoot $pipelineRoot | Out-Null } catch { $caught = $_.Exception.Data.Contains('ExitCode') -and [int]$_.Exception.Data['ExitCode'] -eq $failure.Code }
        Assert-LocalBuildTrue $caught ("$($failure.Name) failure preserves its code")
        Assert-LocalBuildTrue (-not (Test-Path -LiteralPath $publishedPath -PathType Leaf)) ("$($failure.Name) failure invalidates the stable Setup publication")
        if ($failure.Name -ceq 'tests') {
            $previousCopies = @(Get-ChildItem -LiteralPath (Join-Path $pipelineRoot 'dist\local-build') -Filter 'RoboRev-Dashboard-Setup.exe' -File -Recurse)
            Assert-LocalBuildEqual $previousCopies.Count 1 'the invalidated Setup is retained in this run history'
            Assert-LocalBuildEqual (Get-LocalBuildSha256 $previousCopies[0].FullName) $previousHash 'the invalidated Setup history copy is unchanged'
            $previousRun = Split-Path -Parent $previousCopies[0].FullName
            Assert-LocalBuildTrue (Test-Path -LiteralPath (Join-Path $previousRun 'RoboRev-Dashboard-Setup.exe.sha256') -PathType Leaf) 'the invalidated Setup hash remains in run history'
            Assert-LocalBuildTrue (Test-Path -LiteralPath (Join-Path $previousRun 'roborev-dashboard.local.manifest.json') -PathType Leaf) 'the invalidated local manifest remains in run history'
        }
        Assert-LocalBuildEqual $script:MockInstallerCalls 0 ("$($failure.Name) failure never launches Setup")
    }
    $script:MockFailStage = ''
    $script:MockInstallerCalls = 0
    $success = Invoke-LocalBuild -RepositoryRoot $pipelineRoot -NoInstall
    Assert-LocalBuildTrue (-not [bool]$success.Installed) 'NoInstall success does not install'
    Assert-LocalBuildEqual $script:MockInstallerCalls 0 'NoInstall success never launches Setup'
    Assert-LocalBuildTrue ((Get-LocalBuildSha256 $publishedPath) -cne $previousHash) 'NoInstall success publishes the current verified Setup'
    Assert-LocalBuildTrue (Test-Path -LiteralPath ($publishedPath + '.sha256') -PathType Leaf) 'NoInstall success publishes the Setup hash sidecar'

    $fakeSetup = Join-Path $runA 'fake-setup.exe'
    [IO.File]::WriteAllText($fakeSetup, 'setup', (New-Object System.Text.UTF8Encoding($false)))
    $script:MockInstallerExit = 2
    Set-Item Function:\Invoke-LocalDashboardInstaller -Value $realInstaller
    Set-Item Function:\Invoke-LocalBuildProcess -Value {
        param([string]$FilePath, [string[]]$ArgumentList, [string]$WorkingDirectory, [string]$StandardOutputPath, [string]$StandardErrorPath, [int]$TimeoutSeconds)
        if ($script:MockInstallerResultPath) {
            if ($script:MockInstallerState -eq 'malformed') {
                [IO.File]::WriteAllText($script:MockInstallerResultPath, 'not-json', (New-Object System.Text.UTF8Encoding($false)))
                return [pscustomobject]@{ ExitCode = $script:MockInstallerExit; StandardOutput = ''; StandardError = '' }
            }
            $message = switch ($script:MockInstallerState) {
                'ready' { 'setup-ready' }
                'installing' { 'copy-started' }
                'failed' { 'mutex' }
                default { '' }
            }
            $state = [ordered]@{ state = $script:MockInstallerState; message = $message }
            [IO.File]::WriteAllText($script:MockInstallerResultPath, (($state | ConvertTo-Json -Compress) + "`n"), (New-Object System.Text.UTF8Encoding($false)))
        }
        [pscustomobject]@{ ExitCode = $script:MockInstallerExit; StandardOutput = ''; StandardError = '' }
    }
    $script:MockInstallerState = 'ready'
    $preInstallResult = Join-Path $runA 'logs\cancel-result.json'
    $script:MockInstallerResultPath = $preInstallResult
    $preInstallCancel = Invoke-LocalDashboardInstaller -SetupPath $fakeSetup -LogPath (Join-Path $runA 'logs\cancel.log') -ResultPath $preInstallResult
    Assert-LocalBuildTrue ($preInstallCancel.Canceled -and -not $preInstallCancel.Partial -and $preInstallCancel.ExitCode -eq 2) 'Inno pre-install cancel is classified as untouched cancellation'
    $script:MockInstallerExit = 5
    $script:MockInstallerState = 'installing'
    $duringInstallResult = Join-Path $runA 'logs\partial-result.json'
    $script:MockInstallerResultPath = $duringInstallResult
    $duringInstallCancel = Invoke-LocalDashboardInstaller -SetupPath $fakeSetup -LogPath (Join-Path $runA 'logs\partial.log') -ResultPath $duringInstallResult
    Assert-LocalBuildTrue ($duringInstallCancel.Canceled -and $duringInstallCancel.Partial -and $duringInstallCancel.ExitCode -eq 5) 'Inno during-install cancel is classified as potentially partial'
    $script:MockInstallerExit = 2
    $script:MockInstallerState = 'failed'
    $failedResult = Join-Path $runA 'logs\failed-result.json'
    $script:MockInstallerResultPath = $failedResult
    $failedInstaller = $false
    try { Invoke-LocalDashboardInstaller -SetupPath $fakeSetup -LogPath (Join-Path $runA 'logs\failed.log') -ResultPath $failedResult | Out-Null } catch { $failedInstaller = $_.Exception.Data.Contains('ExitCode') -and [int]$_.Exception.Data['ExitCode'] -eq 1 -and [int]$_.Exception.Data['InstallerExitCode'] -eq 2 }
    Assert-LocalBuildTrue $failedInstaller 'Inno exit 2 with failed result state is not mislabeled as user cancellation'
    $script:MockInstallerState = 'ready'
    $script:MockInstallerResultPath = $null
    $missingResult = $false
    try { Invoke-LocalDashboardInstaller -SetupPath $fakeSetup -LogPath (Join-Path $runA 'logs\missing.log') -ResultPath (Join-Path $runA 'logs\missing-result.json') | Out-Null } catch { $missingResult = $_.Exception.Data.Contains('ExitCode') -and [int]$_.Exception.Data['ExitCode'] -eq 1 }
    Assert-LocalBuildTrue $missingResult 'missing installer result state is an unknown failure'
    $script:MockInstallerState = 'malformed'
    $script:MockInstallerResultPath = Join-Path $runA 'logs\malformed-result.json'
    $malformedResult = $false
    try { Invoke-LocalDashboardInstaller -SetupPath $fakeSetup -LogPath (Join-Path $runA 'logs\malformed.log') -ResultPath $script:MockInstallerResultPath | Out-Null } catch { $malformedResult = $_.Exception.Data.Contains('ExitCode') -and [int]$_.Exception.Data['ExitCode'] -eq 1 }
    Assert-LocalBuildTrue $malformedResult 'malformed installer result state is an unknown failure'

    $marker = Write-LocalBuildFailureMarker -RepositoryRoot $repoRoot -RunDirectory $runA -Stage 'tests' -Message 'injected deterministic failure' -ExitCode 19
    Assert-LocalBuildTrue ($marker -match 'Exit code: 19' -and (Test-Path -LiteralPath (Join-Path $repoRoot 'dist\local-build\last-failure.txt'))) 'failure marker records stage and code'
} finally {
    if (Test-Path -LiteralPath $tempRoot) {
        $resolved = (Resolve-Path -LiteralPath $tempRoot -ErrorAction Stop).Path
        if (-not $resolved.StartsWith(([IO.Path]::GetTempPath()).TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing to remove an unexpected local build test path.' }
        Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction Stop
    }
}

Write-Host 'Local build helper tests passed.'
