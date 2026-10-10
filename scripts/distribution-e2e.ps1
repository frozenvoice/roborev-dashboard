#Requires -Version 7.0
<#
.SYNOPSIS
    Opt-in, actual Windows Setup/portable lifecycle verification.
.DESCRIPTION
    The default mode uses the official Setup and refuses a production install.
    Explicit -UseIsolatedInstallerIdentity instead recompiles the same ISS with
    the exact official payload and pinned compiler, changing ONLY its AppId.
    This exercises lifecycle without replacing an existing production install.
    Evidence distinguishes the two modes. /NOICONS avoids real-profile shortcuts;
    only the official uninstaller removes the registration created by this test.
    Application children receive an isolated profile and no development tools.
    No RoboRev database, credentials, real daemon or review jobs are accessed.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$DistributionDirectory,
    [Parameter(Mandatory)][string]$Version,
    [switch]$AllowCurrentUserInstallerTest,
    [switch]$UseIsolatedInstallerIdentity,
    [string]$SetupExtractorPath,
    [string]$SetupExtractorSha256,
    [string]$OlderSetupPath,
    [string]$OlderVersion
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
. (Join-Path $PSScriptRoot 'release-common.ps1')
. (Join-Path $PSScriptRoot 'distribution-common.ps1')
if (-not $AllowCurrentUserInstallerTest) { throw 'Explicit -AllowCurrentUserInstallerTest is required: this test runs the actual Setup and temporarily registers its owned installation in HKCU.' }
if (-not $IsWindows -or -not [Environment]::Is64BitOperatingSystem) { throw 'Actual distribution E2E requires Windows x64.' }
[void](Assert-StrictSemVer -Value $Version)
if ([bool]$OlderSetupPath -ne [bool]$OlderVersion) { throw 'OlderSetupPath and OlderVersion must be supplied together.' }
if ($OlderVersion) { [void](Assert-StrictSemVer -Value $OlderVersion) }
if ([bool]$SetupExtractorPath -ne [bool]$SetupExtractorSha256) { throw 'SetupExtractorPath and SetupExtractorSha256 must be supplied together.' }
if ($UseIsolatedInstallerIdentity -and -not $SetupExtractorPath) { throw 'Isolated identity mode requires a verified Setup extractor to read back the exact original official payload.' }
if ($SetupExtractorPath) {
    if ($SetupExtractorSha256 -cnotmatch '^[0-9a-f]{64}$') { throw 'Setup extractor requires a lowercase exact SHA-256.' }
    $SetupExtractorPath = [IO.Path]::GetFullPath($SetupExtractorPath)
    [void](Assert-SafeExistingParent -Path $SetupExtractorPath)
    [void](Assert-NoReparsePoint -Path $SetupExtractorPath)
    if (-not (Test-Path -LiteralPath $SetupExtractorPath -PathType Leaf) -or (Get-FileHash -LiteralPath $SetupExtractorPath -Algorithm SHA256).Hash.ToLowerInvariant() -cne $SetupExtractorSha256) { throw 'Setup extractor hash mismatch or missing executable.' }
}
$candidate = [IO.Path]::GetFullPath($DistributionDirectory)
[void](Assert-NoReparseTree -Path $candidate)
$setup = Join-Path $candidate 'RoboRev-Dashboard-Setup.exe'
$portable = Join-Path $candidate "RoboRev-Dashboard-$Version-win-x64-portable.zip"
$manifestPath = Join-Path $candidate 'roborev-dashboard.manifest.json'
$officialExe = Join-Path $candidate 'roborev-dashboard.exe'
foreach ($file in @($setup, $portable, $manifestPath, $officialExe)) {
    if (-not (Test-Path -LiteralPath $file -PathType Leaf)) { throw 'Required official distribution artifact is absent.' }
    [void](Assert-NoReparsePoint -Path $file)
}
if ($OlderSetupPath) {
    $OlderSetupPath = [IO.Path]::GetFullPath($OlderSetupPath)
    [void](Assert-SafeExistingParent -Path $OlderSetupPath)
    [void](Assert-NoReparsePoint -Path $OlderSetupPath)
    if (-not (Test-Path -LiteralPath $OlderSetupPath -PathType Leaf)) { throw 'Older Setup is absent.' }
    if ($OlderVersion -ceq $Version) { throw 'Upgrade fixture must have a different, older version.' }
}
$productionRegistration = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{D7B6E4A4-76E5-4F33-A9CE-CF1B8E8DF0AF}_is1'
$testAppId = '34A7BD74-BA3B-4D68-AB1C-B76BC5A681CF'
$registration = if ($UseIsolatedInstallerIdentity) { 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\{' + $testAppId + '}_is1' } else { $productionRegistration }
$defaultInstall = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Programs\RoboRev Dashboard'
if (Test-Path -LiteralPath $registration) { throw 'Existing selected installer registration was preserved; resolve its ownership before testing.' }
if (-not $UseIsolatedInstallerIdentity -and (Test-Path -LiteralPath $defaultInstall)) { throw 'Existing production Dashboard installation was preserved; run on a fresh Windows profile or explicitly select isolated identity mode.' }
$workspace = Join-Path $root '.tmp\distribution-e2e'
if (Test-Path -LiteralPath $workspace) { throw 'Existing .tmp/distribution-e2e was preserved; resolve its ownership before retrying.' }
[void](Assert-SafeExistingParent -Path $workspace)
$evidence = Join-Path $root 'dist\distribution-e2e.json'
[void](Assert-SafeExistingParent -Path $evidence)
if (Test-Path -LiteralPath $evidence) {
    [void](Assert-NoReparsePoint -Path $evidence)
    if (-not (Test-Path -LiteralPath $evidence -PathType Leaf)) { throw 'Evidence destination is not a regular file.' }
}

# Bounded capture runs on native reader threads, avoiding PowerShell event
# runspace races. A separate hidden console allows CTRL+BREAK to target ONLY
# the exact portable process's console group, never this shell or another app.
if (-not ('DistributionTestProcess' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Diagnostics;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Threading;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;
public sealed class DistributionTestProcess : IDisposable {
    [StructLayout(LayoutKind.Sequential)] struct SA { public int length; public IntPtr security; public int inherit; }
    [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] struct SI {
        public int cb; public string reserved, desktop, title; public int x,y,xSize,ySize,xChars,yChars,fill,flags;
        public short show, reservedSize; public IntPtr reservedPtr,input,output,error;
    }
    [StructLayout(LayoutKind.Sequential)] struct PI { public IntPtr process,thread; public uint pid,tid; }
    [DllImport("kernel32.dll", SetLastError=true)] static extern bool CreatePipe(out IntPtr read,out IntPtr write,ref SA sa,int size);
    [DllImport("kernel32.dll", SetLastError=true)] static extern bool SetHandleInformation(IntPtr h,uint mask,uint flags);
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern bool CreateProcess(string app,StringBuilder args,IntPtr psa,IntPtr tsa,bool inherit,uint flags,IntPtr env,string dir,ref SI si,out PI pi);
    [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);
    public Process Process { get; private set; }
    readonly StringBuilder stdout=new StringBuilder(), stderr=new StringBuilder();
    Thread outReader,errReader;
    const int Limit=65536;
    public bool Truncated { get; private set; }
    public string Output { get { lock(stdout) return stdout.ToString(); } }
    public string Error { get { lock(stderr) return stderr.ToString(); } }
    static string Quote(string s) {
        var b=new StringBuilder("\""); int slashes=0;
        foreach(char c in s) { if(c=='\\') { slashes++; continue; } if(c=='"') { b.Append('\\',slashes*2+1); b.Append(c); } else { b.Append('\\',slashes); b.Append(c); } slashes=0; }
        b.Append('\\',slashes*2); return b.Append('"').ToString();
    }
    void Drain(IntPtr handle,StringBuilder buffer) {
        using(var stream=new FileStream(new SafeFileHandle(handle,true),FileAccess.Read))
        using(var reader=new StreamReader(stream,Encoding.UTF8,true)) {
            char[] chars=new char[1024]; int n;
            while((n=reader.Read(chars,0,chars.Length))>0) lock(buffer) {
                int accepted=Math.Min(n,Limit-buffer.Length); if(accepted>0) buffer.Append(chars,0,accepted); if(accepted<n) Truncated=true;
            }
        }
    }
    public static DistributionTestProcess Start(string app,string[] args,string dir,Dictionary<string,string> environment,bool isolatedConsole=true) {
        var result=new DistributionTestProcess(); IntPtr or=IntPtr.Zero,ow=IntPtr.Zero,er=IntPtr.Zero,ew=IntPtr.Zero,ir=IntPtr.Zero,iw=IntPtr.Zero;
        var sa=new SA { length=Marshal.SizeOf(typeof(SA)),inherit=1 }; IntPtr env=IntPtr.Zero;
        try {
            if(!CreatePipe(out or,out ow,ref sa,0)||!CreatePipe(out er,out ew,ref sa,0)||!CreatePipe(out ir,out iw,ref sa,0)) throw new System.ComponentModel.Win32Exception();
            if(!SetHandleInformation(or,1,0)||!SetHandleInformation(er,1,0)||!SetHandleInformation(iw,1,0)) throw new System.ComponentModel.Win32Exception();
            var si=new SI { cb=Marshal.SizeOf(typeof(SI)),flags=0x101,show=0,input=ir,output=ow,error=ew };
            var cmd=new StringBuilder(Quote(app)); foreach(string arg in args) cmd.Append(' ').Append(Quote(arg));
            var keys=new List<string>(environment.Keys); keys.Sort(StringComparer.OrdinalIgnoreCase); var block=new StringBuilder();
            foreach(string key in keys) block.Append(key).Append('=').Append(environment[key]).Append('\0'); block.Append('\0'); env=Marshal.StringToHGlobalUni(block.ToString());
            PI pi; // NEW_CONSOLE has its own root process group; no inherited user console.
            uint flags=isolatedConsole ? 0x410u : 0x08000400u;
            if(!CreateProcess(app,cmd,IntPtr.Zero,IntPtr.Zero,true,flags,env,dir,ref si,out pi)) throw new System.ComponentModel.Win32Exception();
            try {
                result.Process=Process.GetProcessById((int)pi.pid);
                // Open/cache the Process handle while the original native
                // handle still keeps even a very short-lived child alive.
                // GetProcessById alone cannot retrieve ExitCode after exit.
                IntPtr cached=result.Process.Handle;
            } finally { CloseHandle(pi.process); CloseHandle(pi.thread); }
            IntPtr outHandle=or,errHandle=er;
            result.outReader=new Thread(()=>result.Drain(outHandle,result.stdout)) { IsBackground=true };
            result.errReader=new Thread(()=>result.Drain(errHandle,result.stderr)) { IsBackground=true };
            result.outReader.Start(); result.errReader.Start(); or=er=IntPtr.Zero; return result;
        } finally { foreach(IntPtr h in new[]{or,ow,er,ew,ir,iw}) if(h!=IntPtr.Zero) CloseHandle(h); if(env!=IntPtr.Zero) Marshal.FreeHGlobal(env); }
    }
    public void FinishCapture() { outReader.Join(3000); errReader.Join(3000); }
    public void Dispose() { Process.Dispose(); }
}
'@
}

$completed = [Collections.Generic.List[string]]::new()
$children = [Collections.Generic.List[object]]::new()
$sentinels = [Collections.Generic.List[object]]::new()
$hostSentinels = [Collections.Generic.List[object]]::new()
$productionSnapshot = $null
$isolatedInstallerSourceCommit = $null
$isolatedInstallerIssSha256 = $null
$isolatedInstallerLicenseSha256 = $null
$olderFixtureExecutableCommit = $null
$step = 'preflight'
$owned = $false
$collision = $null
$installed = $null
$portablePrimary = $null
$failure = $null
$cleanupFailures = [Collections.Generic.List[string]]::new()

function Get-TestHash([string]$Path) { (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }
function Remember-Sentinel([string]$Path, $List) {
    $List.Add([pscustomobject]@{ path=$Path; present=(Test-Path -LiteralPath $Path -PathType Leaf); hash=$(if (Test-Path -LiteralPath $Path -PathType Leaf) { Get-TestHash $Path } else { '' }) })
}
function Assert-Sentinels($List) {
    foreach ($entry in $List) {
        if ((Test-Path -LiteralPath $entry.path -PathType Leaf) -ne $entry.present) { throw 'Preserved sentinel presence changed.' }
        if ($entry.present -and (Get-TestHash $entry.path) -cne $entry.hash) { throw 'Preserved sentinel contents changed.' }
    }
}
function Read-ProductionRegistration {
    $present = Test-Path -LiteralPath $productionRegistration
    $values = [ordered]@{ present=$present }
    if ($present) {
        $key = Get-Item -LiteralPath $productionRegistration
        foreach ($name in @('DisplayVersion','BuildVersion','InstallLocation')) { $values[$name] = $key.GetValue($name, $null) }
    }
    return ($values | ConvertTo-Json -Compress)
}
function Assert-ProductionPreserved {
    if ((Read-ProductionRegistration) -cne $productionSnapshot) { throw 'Production installation registration changed.' }
    Assert-Sentinels $hostSentinels
}
function Assert-IsolatedInstallerSourceCommit([string]$SourceHead, [string]$ExpectedCandidateCommit) {
    if ($SourceHead -cnotmatch '^[0-9a-f]{40}$' -or $ExpectedCandidateCommit -cnotmatch '^[0-9a-f]{40}$' -or $SourceHead -cne $ExpectedCandidateCommit) {
        throw 'Isolated installer source HEAD does not match the canonical current release manifest commit.'
    }
}
function Start-TestChild([string]$File, [string[]]$Arguments, [switch]$NoConsole) {
    $child = [DistributionTestProcess]::Start($File, $Arguments, $workspace, $childEnvironment, (-not $NoConsole))
    $children.Add($child)
    return $child
}
function Invoke-TestChild([string]$File, [string[]]$Arguments, [switch]$ExpectFailure, [switch]$NoConsole) {
    $child = Start-TestChild $File $Arguments -NoConsole:$NoConsole
    if (-not $child.Process.WaitForExit(120000)) { throw 'Owned test child exceeded its 120 second deadline.' }
    $child.FinishCapture()
    if ($child.Truncated) { throw 'Owned child output exceeded the bounded capture limit.' }
    if ($ExpectFailure) {
        if ($child.Process.ExitCode -eq 0) { throw 'Expected fail-closed invocation unexpectedly succeeded.' }
    } elseif ($child.Process.ExitCode -ne 0) { throw "Owned child failed with exit code $($child.Process.ExitCode)." }
    return $child.Output
}
function Get-Instance([string]$File) { (Invoke-TestChild $File @('--installed-status')) | ConvertFrom-Json }
function Start-TestServer([string]$File, [string[]]$Arguments) {
    $child = Start-TestChild $File $Arguments
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    while ([DateTime]::UtcNow -lt $deadline) {
        if ($child.Process.HasExited) { throw 'Dashboard exited before readiness.' }
        if ($child.Output -match 'RoboRev Dashboard: (http://127\.0\.0\.1:\d+)') {
            return [pscustomobject]@{ child=$child; url=$Matches[1] }
        }
        Start-Sleep -Milliseconds 100
    }
    throw 'Dashboard did not announce readiness in 30 seconds.'
}
function Assert-TestUI($Server) {
    $response = Invoke-WebRequest -Uri ($Server.url + '/') -TimeoutSec 10 -NoProxy
    if ($response.StatusCode -ne 200 -or $response.Headers['Content-Type'] -notmatch 'text/html' -or $response.Content -notmatch '<html') { throw 'Embedded frontend did not start.' }
    $bootstrap = Invoke-RestMethod -Uri ($Server.url + '/api/bootstrap') -TimeoutSec 10 -NoProxy
    if ($bootstrap.roborev.installed) { throw 'Isolated missing-RoboRev fixture discovered an external executable.' }
    if (Test-Path -LiteralPath (Join-Path $profile '.roborev\bin\roborev.exe')) { throw 'RoboRev was installed without approval.' }
}
function Stop-TestPortable($Server) {
    Send-TestBreak $Server.child
    if (-not $Server.child.Process.WaitForExit(30000) -or $Server.child.Process.ExitCode -ne 0) { throw 'Portable did not exit gracefully after CTRL+BREAK.' }
}
function Send-TestBreak($Child) {
    # A consoleless worker attaches to the target. The parent shell's console
    # attachment is never changed, even when invoked from an interactive pwsh.
    [void](Invoke-TestChild $testPowerShell @('-NoProfile','-NonInteractive','-File',$breakHelper,'-TargetPID',([string]$Child.Process.Id)) -NoConsole)
}
function Invoke-TestSetup([string]$File, [string]$Name, [switch]$ExpectFailure) {
    if (-not $UseIsolatedInstallerIdentity -and (Test-Path -LiteralPath $defaultInstall)) { throw 'A production installation appeared during verification; it was preserved.' }
    if ($UseIsolatedInstallerIdentity) { Assert-ProductionPreserved }
    if (Test-Path -LiteralPath $registration) {
        $existing = Get-ItemProperty -LiteralPath $registration
        if ([IO.Path]::GetFullPath($existing.InstallLocation).TrimEnd('\') -ine $installRoot) { throw 'A foreign registration appeared during verification; it was preserved.' }
    }
    [void](Invoke-TestChild $File @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART','/NOICONS','/DASHBOARDNOLAUNCH',('/DIR=' + $installRoot),('/LOG=' + (Join-Path $workspace ($Name + '.log')))) -ExpectFailure:$ExpectFailure)
}
function New-IsolatedTestSetup([string]$SourceDirectory, [string]$PayloadVersion, [string]$Label) {
    # No application compilation or tool installation occurs here. Only the
    # selected test AppId macro differs from the official Setup compiler args.
    $sourceExe = Join-Path $SourceDirectory 'roborev-dashboard.exe'
    $sourceManifest = Get-Content -LiteralPath (Join-Path $SourceDirectory 'roborev-dashboard.manifest.json') -Raw | ConvertFrom-Json
    $source = Assert-OfficialReleaseSource -Executable $sourceExe -Version $PayloadVersion -Commit $sourceManifest.commit
    Assert-IsolatedInstallerSourceCommit (Assert-CleanGitRepository -RepositoryRoot $root -GitPath $sourceGit) $isolatedInstallerSourceCommit
    if ((Get-TestHash (Join-Path $root 'installer\RoboRevDashboard.iss')) -cne $isolatedInstallerIssSha256) { throw 'Bound isolated installer ISS changed before compilation.' }
    $payload = Join-Path $workspace ($Label + '-payload')
    $output = Join-Path $workspace ($Label + '-setup')
    New-Item -ItemType Directory -Path $payload, $output | Out-Null
    foreach ($name in @('roborev-dashboard.exe','roborev-dashboard.manifest.json','roborev-dashboard.sha256')) { Copy-Item -LiteralPath (Join-Path $SourceDirectory $name) -Destination (Join-Path $payload $name) }
    [void](Assert-OfficialReleaseSource -Executable (Join-Path $payload 'roborev-dashboard.exe') -Version $PayloadVersion -Commit $source.commit)
    $args = @('/Qp',('/O' + $output),('/DAppVersion=' + $PayloadVersion),('/DVersionInfoVersion=' + (($PayloadVersion -split '[-+]')[0] + '.0')),('/DSourceDir=' + $payload),('/DOutputDir=' + $output),('/DExeSha256=' + $source.sha256),('/DBaseVersion=' + $PayloadVersion),('/DAppId=' + $testAppId))
    if (Test-Path -LiteralPath (Join-Path $root 'LICENSE') -PathType Leaf) {
        [void](Assert-NoReparsePoint -Path (Join-Path $root 'LICENSE'))
        Copy-Item -LiteralPath (Join-Path $root 'LICENSE') -Destination (Join-Path $payload 'LICENSE')
        if ((Get-TestHash (Join-Path $payload 'LICENSE')) -cne $isolatedInstallerLicenseSha256) { throw 'Bound isolated installer license changed before compilation.' }
        $args += '/DIncludeLicense=1'
    }
    $args += (Join-Path $root 'installer\RoboRevDashboard.iss')
    [void](Invoke-TestChild $testCompiler $args)
    Assert-IsolatedInstallerSourceCommit (Assert-CleanGitRepository -RepositoryRoot $root -GitPath $sourceGit) $isolatedInstallerSourceCommit
    if ((Get-TestHash (Join-Path $root 'installer\RoboRevDashboard.iss')) -cne $isolatedInstallerIssSha256) { throw 'Bound isolated installer ISS changed during compilation.' }
    $compiled = Join-Path $output 'RoboRev-Dashboard-Setup.exe'
    if (-not (Test-Path -LiteralPath $compiled -PathType Leaf)) { throw 'Isolated identity Setup compilation did not produce its artifact.' }
    return $compiled
}
function Assert-InstalledOfficialFiles([string]$ArtifactDirectory, [string]$SourceDirectory, [string]$ExpectedVersion, [string]$ExpectedCommit) {
    # Validate the entire original package.ps1 contract before executing any
    # installed payload. This also gates exact-Setup mode without an extractor.
    [void](Assert-OfficialReleaseSource -Executable (Join-Path $SourceDirectory 'roborev-dashboard.exe') -Version $ExpectedVersion -Commit $ExpectedCommit)
    [void](Assert-OfficialReleaseSource -Executable (Join-Path $ArtifactDirectory 'roborev-dashboard.exe') -Version $ExpectedVersion -Commit $ExpectedCommit)
    foreach ($name in @('roborev-dashboard.exe','roborev-dashboard.manifest.json','roborev-dashboard.sha256')) {
        $actual = (Get-FileHash -LiteralPath (Join-Path $ArtifactDirectory $name) -Algorithm SHA256).Hash
        $expected = (Get-FileHash -LiteralPath (Join-Path $SourceDirectory $name) -Algorithm SHA256).Hash
        if ($actual -cne $expected) { throw 'Installed official executable/manifest/SHA-256 files differ from the verified release source.' }
    }
}
function Assert-InstalledArtifact {
    Assert-InstalledOfficialFiles $installRoot $candidate $Version $manifest.commit
    $metadata = (Invoke-TestChild $installedExe @('--version-json')) | ConvertFrom-Json
    if ($metadata.version -cne $Version -or $metadata.commit -cne $manifest.commit -or $metadata.release -ne $true -or $metadata.target -cne 'windows/amd64' -or $metadata.roborevCompatibility -cne 'v0.64.0') { throw 'Installed official executable provenance mismatch.' }
    $registered = Get-ItemProperty -LiteralPath $registration
    if ($registered.DisplayVersion -cne $Version -or $registered.BuildVersion -cne $Version -or [IO.Path]::GetFullPath($registered.InstallLocation).TrimEnd('\') -ine $installRoot) { throw 'Official installation registration version or location mismatch.' }
}
function Invoke-OwnedUninstaller {
    if (-not (Test-Path -LiteralPath $registration)) { return }
    $registered = Get-ItemProperty -LiteralPath $registration
    if ([IO.Path]::GetFullPath($registered.InstallLocation).TrimEnd('\') -ine $installRoot) { throw 'Refusing to uninstall a registration outside the owned install directory.' }
    [void](Assert-NoReparseTree -Path $installRoot)
    $uninstallers = @(Get-ChildItem -LiteralPath $installRoot -Filter 'unins*.exe' -File)
    if ($uninstallers.Count -ne 1) { throw 'Owned installation does not contain exactly one uninstaller.' }
    [void](Invoke-TestChild $uninstallers[0].FullName @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',('/LOG=' + (Join-Path $workspace 'uninstall.log'))))
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    while ((Test-Path -LiteralPath $installedExe) -and [DateTime]::UtcNow -lt $deadline) { Start-Sleep -Milliseconds 100 }
    if ((Test-Path -LiteralPath $registration) -or (Test-Path -LiteralPath $installedExe)) { throw 'Actual uninstaller did not remove its executable/registration.' }
}

try {
    $productionSnapshot = Read-ProductionRegistration
    # Hash only these documented, non-secret host targets. Never enumerate .roborev.
    Remember-Sentinel (Join-Path ([Environment]::GetFolderPath('ApplicationData')) 'roborev-dashboard\config.json') $hostSentinels
    Remember-Sentinel (Join-Path $env:USERPROFILE '.roborev\bin\roborev.exe') $hostSentinels
    if ($UseIsolatedInstallerIdentity -and (Test-Path -LiteralPath $productionRegistration)) {
        $hostInstall = [string](Get-Item -LiteralPath $productionRegistration).GetValue('InstallLocation', '')
        if ([string]::IsNullOrWhiteSpace($hostInstall) -or -not [IO.Path]::IsPathFullyQualified($hostInstall)) { throw 'Cannot safely snapshot existing production installation location.' }
        [void](Assert-SafeExistingParent -Path $hostInstall)
        foreach ($name in @('roborev-dashboard.exe','roborev-dashboard.manifest.json','roborev-dashboard.sha256','LICENSE')) {
            $hostFile = Join-Path $hostInstall $name
            if (Test-Path -LiteralPath $hostFile) { [void](Assert-NoReparsePoint -Path $hostFile) }
            Remember-Sentinel $hostFile $hostSentinels
        }
    }
    New-Item -ItemType Directory -Path $workspace | Out-Null
    $owned = $true
    $installRoot = Join-Path $workspace 'installed 한글 경로'
    $portableRoot = Join-Path $workspace 'portable 한글 경로'
    $profile = Join-Path $workspace 'profile'
    $appData = Join-Path $profile 'AppData\Roaming'
    $localData = Join-Path $profile 'AppData\Local'
    $separateConfig = Join-Path $workspace 'portable-config'
    $configPath = Join-Path $appData 'roborev-dashboard\config.json'
    $reviewSentinel = Join-Path $profile '.roborev\distribution-review-sentinel.txt'
    New-Item -ItemType Directory -Path $portableRoot, $localData, (Split-Path -Parent $configPath), (Split-Path -Parent $reviewSentinel), $separateConfig -Force | Out-Null
    $configFixture = [ordered]@{ recentRepositories=@((Join-Path $workspace 'registered-repository')); lastSelectedRepository=(Join-Path $workspace 'registered-repository'); panelSelections=@{} } | ConvertTo-Json -Compress
    [IO.File]::WriteAllText($configPath, $configFixture)
    [IO.File]::WriteAllText((Join-Path $separateConfig 'config.json'), $configFixture)
    [IO.File]::WriteAllText($reviewSentinel, 'Owned fixture review data must survive installation, replacement, execution and removal.')
    foreach ($sentinel in @($configPath, (Join-Path $separateConfig 'config.json'), $reviewSentinel)) { Remember-Sentinel $sentinel $sentinels }
    $childEnvironment = [Collections.Generic.Dictionary[string,string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($entry in @{ SystemRoot=$env:SystemRoot; WINDIR=$env:SystemRoot; SystemDrive=$env:SystemDrive; COMSPEC=(Join-Path $env:SystemRoot 'System32\cmd.exe'); PATH=(Join-Path $env:SystemRoot 'System32'); PATHEXT='.EXE;.COM'; USERPROFILE=$profile; HOME=$profile; APPDATA=$appData; LOCALAPPDATA=$localData; TEMP=$workspace; TMP=$workspace; USERNAME=$env:USERNAME; USERDOMAIN=$env:USERDOMAIN }.GetEnumerator()) { $childEnvironment[$entry.Key] = $entry.Value }
    $testPowerShell = (Get-Process -Id $PID).Path
    $breakHelper = Join-Path $workspace 'send-owned-console-break.ps1'
    [IO.File]::WriteAllText($breakHelper, @'
param([Parameter(Mandatory)][uint32]$TargetPID)
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class OwnedDistributionBreak {
    delegate bool Handler(uint code);
    [DllImport("kernel32.dll")] static extern bool AttachConsole(uint pid);
    [DllImport("kernel32.dll")] static extern bool FreeConsole();
    [DllImport("kernel32.dll")] static extern bool SetConsoleCtrlHandler(Handler handler, bool add);
    [DllImport("kernel32.dll")] static extern bool GenerateConsoleCtrlEvent(uint kind, uint group);
    static readonly Handler callback = code => true;
    public static bool Send(uint pid) {
        FreeConsole(); // only this dedicated helper, never the calling shell
        if (!AttachConsole(pid)) return false;
        // Broadcast only inside this freshly created, test-owned console.
        // NEW_CONSOLE does not create a targetable CREATE_NEW_PROCESS_GROUP.
        try { SetConsoleCtrlHandler(callback, true); bool sent=GenerateConsoleCtrlEvent(1, 0); System.Threading.Thread.Sleep(200); return sent; }
        finally { FreeConsole(); }
    }
}
"@
if (-not [OwnedDistributionBreak]::Send($TargetPID)) { exit 9 }
exit 0
'@)
    $installedExe = Join-Path $installRoot 'roborev-dashboard.exe'
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    $exeHash = Get-TestHash $officialExe
    if ($manifest.version -cne $Version -or $manifest.sha256 -cne $exeHash -or $manifest.target -cne 'windows/amd64' -or $manifest.roborevCompatibility -cne 'v0.64.0' -or $manifest.commit -notmatch '^[0-9a-f]{40}$') { throw 'Official release manifest contract mismatch.' }
    $step = 'portable-exact-entries-and-provenance'
    $zip = [IO.Compression.ZipFile]::OpenRead($portable)
    try {
        $names = @($zip.Entries | ForEach-Object { $_.FullName } | Sort-Object)
        if (($names -join '|') -cne 'roborev-dashboard.exe|roborev-dashboard.manifest.json|roborev-dashboard.sha256') { throw 'Unexpected portable ZIP entries.' }
        foreach ($entry in $zip.Entries) { [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, (Join-Path $portableRoot $entry.FullName)) }
    } finally { $zip.Dispose() }
    $portableExe = Join-Path $portableRoot 'roborev-dashboard.exe'
    foreach ($name in $names) { if ((Get-TestHash (Join-Path $portableRoot $name)) -cne (Get-TestHash (Join-Path $candidate $name))) { throw 'Portable entry differs from official release bytes.' } }
    $portableMetadata = (Invoke-TestChild $portableExe @('--version-json')) | ConvertFrom-Json
    if ($portableMetadata.version -cne $Version -or $portableMetadata.release -ne $true -or $portableMetadata.commit -cne $manifest.commit) { throw 'Portable version/provenance mismatch.' }
    $completed.Add($step)

    if ($SetupExtractorPath) {
        $step = 'official-setup-payload-readback'
        $extracted = Join-Path $workspace 'official-setup-readback'
        New-Item -ItemType Directory -Path $extracted | Out-Null
        [void](Invoke-TestChild $SetupExtractorPath @('--extract','--silent','--output-dir',$extracted,$setup))
        [void](Assert-NoReparseTree -Path $extracted)
        $extractedApp = Join-Path $extracted 'app'
        foreach ($name in @('roborev-dashboard.exe','roborev-dashboard.manifest.json','roborev-dashboard.sha256')) {
            if ((Get-TestHash (Join-Path $extractedApp $name)) -cne (Get-TestHash (Join-Path $candidate $name))) { throw 'Exact official Setup payload differs from the official/portable bytes.' }
        }
        $allowedPayload = @('roborev-dashboard.exe','roborev-dashboard.manifest.json','roborev-dashboard.sha256')
        if (Test-Path -LiteralPath (Join-Path $root 'LICENSE') -PathType Leaf) {
            $allowedPayload += 'LICENSE'
            if ((Get-TestHash (Join-Path $extractedApp 'LICENSE')) -cne (Get-TestHash (Join-Path $root 'LICENSE'))) { throw 'Exact official Setup license differs from its source.' }
        }
        $actualPayload = @(Get-ChildItem -LiteralPath $extractedApp -Force)
        if ($actualPayload.Count -ne $allowedPayload.Count -or @($actualPayload | Where-Object { $_.PSIsContainer -or $_.Name -cnotin $allowedPayload }).Count -ne 0) { throw 'Exact official Setup contains unexpected application payload.' }
        $readbackMetadata = (Invoke-TestChild (Join-Path $extractedApp 'roborev-dashboard.exe') @('--version-json')) | ConvertFrom-Json
        if ($readbackMetadata.version -cne $Version -or $readbackMetadata.commit -cne $manifest.commit -or $readbackMetadata.release -ne $true) { throw 'Exact official Setup payload provenance mismatch.' }
        $completed.Add($step)
    }

    $executedSetup = $setup
    $executedOlderSetup = $OlderSetupPath
    if ($UseIsolatedInstallerIdentity) {
        $step = 'isolated-identity-setup-compilation'
        $canonicalCurrent = Assert-OfficialReleaseSource -Executable $officialExe -Version $Version -Commit $manifest.commit
        $sourceGit = Resolve-DistributionGit
        $isolatedInstallerSourceCommit = Assert-CleanGitRepository -RepositoryRoot $root -GitPath $sourceGit
        Assert-IsolatedInstallerSourceCommit $isolatedInstallerSourceCommit $canonicalCurrent.commit
        [void](Assert-SafeExistingParent -Path (Join-Path $root 'installer'))
        [void](Assert-NoReparsePoint -Path (Join-Path $root 'installer\RoboRevDashboard.iss'))
        $isolatedInstallerIssSha256 = Get-TestHash (Join-Path $root 'installer\RoboRevDashboard.iss')
        if (Test-Path -LiteralPath (Join-Path $root 'LICENSE') -PathType Leaf) {
            [void](Assert-NoReparsePoint -Path (Join-Path $root 'LICENSE'))
            $isolatedInstallerLicenseSha256 = Get-TestHash (Join-Path $root 'LICENSE')
        }
        . (Join-Path $PSScriptRoot 'prepare-inno.ps1')
        $compilerDirectory = Join-Path $root 'dist\tools\innosetup-6.7.3'
        [void](Assert-SafeExistingParent -Path $compilerDirectory)
        $testCompiler = Assert-InnoCompilerPayload -CompilerDirectory $compilerDirectory
        $executedSetup = New-IsolatedTestSetup $candidate $Version 'current'
        if ($OlderSetupPath) {
            $olderSourceDirectory = Split-Path -Parent $OlderSetupPath
            $olderSourceManifest = Get-Content -LiteralPath (Join-Path $olderSourceDirectory 'roborev-dashboard.manifest.json') -Raw | ConvertFrom-Json
            $canonicalOlder = Assert-OfficialReleaseSource -Executable (Join-Path $olderSourceDirectory 'roborev-dashboard.exe') -Version $OlderVersion -Commit $olderSourceManifest.commit
            $olderFixtureExecutableCommit = $canonicalOlder.commit
            $executedOlderSetup = New-IsolatedTestSetup $olderSourceDirectory $OlderVersion 'older'
        }
        $completed.Add($step)
    }
    $executedSetupHash = Get-TestHash $executedSetup
    $executedOlderSetupHash = if ($executedOlderSetup) { Get-TestHash $executedOlderSetup } else { $null }

    $step = 'plain-portable-toolchain-free-and-repeat-safety'
    $collision = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 7410)
    $collision.Start()
    $portablePrimary = Start-TestServer $portableExe @('--no-open')
    if ($portablePrimary.url -ceq 'http://127.0.0.1:7410') { throw 'Portable adopted foreign listener.' }
    Assert-TestUI $portablePrimary
    [void](Invoke-TestChild $portableExe @('--no-open') -ExpectFailure)
    Assert-Sentinels $sentinels
    Stop-TestPortable $portablePrimary
    $portablePrimary = $null
    $completed.Add($step)

    if ($OlderSetupPath) {
        $step = 'older-version-first-install'
        Invoke-TestSetup $executedOlderSetup 'older-install'
        if ((Get-ItemProperty -LiteralPath $registration).BuildVersion -cne $OlderVersion) { throw 'Older fixture registered version differs.' }
        $installed = Start-TestServer $installedExe @('--installed','--no-open')
        $completed.Add($step)
    }
    $step = $(if ($OlderSetupPath) { 'forward-upgrade-while-running' } else { 'official-first-install' })
    Invoke-TestSetup $executedSetup 'official-install'
    if ($installed -and -not $installed.child.Process.WaitForExit(10000)) { throw 'Upgrade did not gracefully stop its exact installed process.' }
    $installed = $null
    Assert-InstalledArtifact
    Assert-Sentinels $sentinels
    $completed.Add($step)

    $step = 'installed-duplicate-port-collision-and-portable-coexistence'
    $installed = Start-TestServer $installedExe @('--installed','--no-open')
    $state = Get-Instance $installedExe
    if ($state.pid -ne $installed.child.Process.Id -or $state.url -cne $installed.url -or $state.url -ceq 'http://127.0.0.1:7410') { throw 'Installed identity/foreign listener mismatch.' }
    [void](Invoke-TestChild $installedExe @('--installed','--no-open'))
    if ((Get-Instance $installedExe).pid -ne $state.pid) { throw 'Duplicate installed invocation started a second server.' }
    [void](Invoke-TestChild $portableExe @('--no-open') -ExpectFailure)
    $portablePrimary = Start-TestServer $portableExe @('--no-open','--config-dir',$separateConfig)
    if ($portablePrimary.url -ceq $installed.url -or $portablePrimary.url -ceq 'http://127.0.0.1:7410') { throw 'Independent portable process reused an occupied port.' }
    Assert-TestUI $installed
    Assert-TestUI $portablePrimary
    [void](Invoke-TestChild $portableExe @('--no-open','--config-dir',$separateConfig) -ExpectFailure)
    Assert-Sentinels $sentinels
    $completed.Add($step)

    $step = 'graceful-installed-stop-preserves-portable'
    [void](Invoke-TestChild $installedExe @('--stop-installed'))
    if (-not $installed.child.Process.WaitForExit(10000) -or $installed.child.Process.ExitCode -ne 0) { throw 'Installed process did not exit gracefully.' }
    $installed = $null
    Assert-TestUI $portablePrimary
    $completed.Add($step)

    $step = 'same-version-reinstall-while-running'
    $installed = Start-TestServer $installedExe @('--installed','--no-open')
    Invoke-TestSetup $executedSetup 'same-version-reinstall'
    if (-not $installed.child.Process.WaitForExit(10000)) { throw 'Reinstall did not stop its exact owned instance.' }
    $installed = $null
    Assert-InstalledArtifact
    Assert-TestUI $portablePrimary
    Assert-Sentinels $sentinels
    $completed.Add($step)

    if ($OlderSetupPath) {
        $step = 'downgrade-block-preserves-running-current-version'
        $installed = Start-TestServer $installedExe @('--installed','--no-open')
        Invoke-TestSetup $executedOlderSetup 'blocked-downgrade' -ExpectFailure
        Assert-InstalledArtifact
        Assert-TestUI $installed
        if ($installed.child.Process.HasExited) { throw 'Blocked downgrade stopped the current installation.' }
        Assert-Sentinels $sentinels
        $completed.Add($step)
    } else { $installed = Start-TestServer $installedExe @('--installed','--no-open') }

    $step = 'uninstall-running-installed-preserves-portable-and-user-data'
    Invoke-OwnedUninstaller
    if (-not $installed.child.Process.WaitForExit(10000)) { throw 'Uninstall did not stop its owned installed process.' }
    $installed = $null
    Assert-TestUI $portablePrimary
    Assert-Sentinels $sentinels
    Assert-Sentinels $hostSentinels
    if ($UseIsolatedInstallerIdentity) { Assert-ProductionPreserved }
    Stop-TestPortable $portablePrimary
    $portablePrimary = $null
    $completed.Add($step)
} catch { $failure = $_; } finally {
    if ($collision) { $collision.Stop() }
    if ($owned) {
        try {
            if ($installed -and -not $installed.child.Process.HasExited -and (Test-Path -LiteralPath $installedExe)) { [void](Invoke-TestChild $installedExe @('--stop-installed')) }
            Invoke-OwnedUninstaller
        } catch { $cleanupFailures.Add('Owned official uninstall cleanup failed; its registration and workspace were preserved.') }
        foreach ($child in $children.ToArray()) {
            try {
                if (-not $child.Process.HasExited) {
                    Send-TestBreak $child
                    if (-not $child.Process.WaitForExit(30000)) {
                        # Cleanup fallback is confined to this handle/PID, never
                        # a process name, unrelated child or real RoboRev daemon.
                        $child.Process.Kill()
                        [void]$child.Process.WaitForExit(5000)
                        $cleanupFailures.Add('An owned process required forced cleanup; graceful verification did not pass.')
                    }
                }
                $child.FinishCapture()
                $child.Dispose()
            } catch { $cleanupFailures.Add('Owned process cleanup failed.') }
        }
        try { Assert-Sentinels $hostSentinels; if ($UseIsolatedInstallerIdentity) { Assert-ProductionPreserved } } catch { $cleanupFailures.Add('Host non-secret sentinel preservation failed.') }
        if ($cleanupFailures.Count -eq 0 -and -not (Test-Path -LiteralPath $registration)) {
            [void](Assert-NoReparseTree -Path $workspace)
            if (-not (Test-PathWithin -Path $workspace -Root (Join-Path $root '.tmp'))) { throw 'Owned cleanup containment check failed.' }
            Remove-Item -LiteralPath $workspace -Recurse -Force
        }
    }
    # Evidence deliberately excludes child output, config bodies, host paths,
    # profile/environment dumps and installation logs.
    $result = [ordered]@{ version=$Version; platform='windows/amd64'; installerMode=$(if ($UseIsolatedInstallerIdentity) { 'isolated-test-AppId; official payload and unchanged ISS' } else { 'exact official Setup' }); exactOfficialSetupLifecycle=(-not $UseIsolatedInstallerIdentity); passed=$completed.ToArray(); failed=$(if ($failure) { $step } else { $null }); cleanupFailures=$cleanupFailures.ToArray(); executableSha256=$(if (Test-Path -LiteralPath $officialExe) { Get-TestHash $officialExe } else { $null }); setupSha256=(Get-TestHash $setup); executedSetupSha256=$(if (Get-Variable executedSetupHash -ErrorAction SilentlyContinue) { $executedSetupHash } else { $null }); portableSha256=(Get-TestHash $portable); olderSetupSha256=$(if ($OlderSetupPath) { Get-TestHash $OlderSetupPath } else { $null }); executedOlderSetupSha256=$(if (Get-Variable executedOlderSetupHash -ErrorAction SilentlyContinue) { $executedOlderSetupHash } else { $null }); olderVersion=$(if ($OlderVersion) { $OlderVersion } else { $null }); unverified=@('interactive approval/cancellation','Start Menu browser opening','SmartScreen experience') }
    if ($UseIsolatedInstallerIdentity) { $result.unverified += 'exact production-AppId Setup lifecycle; requires a fresh profile' }
    if (-not $SetupExtractorPath) { $result.unverified += 'nonexecuting exact original Setup payload readback' }
    $result.setupExtractorSha256 = $(if ($SetupExtractorPath) { $SetupExtractorSha256 } else { $null })
    $result.isolatedInstallerSourceCommit = $isolatedInstallerSourceCommit
    $result.isolatedInstallerIssSha256 = $isolatedInstallerIssSha256
    $result.isolatedInstallerLicenseSha256 = $isolatedInstallerLicenseSha256
    $result.olderFixtureExecutableCommit = $olderFixtureExecutableCommit
    $result.olderFixtureInstallerSourceCommit = $(if ($UseIsolatedInstallerIdentity -and $OlderSetupPath) { $isolatedInstallerSourceCommit } else { $null })
    $result.olderFixtureInstallerSourceMatchesExecutableCommit = $(if ($UseIsolatedInstallerIdentity -and $OlderSetupPath) { $olderFixtureExecutableCommit -ceq $isolatedInstallerSourceCommit } else { $null })
    $result.olderFixtureProvenance = $(if ($UseIsolatedInstallerIdentity -and $OlderSetupPath) { 'Version/upgrade fixture rebuilt with current clean installer source; historical shipped installer source is not verified.' } elseif ($OlderSetupPath) { 'Supplied original older Setup executed without rebuilding.' } else { $null })
    if (-not $OlderSetupPath) { $result.unverified += @('forward upgrade from older official version','downgrade rejection') }
    [IO.File]::WriteAllText($evidence, ($result | ConvertTo-Json -Depth 5) + "`n", [Text.UTF8Encoding]::new($false))
}
if ($cleanupFailures.Count -gt 0) { throw ($cleanupFailures -join ' ') }
if ($failure) { throw $failure }
Write-Host "PASS: actual Setup/portable lifecycle; sanitized evidence: $evidence"
