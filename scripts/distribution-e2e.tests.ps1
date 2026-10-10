#Requires -Version 7.0
# Focused harness safety checks; never builds or invokes Setup/RoboRev.
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
. (Join-Path $PSScriptRoot 'release-common.ps1')
. (Join-Path $PSScriptRoot 'distribution-common.ps1')
$harness = Join-Path $PSScriptRoot 'distribution-e2e.ps1'
$tokens = $null
$errors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($harness, [ref]$tokens, [ref]$errors)
if ($errors.Count -ne 0) { throw 'Distribution E2E harness must parse cleanly.' }
$officialFilesHelper = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq 'Assert-InstalledOfficialFiles' }, $true)
if ($officialFilesHelper.Count -ne 1) { throw 'Expected exactly one pure installed three-file contract helper.' }
. ([scriptblock]::Create($officialFilesHelper[0].Extent.Text))
$sourceCommitHelper = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq 'Assert-IsolatedInstallerSourceCommit' }, $true)
if ($sourceCommitHelper.Count -ne 1) { throw 'Expected exactly one pure installer source commit guard.' }
. ([scriptblock]::Create($sourceCommitHelper[0].Extent.Text))
Assert-IsolatedInstallerSourceCommit ('a' * 40) ('a' * 40)
foreach ($sourceHead in @(('b' * 40), 'unknown', ('A' * 40))) {
    $sourceRejected = $false
    try { Assert-IsolatedInstallerSourceCommit $sourceHead ('a' * 40) } catch { $sourceRejected = $true }
    if (-not $sourceRejected) { throw 'Mismatched/invalid isolated installer source commit was accepted.' }
}
$source = $ast.FindAll({ param($node) $node -is [Management.Automation.Language.StringConstantExpressionAst] -and $node.Value.Contains('public sealed class DistributionTestProcess') }, $true)
if ($source.Count -ne 1) { throw 'Expected exactly one native process helper.' }
if (-not ('DistributionTestProcess' -as [type])) { Add-Type -TypeDefinition $source[0].Value }
$guardPassed = $false
try { & $harness -DistributionDirectory (Join-Path $root '.tmp\nonexistent-distribution-guard') -Version '0.1.0' }
catch { $guardPassed = $_.Exception.Message.StartsWith('Explicit -AllowCurrentUserInstallerTest is required:') }
if (-not $guardPassed) { throw 'Missing opt-in must refuse before any artifact/registry/workspace access.' }
if (-not $IsWindows) { Write-Host 'PASS: parser/opt-in; BLOCKED: Windows native helper cases.'; return }
$isolatedGuardPassed = $false
try { & $harness -DistributionDirectory (Join-Path $root '.tmp\nonexistent-distribution-guard') -Version '0.1.0' -AllowCurrentUserInstallerTest -UseIsolatedInstallerIdentity }
catch { $isolatedGuardPassed = $_.Exception.Message.StartsWith('Isolated identity mode requires a verified Setup extractor') }
if (-not $isolatedGuardPassed) { throw 'Isolated identity must require explicit verified original Setup readback.' }
$extractorGuardPassed = $false
try { & $harness -DistributionDirectory (Join-Path $root '.tmp\nonexistent-distribution-guard') -Version '0.1.0' -AllowCurrentUserInstallerTest -SetupExtractorSha256 ('a' * 64) }
catch { $extractorGuardPassed = $_.Exception.Message.StartsWith('SetupExtractorPath and SetupExtractorSha256 must be supplied together.') }
if (-not $extractorGuardPassed) { throw 'Extractor path and SHA-256 must be a paired verified input.' }
$workspace = Join-Path $root '.tmp\distribution-e2e-selftest'
if (Test-Path -LiteralPath $workspace) { throw 'Existing distribution E2E self-test workspace was preserved.' }
[void](Assert-SafeExistingParent -Path $workspace)
$children = [Collections.Generic.List[object]]::new()
New-Item -ItemType Directory -Path $workspace | Out-Null
try {
    $officialSource = Join-Path $workspace 'official-source'
    $installedFixture = Join-Path $workspace 'installed-fixture'
    New-Item -ItemType Directory -Path $officialSource, $installedFixture | Out-Null
    $fixtureExe = Join-Path $officialSource 'roborev-dashboard.exe'
    [IO.File]::WriteAllText($fixtureExe, 'Non-executable test bytes for pure installed payload verification.')
    $fixtureSha = (Get-FileHash -LiteralPath $fixtureExe -Algorithm SHA256).Hash.ToLowerInvariant()
    $fixtureCommit = 'a' * 40
    $fixtureManifest = New-ReleaseManifest -Version '1.2.3' -Commit $fixtureCommit -Size ((Get-Item -LiteralPath $fixtureExe).Length) -Sha256 $fixtureSha
    [IO.File]::WriteAllText((Join-Path $officialSource 'roborev-dashboard.manifest.json'), (ConvertTo-DeterministicJsonText -Value $fixtureManifest))
    [IO.File]::WriteAllText((Join-Path $officialSource 'roborev-dashboard.sha256'), "$fixtureSha  roborev-dashboard.exe`n")
    foreach ($name in @('roborev-dashboard.exe','roborev-dashboard.manifest.json','roborev-dashboard.sha256')) { Copy-Item -LiteralPath (Join-Path $officialSource $name) -Destination (Join-Path $installedFixture $name) }
    Assert-InstalledOfficialFiles $installedFixture $officialSource '1.2.3' $fixtureCommit
    foreach ($name in @('roborev-dashboard.manifest.json','roborev-dashboard.sha256')) {
        $target = Join-Path $installedFixture $name
        foreach ($condition in @('missing','corrupt')) {
            if ($condition -eq 'missing') { Remove-Item -LiteralPath $target } else { [IO.File]::WriteAllText($target, 'corrupt metadata must fail before payload execution') }
            $rejected = $false
            try { Assert-InstalledOfficialFiles $installedFixture $officialSource '1.2.3' $fixtureCommit } catch { $rejected = $true }
            if (-not $rejected) { throw "Installed $condition $name was incorrectly accepted." }
            Copy-Item -LiteralPath (Join-Path $officialSource $name) -Destination $target -Force
        }
    }
    $fixture = Join-Path $workspace 'arguments 한글 test.ps1'
    [IO.File]::WriteAllText($fixture, @'
param([string]$Value, [string]$Mode)
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
if ($Mode -eq 'large') { [Console]::Write('x' * 70000); exit 0 }
if ($Mode -in @('break','send')) {
    Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class OwnedBreakFixture {
    delegate bool Handler(uint code);
    [DllImport("kernel32.dll")] static extern bool SetConsoleCtrlHandler(Handler callback, bool add);
    [DllImport("kernel32.dll", SetLastError=true)] static extern bool AttachConsole(uint pid);
    [DllImport("kernel32.dll")] static extern bool FreeConsole();
    [DllImport("kernel32.dll")] static extern bool GenerateConsoleCtrlEvent(uint kind, uint group);
    [DllImport("kernel32.dll")] static extern IntPtr GetConsoleWindow();
    static readonly Handler callback = code => { if(code == 1) Environment.Exit(0); return true; };
    public static bool Install() { return SetConsoleCtrlHandler(callback, true); }
    public static bool Send(uint pid) {
        FreeConsole();
        if(!AttachConsole(pid)) { Console.Error.WriteLine("AttachConsole error="+Marshal.GetLastWin32Error()+", console="+GetConsoleWindow()+", target="+pid); return false; }
        try { SetConsoleCtrlHandler(callback,true); bool sent=GenerateConsoleCtrlEvent(1,0); System.Threading.Thread.Sleep(200); return sent; }
        finally { FreeConsole(); }
    }
}
"@
    if ($Mode -eq 'send') { if ([OwnedBreakFixture]::Send([uint32]$Value)) { exit 0 }; exit 9 }
    if (-not [OwnedBreakFixture]::Install()) { exit 9 }
    [Console]::WriteLine('break-ready')
    Start-Sleep -Seconds 30
    exit 8
}
[Console]::WriteLine(($Value | ConvertTo-Json -Compress))
if ($Mode -eq 'live') { Start-Sleep -Seconds 4 }
'@)
    $environment = [Collections.Generic.Dictionary[string,string]]::new([StringComparer]::OrdinalIgnoreCase)
    $environment['SystemRoot'] = $env:SystemRoot
    $environment['WINDIR'] = $env:SystemRoot
    $environment['PATH'] = Join-Path $env:SystemRoot 'System32'
    $environment['TEMP'] = $workspace
    $environment['TMP'] = $workspace
    $pwsh = (Get-Process -Id $PID).Path
    foreach ($value in @('한글 경로 with spaces', 'quote"and trailing\', 'literal $() ` backtick ; & |', '')) {
        $child = [DistributionTestProcess]::Start($pwsh, @('-NoProfile','-NonInteractive','-File',$fixture,'-Value',$value), $workspace, $environment)
        $children.Add($child)
        if (-not $child.Process.WaitForExit(10000)) { throw 'Native argument fixture timed out.' }
        $child.FinishCapture()
        if ($child.Process.ExitCode -ne 0 -or $child.Truncated -or ($child.Output | ConvertFrom-Json) -cne $value) { throw "Native argument array did not preserve literal Windows arguments (code=$($child.Process.ExitCode), stdout=$($child.Output), stderr=$($child.Error))." }
    }
    $live = [DistributionTestProcess]::Start($pwsh, @('-NoProfile','-NonInteractive','-File',$fixture,'-Value','ready','-Mode','live'), $workspace, $environment)
    $children.Add($live)
    $deadline = [DateTime]::UtcNow.AddSeconds(3)
    while (-not $live.Output -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 50 }
    if (-not $live.Output -or $live.Process.HasExited) { throw 'Native reader must capture readiness before the child exits.' }
    if (-not $live.Process.WaitForExit(10000)) { throw 'Live output fixture timed out.' }
    $large = [DistributionTestProcess]::Start($pwsh, @('-NoProfile','-NonInteractive','-File',$fixture,'-Mode','large'), $workspace, $environment)
    $children.Add($large)
    if (-not $large.Process.WaitForExit(10000)) { throw 'Bounded output fixture timed out.' }
    $large.FinishCapture()
    if ($large.Output.Length -ne 65536 -or -not $large.Truncated) { throw 'Native reader output must be bounded while draining the pipe.' }
    $breakChild = [DistributionTestProcess]::Start($pwsh, @('-NoProfile','-NonInteractive','-File',$fixture,'-Mode','break'), $workspace, $environment)
    $children.Add($breakChild)
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    while ($breakChild.Output -notmatch 'break-ready' -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 50 }
    if ($breakChild.Output -notmatch 'break-ready') { throw 'Isolated CTRL+BREAK fixture did not start.' }
    $sender = [DistributionTestProcess]::Start($pwsh, @('-NoProfile','-NonInteractive','-File',$fixture,'-Mode','send','-Value',([string]$breakChild.Process.Id)), $workspace, $environment, $false)
    $children.Add($sender)
    if (-not $sender.Process.WaitForExit(10000) -or $sender.Process.ExitCode -ne 0 -or -not $breakChild.Process.WaitForExit(10000) -or $breakChild.Process.ExitCode -ne 0) { $sender.FinishCapture(); throw "Consoleless CTRL+BREAK sender must reach only the owned hidden console group (senderCode=$($sender.Process.ExitCode), targetExited=$($breakChild.Process.HasExited), targetError=$($breakChild.Error), senderError=$($sender.Error))." }
    Write-Host 'PASS: distribution E2E parser, opt-in, installed three-file metadata rejection, literal UTF-8 arguments, live readiness, bounded output and isolated CTRL+BREAK.'
} finally {
    foreach ($child in $children) {
        if (-not $child.Process.HasExited) { $child.Process.Kill(); [void]$child.Process.WaitForExit(5000) }
        $child.Dispose()
    }
    [void](Assert-NoReparseTree -Path $workspace)
    if (-not (Test-PathWithin -Path $workspace -Root (Join-Path $root '.tmp'))) { throw 'Self-test cleanup containment failed.' }
    Remove-Item -LiteralPath $workspace -Recurse -Force
}
