# Execute only in the disposable Windows Sandbox created by
# prepare-installer-sandbox.ps1. No host profile or repository is mapped here.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
if ($env:USERNAME -cne 'WDAGUtilityAccount' -or
    -not $env:USERPROFILE.EndsWith('\WDAGUtilityAccount', [StringComparison]::OrdinalIgnoreCase) -or
    $PSScriptRoot -cne 'C:\DashboardInstallerInput') {
    throw 'Installer E2E requires the generated disposable Windows Sandbox. Refusing to install on this host.'
}
$inputRoot = 'C:\DashboardInstallerInput'
$resultRoot = 'C:\DashboardInstallerResults'
if (-not (Test-Path -LiteralPath $resultRoot -PathType Container)) { throw 'Sandbox results mapping is absent.' }
$installRoot = Join-Path $env:LOCALAPPDATA 'Programs\RoboRev Dashboard'
if (Test-Path -LiteralPath $installRoot) { throw 'Expected a fresh disposable Sandbox; existing app was preserved.' }
$expected = Get-Content -LiteralPath (Join-Path $inputRoot 'expected.json') -Raw | ConvertFrom-Json
$setup = Join-Path $inputRoot 'Setup.exe'
$exe = Join-Path $installRoot 'roborev-dashboard.exe'
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{D7B6E4A4-76E5-4F33-A9CE-CF1B8E8DF0AF}_is1'
if (Test-Path -LiteralPath $uninstallKey) { throw 'Fresh Sandbox has an existing Dashboard registration; it was preserved.' }
$script:step = 'preflight'
$completed = New-Object 'System.Collections.Generic.List[string]'
$collision = $null
$primary = $null

function Assert-SandboxHash([string]$Path, [string]$Hash) {
    if ((Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash -ine $Hash) { throw "Artifact hash mismatch: $Path" }
}
function Invoke-SandboxProcess([string]$File, [string[]]$Arguments) {
    # All arguments are fixed test commands or generated C:\DashboardInstallerResults paths.
    $process = Start-Process -FilePath $File -ArgumentList $Arguments -WindowStyle Hidden -PassThru
    try {
        if (-not $process.WaitForExit(120000)) { throw "Sandbox step $script:step timed out. Close this disposable Sandbox before retrying." }
        if ($process.ExitCode -ne 0) { throw "Sandbox step $script:step failed with exit code $($process.ExitCode)." }
    } finally { $process.Dispose() }
}
function Assert-SandboxSentinels {
    Assert-SandboxHash $script:configPath $script:configHash
    Assert-SandboxHash $script:roborevSentinel $script:roborevHash
}
function Read-SandboxInstance {
    $status = & $exe --installed-status
    if ($LASTEXITCODE -ne 0) { throw 'Installed status command failed.' }
    return ($status | ConvertFrom-Json)
}
function Start-SandboxInstance {
    $process = Start-Process -FilePath $exe -ArgumentList @('--installed', '--no-open') -WindowStyle Hidden -PassThru
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($process.HasExited) { throw 'Installed app exited before becoming ready.' }
        $state = Read-SandboxInstance
        if ($state.running) { return $process }
        Start-Sleep -Milliseconds 100
    }
    throw 'Installed app did not become ready in 30 seconds.'
}

Start-Transcript -LiteralPath (Join-Path $resultRoot 'sandbox.log') | Out-Null
try {
    Assert-SandboxHash $setup $expected.setupSha256
    Assert-SandboxHash (Join-Path $inputRoot 'expected.exe') $expected.executableSha256
    # The ephemeral profile has no real Git, RoboRev, agent or development tools.
    $env:Path = Join-Path $env:SystemRoot 'System32'
    $script:configPath = Join-Path $env:APPDATA 'roborev-dashboard\config.json'
    $script:roborevSentinel = Join-Path $env:USERPROFILE '.roborev\sandbox-sentinel.txt'
    New-Item -ItemType Directory -Path (Split-Path -Parent $script:configPath), (Split-Path -Parent $script:roborevSentinel) -Force | Out-Null
    [IO.File]::WriteAllText($script:configPath, '{ "recentRepositories": [], "lastSelectedRepository": "" }')
    [IO.File]::WriteAllText($script:roborevSentinel, 'preserve this disposable RoboRev data sentinel')
    $script:configHash = (Get-FileHash -LiteralPath $script:configPath).Hash
    $script:roborevHash = (Get-FileHash -LiteralPath $script:roborevSentinel).Hash

    $script:step = 'first-install'
    Invoke-SandboxProcess $setup @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/DASHBOARDNOLAUNCH', '/LOG=C:\DashboardInstallerResults\first-install.log')
    Assert-SandboxHash $exe $expected.executableSha256
    if (-not (Test-Path -LiteralPath $uninstallKey)) { throw 'Current-user Installed Apps registration is missing.' }
    Assert-SandboxSentinels
    $completed.Add($script:step)

    $script:step = 'toolchain-free-version'
    $version = & $exe --version-json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $version.version -cne 'dev' -or $version.release -ne $false) { throw 'Local development provenance mismatch.' }
    Invoke-SandboxProcess $exe @('--version')
    Invoke-SandboxProcess $exe @('--help')
    $completed.Add($script:step)

    $script:step = 'shortcut-contract'
    $shell = New-Object -ComObject WScript.Shell
    $programs = [Environment]::GetFolderPath('Programs')
    $shortcuts = @(Get-ChildItem -LiteralPath $programs -Filter '*.lnk' -Recurse | ForEach-Object { $shell.CreateShortcut($_.FullName) } | Where-Object { $_.TargetPath -ieq $exe })
    if (@($shortcuts | Where-Object { $_.Arguments -ceq '--installed' }).Count -ne 1 -or
        @($shortcuts | Where-Object { $_.Arguments -ceq '--stop-installed' }).Count -ne 1) { throw 'Start/stop shortcut target or arguments mismatch.' }
    $completed.Add($script:step)

    $script:step = 'port-collision-and-duplicate-launch'
    $collision = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 7410)
    $collision.Start()
    $primary = Start-SandboxInstance
    $state = Read-SandboxInstance
    if ($state.pid -ne $primary.Id -or $state.url -eq 'http://127.0.0.1:7410') { throw 'Unrelated localhost server adopted or wrong process identity.' }
    Invoke-SandboxProcess $exe @('--installed', '--no-open')
    $secondState = Read-SandboxInstance
    if ($secondState.pid -ne $state.pid) { throw 'Repeated launch started another server.' }
    Assert-SandboxSentinels
    $completed.Add($script:step)

    $script:step = 'same-version-reinstall-while-running'
    Invoke-SandboxProcess $setup @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/DASHBOARDNOLAUNCH', '/LOG=C:\DashboardInstallerResults\reinstall.log')
    if (-not $primary.WaitForExit(10000)) { throw 'Previous installed instance survived replacement.' }
    $primary.Dispose(); $primary = $null
    Assert-SandboxHash $exe $expected.executableSha256
    if (-not (Test-Path -LiteralPath $uninstallKey)) { throw 'Reinstall lost the fixed Installed Apps registration.' }
    Assert-SandboxSentinels
    $completed.Add($script:step)

    $script:step = 'uninstall-while-running'
    $primary = Start-SandboxInstance
    $uninstallers = @(Get-ChildItem -LiteralPath $installRoot -Filter 'unins*.exe')
    if ($uninstallers.Count -ne 1) { throw 'Expected exactly one registered uninstaller.' }
    Invoke-SandboxProcess $uninstallers[0].FullName @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/LOG=C:\DashboardInstallerResults\uninstall.log')
    if (-not $primary.WaitForExit(10000)) { throw 'Installed instance survived uninstall.' }
    $primary.Dispose(); $primary = $null
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    while ((Test-Path -LiteralPath $exe) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 100 }
    if (Test-Path -LiteralPath $exe) { throw 'Installed executable was not removed.' }
    if (Test-Path -LiteralPath $uninstallKey) { throw 'Installed Apps registration was not removed.' }
    foreach ($shortcut in @(Get-ChildItem -LiteralPath $programs -Filter '*.lnk' -Recurse)) {
        if ($shell.CreateShortcut($shortcut.FullName).TargetPath -ieq $exe) { throw 'Dashboard shortcut was not removed.' }
    }
    Assert-SandboxSentinels
    $completed.Add($script:step)
    $result = [ordered]@{ passed = $completed.ToArray(); failed = $null; unverified = @('interactive approval/cancellation', 'browser opening through Start Menu', 'upgrade from older version', 'downgrade rejection', 'SmartScreen experience') }
    $result | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $resultRoot 'result.json') -Encoding UTF8
    Write-Host 'Sandbox automated cases passed. Consult result.json for the manual cases still required.'
} catch {
    [ordered]@{ passed = $completed.ToArray(); failed = $script:step; error = $_.Exception.Message } | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $resultRoot 'result.json') -Encoding UTF8
    throw
} finally {
    if ($null -ne $collision) { $collision.Stop() }
    if ($null -ne $primary) {
        if (Test-Path -LiteralPath $exe) { & $exe --stop-installed }
        $primary.Dispose()
    }
    Stop-Transcript | Out-Null
}
