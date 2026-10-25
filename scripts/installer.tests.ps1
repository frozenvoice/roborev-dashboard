$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $scriptRoot '..')).Path
. (Join-Path $scriptRoot 'prepare-inno.ps1')
. (Join-Path $scriptRoot 'package-local.ps1')

function Assert-InstallerTest {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "Installer contract assertion failed: $Message" }
}

function Assert-InstallerThrows {
    param([scriptblock]$Action, [string]$Message)
    $thrown = $false
    try { & $Action } catch { $thrown = $true }
    Assert-InstallerTest $thrown $Message
}

$spec = Get-InnoSetupSpec
Assert-InstallerTest ($spec.Version -ceq '6.7.3') 'Inno version remains pinned'
Assert-InstallerTest ($spec.PackageUrl -notmatch 'latest') 'Inno URL does not use latest'
Assert-InstallerTest ($spec.PackageSha256 -match '^[A-F0-9]{64}$') 'package SHA-256 is pinned'
Assert-InstallerTest ($spec.CompilerSha256 -match '^[A-F0-9]{64}$') 'ISCC SHA-256 is pinned'
Assert-InstallerTest ([int]$spec.CompilerFileCount -eq 119) 'compiler payload stable file count is pinned'
Assert-InstallerTest ($spec.CompilerTreeSha256 -match '^[a-f0-9]{64}$') 'compiler payload tree SHA-256 is pinned'
Assert-InstallerTest ($spec.CompilerTreeSha256 -ceq '90727b1f2a9315c9f779774884f0cee5b187173efa96dd48dabc415be537a4be') 'compiler payload tree digest matches ordinal reference evidence'

Assert-InstallerTest ((ConvertTo-LocalInstallerVersion -BaseVersion '0.1.0') -ceq '0.1.0-local') 'local version label'
$releaseManifest = New-ReleaseManifest -Version '0.1.0' -Commit ('a' * 40) -Size 4 -Sha256 ('0' * 64)
Assert-InstallerTest (($releaseManifest.version -ceq '0.1.0') -and @($releaseManifest.PSObject.Properties).Count -eq 8) 'release installer uses unchanged official eight-field manifest'
$developmentManifest = New-LocalManifestText -Version '0.1.0-local' -Commit 'unknown' -Size 4 -Sha256 ('0' * 64) | ConvertFrom-Json
Assert-InstallerTest (($developmentManifest.version -ceq '0.1.0-local') -and ($developmentManifest.local -eq $true)) 'local installer manifest is still local'
Assert-InstallerTest ((ConvertTo-LocalInstallerVersion -BaseVersion '1.2.3-beta.1+build.2') -ceq '1.2.3-beta.1-local+build.2') 'local label precedes build metadata'
Assert-InstallerTest ((ConvertFrom-LocalInstallerVersion -LocalVersion '1.2.3-beta.1-local+build.2') -ceq '1.2.3-beta.1+build.2') 'local label can recover the basis version'
Assert-InstallerTest ((ConvertTo-LocalVersionInfoVersion -BaseVersion '0.1.0') -ceq '0.1.0.0') 'numeric file version'
Assert-InstallerTest ((ConvertTo-LocalVersionInfoVersion -BaseVersion '1.2.3-beta.1+build.2') -ceq '1.2.3.0') 'numeric file version strips prerelease and metadata'
Assert-InstallerTest (-not (Test-LocalSemVer -Value 'v0.1.0')) 'v-prefixed version rejected'
Assert-InstallerTest (-not (Test-LocalSemVer -Value '1.2.3-01')) 'numeric prerelease with a leading zero rejected'
foreach ($invalidVersion in @('1.2', '1.2.3.', '1.2.3-', '1.2.3+', '1.2.3+build.')) {
    Assert-InstallerTest (-not (Test-LocalSemVer -Value $invalidVersion)) "malformed version rejected: $invalidVersion"
}
Assert-InstallerThrows { ConvertTo-LocalInstallerVersion -BaseVersion '0.1' } 'invalid base version rejected'

$issPath = Join-Path $repoRoot 'installer\RoboRevDashboard.iss'
$iss = [IO.File]::ReadAllText($issPath, [Text.Encoding]::UTF8)
foreach ($required in @(
        'AppId={{{#AppId}}',
        'DefaultDirName={localappdata}\Programs\RoboRev Dashboard',
        '#define ProgramGroupName "RoboRev Dashboard"',
        'DefaultGroupName={#ProgramGroupName}',
        'PrivilegesRequired=lowest',
        'ArchitecturesAllowed=x64os',
        'DisableDirPage=yes',
        'Name: "{group}\RoboRev Dashboard"',
        'Parameters: "--installed"',
        'Parameters: "--stop-installed"',
        'GetSHA256OfFile',
        'ExpectedExeSha256',
        'BuildVersion',
        'VersionEvidenceValid',
        'DestinationFilesAreSafe',
        'Global\RoboRevDashboard.Installing',
        'CreateProcessW',
        'GetExitCodeProcess',
        'MAX_STOP_WAIT_MS',
        '/DASHBOARDRESULT=',
        'WriteDashboardResult',
        'SaveStringToFile',
        "'ready'",
        "'installing'",
        "'succeeded'",
        "'failed'",
        '/DASHBOARDNOLAUNCH',
        'function PrepareToInstall',
        'procedure CurUninstallStepChanged',
        'Abort;')) {
    Assert-InstallerTest ($iss.Contains($required)) "installer script contains '$required'"
}
Assert-InstallerTest (-not $iss.Contains('taskkill')) 'installer has no taskkill fallback'
Assert-InstallerTest (-not $iss.Contains('CloseApplications=yes')) 'installer does not delegate shutdown to broad CloseApplications'
Assert-InstallerTest (-not $iss.Contains('ewWaitUntilTerminated')) 'installer stop supervision is bounded'
Assert-InstallerTest (-not $iss.Contains('roborev.exe')) 'installer does not ship or install RoboRev'
$silentHelperStart = $iss.IndexOf('function CurrentOperationSilent', [StringComparison]::Ordinal)
$gracefulStopStart = $iss.IndexOf('function TryGracefulStop', [StringComparison]::Ordinal)
$gracefulStopEnd = $iss.IndexOf('function DestinationFilesAreSafe', [StringComparison]::Ordinal)
Assert-InstallerTest (($silentHelperStart -ge 0) -and ($gracefulStopStart -gt $silentHelperStart) -and ($gracefulStopEnd -gt $gracefulStopStart)) 'mode-specific silence helper precedes graceful stop'
$silentHelper = $iss.Substring($silentHelperStart, $gracefulStopStart - $silentHelperStart)
Assert-InstallerTest ($silentHelper -match '(?s)if IsUninstaller then\s+Result := UninstallSilent\s+else\s+Result := WizardSilent;') 'uninstaller uses UninstallSilent and Setup uses WizardSilent in separate branches'
$gracefulStop = $iss.Substring($gracefulStopStart, $gracefulStopEnd - $gracefulStopStart)
Assert-InstallerTest (-not ($gracefulStop -match '\b(?:WizardSilent|UninstallSilent)\b')) 'shared stop path never calls a mode-restricted silence function directly'
$removeBeforeText = -join @([char]0xC81C, [char]0xAC70, ' ', [char]0xC804, [char]0xC5D0)
$installBeforeText = -join @([char]0xC124, [char]0xCE58, ' ', [char]0xC804, [char]0xC5D0)
$confirmationPattern = '(?s)if not CurrentOperationSilent then begin\s+if IsUninstaller then\s+ConfirmationText := [^;]*' + [regex]::Escape($removeBeforeText) + '[^;]*\s+else\s+ConfirmationText := [^;]*' + [regex]::Escape($installBeforeText) + '[^;]*;\s+if MsgBox\(ConfirmationText, mbConfirmation, MB_YESNO\) <> IDYES then Exit;'
Assert-InstallerTest ($gracefulStop -match $confirmationPattern) 'interactive stop confirmation is operation-specific and silent operations skip it'
$stopPrompt = $gracefulStop.IndexOf('if not CurrentOperationSilent', [StringComparison]::Ordinal)
$stopProcess = $gracefulStop.IndexOf('if not CreateProcessW', [StringComparison]::Ordinal)
Assert-InstallerTest (($stopPrompt -ge 0) -and ($stopProcess -gt $stopPrompt)) 'operation confirmation remains before the exact executable stop request'
$versionGate = $iss.IndexOf('if not Uninstalling then begin', [StringComparison]::Ordinal)
$stopGate = $iss.IndexOf('if ExistingExecutableIsLocked(ExePath) then begin', [StringComparison]::Ordinal)
Assert-InstallerTest (($versionGate -ge 0) -and ($stopGate -gt $versionGate)) 'version evidence is checked before graceful stop'
$curStepStart = $iss.IndexOf('procedure CurStepChanged', [StringComparison]::Ordinal)
$customExitStart = $iss.IndexOf('function GetCustomSetupExitCode', [StringComparison]::Ordinal)
Assert-InstallerTest (($curStepStart -ge 0) -and ($customExitStart -gt $curStepStart)) 'post-install code block is present'
$curStepBlock = $iss.Substring($curStepStart, $customExitStart - $curStepStart)
Assert-InstallerTest (-not $curStepBlock.Contains('ReleaseInstallingMutex')) 'install mutex is held through setup teardown'
$readyState = $iss.IndexOf("WriteDashboardResult('ready'", [StringComparison]::Ordinal)
$installingState = $iss.IndexOf("WriteDashboardResult('installing'", [StringComparison]::Ordinal)
$succeededState = $iss.IndexOf("WriteDashboardResult('succeeded'", [StringComparison]::Ordinal)
Assert-InstallerTest (($readyState -ge 0) -and ($installingState -gt $readyState) -and ($succeededState -gt $installingState)) 'result states follow setup lifecycle order'
Assert-InstallerTest (-not $iss.Contains('WizardStyle=modern' + "`r`n")) 'product script does not set a second wizard style'
$themePath = Join-Path $repoRoot 'installer\SetupTheme.iss'
$theme = [IO.File]::ReadAllText($themePath, [Text.Encoding]::UTF8)
foreach ($requiredTheme in @(
        'WizardStyle=modern dark polar hidebevels',
        '<event(''CurInstallProgressChanged'')>',
        'ThemeFill.Width := Width',
        'ThemeMode := SetupThemeMode();',
        'Failure := SetupThemeFailure();')) {
    Assert-InstallerTest ($theme.Contains($requiredTheme)) "theme script contains '$requiredTheme'"
}
Assert-InstallerTest (-not $theme.Contains('ReleaseInstallingMutex')) 'theme does not own the install mutex'
Assert-InstallerTest (-not $theme.Contains('WriteDashboardResult')) 'theme does not write install result state'
Assert-InstallerTest (-not $theme.Contains('CreateProcessW')) 'theme does not start processes'
Assert-InstallerTest (-not $theme.Contains('taskkill')) 'theme has no taskkill'
Assert-InstallerTest (-not $theme.Contains('DASHBOARDRESULT')) 'theme does not touch the result channel'
Assert-InstallerTest (-not $theme.Contains('DASHBOARDNOLAUNCH')) 'theme does not interpret the no-launch switch'
$includeAt = $iss.LastIndexOf('#include "SetupTheme.iss"', [StringComparison]::Ordinal)
$modeAt = $iss.IndexOf('function SetupThemeMode', [StringComparison]::Ordinal)
$failureAt = $iss.IndexOf('function SetupThemeFailure', [StringComparison]::Ordinal)
$correctAt = $iss.IndexOf('procedure DashboardCorrectFinishedPage', [StringComparison]::Ordinal)
Assert-InstallerTest (($modeAt -gt $customExitStart) -and ($failureAt -gt $modeAt) -and ($includeAt -gt $failureAt) -and ($correctAt -gt $includeAt)) 'theme is included after the install gates and the finished-page correction follows it'
$correctEnd = $iss.IndexOf('ThemeRun.Visible := False;', [StringComparison]::Ordinal)
Assert-InstallerTest ($correctEnd -gt $correctAt) 'finished-page correction hides the launch checkbox'
$correctBody = $iss.Substring($correctAt, $correctEnd - $correctAt)
Assert-InstallerTest (-not $correctBody.Contains('LaunchAllowed')) 'finished-page correction does not change the launch gate'
Assert-InstallerTest (-not $correctBody.Contains('WriteDashboardResult')) 'finished-page correction does not write result state'
Assert-InstallerTest ($iss.Contains('PrepareFailure := ErrorText')) 'prepare failure text is retained for the themed failure page'
foreach ($artName in @(
        'setup-brand.png', 'setup-logo.png', 'setup-done.png', 'setup-failed.png',
        'setup-panel-location.png', 'setup-panel-detail.png', 'setup-track.png',
        'setup-minimize.png', 'setup-close.png', 'setup-backdrop-1x.png', 'setup-backdrop-2x.png')) {
    Assert-InstallerTest (Test-Path -LiteralPath (Join-Path $repoRoot "assets\installer\$artName") -PathType Leaf) "installer art exists: $artName"
}
$setupUiPath = Join-Path $repoRoot 'scripts\test-setup-ui.ps1'
Assert-InstallerTest (Test-Path -LiteralPath $setupUiPath -PathType Leaf) 'isolated setup UI test exists'
$setupUi = [IO.File]::ReadAllText($setupUiPath, [Text.Encoding]::UTF8)
$lockWait = $setupUi.IndexOf('$fixtureMutex.WaitOne(0)', [StringComparison]::Ordinal)
$ownsRefusal = $setupUi.IndexOf('Another setup UI run owns', [StringComparison]::Ordinal)
$firstStop = $setupUi.IndexOf('Stop-FixtureProcesses $fixtureRoot', [StringComparison]::Ordinal)
$firstRemove = $setupUi.IndexOf('Remove-Item -LiteralPath $fixtureRoot -Recurse -Force', [StringComparison]::Ordinal)
Assert-InstallerTest (($lockWait -gt 0) -and ($ownsRefusal -gt $lockWait) -and ($firstStop -gt $ownsRefusal) -and ($firstRemove -gt $firstStop)) 'fixture directory is locked before it is inspected or cleaned'
$ownerGate = $setupUi.IndexOf('if ($fixtureLocked) {', $firstRemove, [StringComparison]::Ordinal)
Assert-InstallerTest ($ownerGate -gt $firstRemove) 'fixture cleanup is gated on the lock owner'
$finalStop = $setupUi.IndexOf('Stop-FixtureProcesses $fixtureRoot', $ownerGate, [StringComparison]::Ordinal)
$finalRemove = if ($finalStop -ge 0) { $setupUi.IndexOf('Remove-Item -LiteralPath $fixtureRoot', $finalStop, [StringComparison]::Ordinal) } else { -1 }
$releaseAt = if ($finalRemove -ge 0) { $setupUi.IndexOf('$fixtureMutex.ReleaseMutex()', $finalRemove, [StringComparison]::Ordinal) } else { -1 }
Assert-InstallerTest (($finalStop -gt $ownerGate) -and ($finalRemove -gt $finalStop) -and ($releaseAt -gt $finalRemove)) 'fixture cleanup in finally runs only for the lock owner and releases after cleanup'
& $setupUiPath -FixtureLockSelfTest

$treeTemp = Join-Path ([IO.Path]::GetTempPath()) ('roborev-dashboard-installer-tree-tests-' + [guid]::NewGuid().ToString('N'))
try {
    New-Item -ItemType Directory -Path $treeTemp -Force | Out-Null
    [IO.File]::WriteAllText((Join-Path $treeTemp 'z.txt'), 'z', [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $treeTemp 'A.txt'), 'A', [Text.UTF8Encoding]::new($false))
    $treeLines = @(Get-InnoCompilerTreeLines -CompilerDirectory $treeTemp)
    $treeNames = @($treeLines | ForEach-Object { ($_ -split "`t", 2)[0] })
    Assert-InstallerTest (($treeNames.Count -eq 2) -and ($treeNames[0] -ceq 'A.txt') -and ($treeNames[1] -ceq 'z.txt')) 'compiler tree lines use ordinal path order'
} finally {
    if (Test-Path -LiteralPath $treeTemp) {
        $resolvedTree = (Resolve-Path -LiteralPath $treeTemp).Path
        $treePrefix = ([IO.Path]::GetTempPath()).TrimEnd('\') + '\roborev-dashboard-installer-tree-tests-'
        if (-not $resolvedTree.StartsWith($treePrefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing to clean unexpected tree test path.' }
        Remove-Item -LiteralPath $resolvedTree -Recurse -Force -ErrorAction Stop
    }
}

$temp = Join-Path ([IO.Path]::GetTempPath()) ('roborev-dashboard-installer-tests-' + [guid]::NewGuid().ToString('N'))
try {
    New-Item -ItemType Directory -Path $temp -Force | Out-Null
    $payload = Join-Path $temp 'payload'
    New-Item -ItemType Directory -Path $payload -Force | Out-Null
    [IO.File]::WriteAllBytes((Join-Path $payload 'roborev-dashboard.exe'), [byte[]](1, 2, 3, 4))
    [IO.File]::WriteAllText((Join-Path $payload 'roborev-dashboard.manifest.json'), (New-LocalManifestText -Version '0.1.0-local' -Commit 'unknown' -Size 4 -Sha256 '0000000000000000000000000000000000000000000000000000000000000000'), [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $payload 'roborev-dashboard.sha256'), "0000000000000000000000000000000000000000000000000000000000000000  roborev-dashboard.exe`n", [Text.UTF8Encoding]::new($false))
    Assert-LocalPayloadAllowlist -Directory $payload
    [IO.File]::WriteAllText((Join-Path $payload 'unexpected.txt'), 'reject', [Text.UTF8Encoding]::new($false))
    Assert-InstallerThrows { Assert-LocalPayloadAllowlist -Directory $payload } 'payload allowlist rejects unexpected files'
} finally {
    if (Test-Path -LiteralPath $temp) {
        $resolved = (Resolve-Path -LiteralPath $temp).Path
        $tempPrefix = ([IO.Path]::GetTempPath()).TrimEnd('\') + '\roborev-dashboard-installer-tests-'
        if (-not $resolved.StartsWith($tempPrefix, [StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing to clean unexpected installer test path.' }
        Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction Stop
    }
}

Write-Host 'Installer contract tests passed.'
