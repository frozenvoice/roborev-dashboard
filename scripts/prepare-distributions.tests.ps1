# Deterministic fixtures only: never compile, install, or run RoboRev/the Dashboard.
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'distribution-common.ps1')

function Assert-DistributionTest { param([bool]$Condition, [string]$Message) if (!$Condition) { throw "Distribution assertion failed: $Message" } }
function Assert-DistributionThrows {
    param([scriptblock]$Action, [string]$Message, [string]$ExpectedError)
    $thrown = $false
    try { & $Action | Out-Null } catch {
        $thrown = $true
        if ($ExpectedError) { Assert-DistributionTest ($_.Exception.Message.Contains($ExpectedError)) "unexpected rejection: $($_.Exception.Message)" }
    }
    Assert-DistributionTest $thrown $Message
}
function New-DistributionFixture {
    param([string]$Directory, [string]$Version = '0.1.0', [string]$Commit = ('a' * 40))
    New-Item -ItemType Directory -Path $Directory -Force | Out-Null
    $exe = Join-Path $Directory 'roborev-dashboard.exe'
    [IO.File]::WriteAllBytes($exe, [byte[]](0x4D, 0x5A, 1, 2, 3, 4))
    $hash = Get-FileSha256Lower -Path $exe
    $manifest = New-ReleaseManifest -Version $Version -Commit $Commit -Size 6 -Sha256 $hash
    Write-Utf8NoBom -Path (Join-Path $Directory 'roborev-dashboard.manifest.json') -Content (ConvertTo-DeterministicJsonText -Value $manifest)
    Write-Utf8NoBom -Path (Join-Path $Directory 'roborev-dashboard.sha256') -Content "$hash  roborev-dashboard.exe`n"
}
$testRoot = Get-FullPathSafe -Path (Join-Path $PSScriptRoot '..\.tmp\distribution-tests')
if (Test-Path -LiteralPath $testRoot) { throw "Refusing to reuse existing distribution tests: $testRoot" }
[void](Assert-SafeExistingParent -Path $testRoot)
New-Item -ItemType Directory -Path $testRoot | Out-Null
try {
    $realGit = Resolve-DistributionGit
    Assert-DistributionTest ($realGit -is [string] -and [IO.Path]::IsPathRooted($realGit)) 'Git resolution returns one absolute string'
    $firstGitDirectory = Join-Path $testRoot 'first-git'
    $secondGitDirectory = Join-Path $testRoot 'second-git'
    foreach ($directory in @($firstGitDirectory, $secondGitDirectory)) {
        New-Item -ItemType Directory -Path $directory | Out-Null
        # Discovery fixtures only: these files are never executed.
        [IO.File]::WriteAllBytes((Join-Path $directory 'git.exe'), [byte[]](0x4D, 0x5A))
    }
    $savedGitTestPath = [Environment]::GetEnvironmentVariable('PATH', 'Process')
    try {
        $env:PATH = $firstGitDirectory + [IO.Path]::PathSeparator + $secondGitDirectory + [IO.Path]::PathSeparator + $savedGitTestPath
        Assert-DistributionTest (@(Get-Command git -CommandType Application -All).Count -ge 2) 'duplicate Git PATH fixture exposes multiple applications'
        $selectedGit = Resolve-DistributionGit
        Assert-DistributionTest ($selectedGit -is [string] -and $selectedGit -ceq (Join-Path $firstGitDirectory 'git.exe')) 'duplicate Git resolution preserves first PATH entry'
        $env:PATH = $secondGitDirectory + [IO.Path]::PathSeparator + $firstGitDirectory + [IO.Path]::PathSeparator + $savedGitTestPath
        Assert-DistributionTest ((Resolve-DistributionGit) -ceq (Join-Path $secondGitDirectory 'git.exe')) 'Git selection follows changed PATH priority'
    } finally { [Environment]::SetEnvironmentVariable('PATH', $savedGitTestPath, 'Process') }
    $cleanGitFixture = Join-Path $testRoot 'clean-source'
    $emptyGitTemplate = Join-Path $testRoot 'empty-template'
    $emptyGitHooks = Join-Path $testRoot 'empty-hooks'
    foreach ($directory in @($cleanGitFixture, $emptyGitTemplate, $emptyGitHooks)) { New-Item -ItemType Directory -Path $directory | Out-Null }
    & $realGit -C $cleanGitFixture init --quiet ("--template=" + $emptyGitTemplate)
    if ($LASTEXITCODE -ne 0) { throw 'Git source guard fixture initialization failed.' }
    & $realGit -C $cleanGitFixture -c user.name=DistributionFixture -c user.email=fixture@example.invalid -c ("core.hooksPath=" + $emptyGitHooks) commit --allow-empty --no-gpg-sign --quiet -m 'Distribution fixture'
    if ($LASTEXITCODE -ne 0) { throw 'Git source guard fixture commit failed.' }
    $fixtureCommit = Assert-CleanGitRepository -RepositoryRoot $cleanGitFixture -GitPath (Resolve-DistributionGit)
    Assert-DistributionTest ($fixtureCommit -cmatch '^[0-9a-f]{40}$') 'resolved Git performs actual clean-source commit read'
    Assert-DistributionThrows {
        Invoke-DistributionPreparation -ProjectRoot $cleanGitFixture -DistributionVersion '0.1.0' -Destination (Join-Path $testRoot 'guard-candidate') -PackageBuilder { throw 'Clean Git guard reached fake package.' }
    } 'default distribution clean guard reaches the fake build with duplicate host Git installations' 'Clean Git guard reached fake package'
    Assert-DistributionTest (!(Test-Path -LiteralPath (Join-Path $testRoot 'guard-candidate'))) 'Git guard fixture produces no release candidate'
    $source = Join-Path $testRoot 'official'
    New-DistributionFixture -Directory $source
    $exe = Join-Path $source 'roborev-dashboard.exe'
    $manifestPath = Join-Path $source 'roborev-dashboard.manifest.json'
    $sidecarPath = Join-Path $source 'roborev-dashboard.sha256'
    $manifestText = [IO.File]::ReadAllText($manifestPath)
    $sidecarText = [IO.File]::ReadAllText($sidecarPath)
    [void](Assert-OfficialReleaseSource -Executable $exe -Version '0.1.0' -Commit ('a' * 40))
    foreach ($fieldChange in @(
        @{ name = 'other' }, @{ version = '0.2.0' }, @{ commit = ('b' * 40) }, @{ target = 'linux/amd64' },
        @{ roborevCompatibility = 'v0.65.0' }, @{ size = 7 }, @{ size = '6' }, @{ sha256 = ('0' * 64) }, @{ signed = $true }, @{ signed = 'false' })) {
        $value = $manifestText | ConvertFrom-Json
        foreach ($key in $fieldChange.Keys) { $value.$key = $fieldChange[$key] }
        Write-Utf8NoBom -Path $manifestPath -Content ($value | ConvertTo-Json)
        Assert-DistributionThrows { Assert-OfficialReleaseSource -Executable $exe -Version '0.1.0' -Commit ('a' * 40) } 'manifest mismatch/type rejection'
    }
    foreach ($invalidJson in @($manifestText.Replace('"name":', '"local":false,"name":'), $manifestText.Replace('"name":', '"name":"duplicate","name":'))) {
        Write-Utf8NoBom -Path $manifestPath -Content $invalidJson
        Assert-DistributionThrows { Assert-OfficialReleaseSource -Executable $exe -Version '0.1.0' -Commit ('a' * 40) } 'extra/duplicate manifest property rejection'
    }
    Write-Utf8NoBom -Path $manifestPath -Content $manifestText
    Write-Utf8NoBom -Path $sidecarPath -Content ($sidecarText.Replace('roborev-dashboard.exe', 'other.exe'))
    Assert-DistributionThrows { Assert-OfficialReleaseSource -Executable $exe -Version '0.1.0' -Commit ('a' * 40) } 'sidecar name rejection'
    Write-Utf8NoBom -Path $sidecarPath -Content $sidecarText
    Move-Item -LiteralPath $sidecarPath -Destination ($sidecarPath + '.missing')
    Assert-DistributionThrows { Assert-OfficialReleaseSource -Executable $exe -Version '0.1.0' -Commit ('a' * 40) } 'missing official sidecar rejection'
    Move-Item -LiteralPath ($sidecarPath + '.missing') -Destination $sidecarPath
    Assert-DistributionThrows {
        Invoke-ArtifactCommandContract -GoPath 'fake-go' -ArtifactPath $exe -ExpectedVersion '0.1.0' -ExpectedCommit ('a' * 40) -ExpectedRelease:$true -NativeInvoker { '{"Action":"fail","Test":"TestArtifactCommandContract","Package":"github.com/froze/roborev-dashboard/internal/releasee2e"}' }
    } 'release provenance rejection cannot pass without a passing harness event'

    $zipA = Join-Path $testRoot 'a.zip'
    $zipB = Join-Path $testRoot 'b.zip'
    New-DeterministicPortableZip -SourceDirectory $source -ZipPath $zipA
    (Get-Item -LiteralPath $exe).LastWriteTimeUtc = [DateTime]::UtcNow.AddYears(-5)
    New-DeterministicPortableZip -SourceDirectory $source -ZipPath $zipB
    Assert-DistributionTest ((Get-FileSha256Lower -Path $zipA) -ceq (Get-FileSha256Lower -Path $zipB)) 'ZIP deterministic across filesystem times'
    Assert-DistributionThrows { New-DeterministicPortableZip -SourceDirectory $source -ZipPath $zipA } 'ZIP no-overwrite'
    # An unchanged EXE is insufficient: every metadata entry is checked too.
    Write-Utf8NoBom -Path $sidecarPath -Content ($sidecarText.Replace('roborev-dashboard.exe', 'different-file.exe'))
    Assert-DistributionThrows { Assert-PortableZip -SourceDirectory $source -ZipPath $zipA } 'ZIP metadata entry mismatch rejection'
    Write-Utf8NoBom -Path $sidecarPath -Content $sidecarText

    $packageFake = {
        param($p, $v, $s, $r)
        Assert-DistributionTest (!(Test-Path -LiteralPath $s)) 'official package receives absent output'
        New-DistributionFixture -Directory $s -Version $v
    }
    $installerFake = {
        param($e, $p, $s, $v, $c)
        $path = Join-Path $s 'RoboRev-Dashboard-Setup.exe'
        [IO.File]::WriteAllBytes($path, [byte[]](5, 6, 7, 8))
        $sha = Get-FileSha256Lower -Path $path
        Write-Utf8NoBom -Path ($path + '.sha256') -Content "$sha  RoboRev-Dashboard-Setup.exe`n"
        [pscustomobject]@{ Version = $v; ExecutableSha256 = Get-FileSha256Lower -Path $e; InstallerSha256 = $sha }
    }
    $guard = { param($p) 'a' * 40 }
    $common = @{ ProjectRoot = $testRoot; DistributionVersion = '0.1.0'; PackageBuilder = $packageFake; InstallerBuilder = $installerFake; SourceGuard = $guard; VerifyReproducible = $true }
    $acquired = [Threading.ManualResetEvent]::new($false)
    $release = [Threading.ManualResetEvent]::new($false)
    $holder = [PowerShell]::Create()
    [void]$holder.AddScript('param($name,$ready,$release) $m = [Threading.Mutex]::new($false,$name); $locked = $false; try { $locked = $m.WaitOne(0); if (!$locked) { throw "Fixture lock unavailable" }; [void]$ready.Set(); [void]$release.WaitOne(10000) } finally { if ($locked) { $m.ReleaseMutex() }; $m.Dispose() }').AddArgument((Get-DistributionLockName -ProjectRoot $testRoot)).AddArgument($acquired).AddArgument($release)
    $async = $holder.BeginInvoke()
    try {
        Assert-DistributionTest ($acquired.WaitOne(5000)) 'fixture lock acquired by independent thread'
        Assert-DistributionThrows { Invoke-DistributionPreparation @common -Destination 'locked' } 'per-checkout lock rejects concurrent build' 'Another distribution build owns this checkout'
    } finally {
        [void]$release.Set()
        $holder.EndInvoke($async) | Out-Null
        $holder.Dispose(); $acquired.Dispose(); $release.Dispose()
    }
    $unicodeDestination = [string][char]0xACF5 + [char]0xBC31 + ' path\candidate'
    $result = Invoke-DistributionPreparation @common -Destination $unicodeDestination
    Assert-DistributionTest ($result.Directory -ceq (Join-Path $testRoot $unicodeDestination)) 'custom output resolution'
    Assert-DistributionFileSet -Directory $result.Directory -Version '0.1.0'
    Assert-DistributionThrows { Invoke-DistributionPreparation @common -Destination $unicodeDestination } 'candidate no-overwrite'
    Assert-DistributionTest (!(Test-Path -LiteralPath (Join-Path $result.Directory 'RoboRev-Dashboard-Setup.exe.sha256'))) 'redundant installer sidecar removed'
    [IO.File]::WriteAllText((Join-Path $result.Directory 'extra.txt'), 'unexpected')
    Assert-DistributionThrows { Assert-DistributionFileSet -Directory $result.Directory -Version '0.1.0' } 'candidate exact allowlist'

    foreach ($failure in @('package', 'installer', 'sidecar', 'extra', 'head', 'dirty')) {
        $failureArguments = $common.Clone()
        switch ($failure) {
            'package' { $failureArguments.PackageBuilder = { param($p, $v, $s, $r) New-DistributionFixture -Directory $s -Version $v; throw 'Fake package failed.' } }
            'installer' { $failureArguments.InstallerBuilder = { throw 'Fake installer failed.' } }
            'sidecar' { $failureArguments.InstallerBuilder = { param($e, $p, $s, $v, $c) $result = & $installerFake $e $p $s $v $c; Write-Utf8NoBom -Path (Join-Path $s 'RoboRev-Dashboard-Setup.exe.sha256') -Content 'bad'; $result } }
            'extra' { $failureArguments.InstallerBuilder = { param($e, $p, $s, $v, $c) $result = & $installerFake $e $p $s $v $c; [IO.File]::WriteAllText((Join-Path $s 'extra.txt'), 'bad'); $result } }
            'head' { $state = @{ count = 0 }; $failureArguments.SourceGuard = { param($p) $state.count++; if ($state.count -eq 1) { 'a' * 40 } else { 'b' * 40 } } }
            'dirty' { $state = @{ count = 0 }; $failureArguments.SourceGuard = { param($p) $state.count++; if ($state.count -eq 1) { 'a' * 40 } else { throw 'Dirty checkout' } } }
        }
        $destination = Join-Path $testRoot 'failure'
        $expectedFailure = switch ($failure) {
            'package' { 'Fake package failed' }; 'installer' { 'Fake installer failed' }; 'sidecar' { 'Installer SHA-256 sidecar mismatch' }
            'extra' { 'exactly six files' }; 'head' { 'Checkout HEAD changed' }; 'dirty' { 'Dirty checkout' }
        }
        Assert-DistributionThrows { Invoke-DistributionPreparation @failureArguments -Destination $destination } "failure must block publication: $failure" $expectedFailure
        Assert-DistributionTest (!(Test-Path -LiteralPath $destination)) "no candidate after $failure"
        Assert-DistributionTest (@(Get-ChildItem -LiteralPath $testRoot -Filter '.dual-distribution-*' -Force).Count -eq 0) "owned stage cleaned after $failure"
    }
    # A valid historical release triple can be accepted independently of package.json.
    New-DistributionFixture -Directory $source -Version '0.0.9'
    [void](Assert-OfficialReleaseSource -Executable $exe -Version '0.0.9' -Commit ('a' * 40))
} finally {
    if (Test-Path -LiteralPath $testRoot) {
        if (!(Test-PathWithin -Path $testRoot -Root (Join-Path $PSScriptRoot '..\.tmp')) -or [IO.Path]::GetFileName($testRoot) -cne 'distribution-tests') { throw 'Unsafe distribution test cleanup.' }
        [void](Assert-SafeExistingParent -Path $testRoot)
        [void](Assert-NoReparseTree -Path $testRoot)
        Remove-Item -LiteralPath $testRoot -Recurse -Force
    }
}
Write-Host 'Distribution contract tests passed.'
