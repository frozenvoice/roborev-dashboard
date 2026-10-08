$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $scriptRoot 'verify-local.ps1')

function Assert-VerifyLocalTest {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "Verifier self-test failed: $Message" }
}

function Remove-VerifySelfTestTree {
    param([string]$Path, [string]$TempBase)
    if (-not (Test-Path -LiteralPath $Path -PathType Any)) { return }
    $resolved = (Resolve-Path -LiteralPath $Path -ErrorAction Stop).Path
    $base = (Get-FullPathSafe -Path $TempBase).TrimEnd('\') + '\'
    if (-not (Get-FullPathSafe -Path $resolved).StartsWith($base, [StringComparison]::OrdinalIgnoreCase)) { throw 'Self-test cleanup escaped the temp base.' }
    Assert-NoReparseTree -Path $resolved
    Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction Stop
}

$fullPlan = @(New-VerifyStagePlan -Mode Full)
Assert-VerifyLocalTest (@($fullPlan | Where-Object Action -eq 'FullTests').Count -eq 1) 'Full invokes repository tests exactly once'
Assert-VerifyLocalTest (@($fullPlan | Where-Object Name -eq 'PowerShell self-tests').Count -eq 0) 'Full does not duplicate quick self-tests'
$quickPlan = @(New-VerifyStagePlan -Mode Quick)
Assert-VerifyLocalTest (@($quickPlan | Where-Object Action -eq 'SelfTests').Count -eq 1) 'Quick includes self-tests'
Assert-VerifyLocalTest (@($quickPlan | Where-Object Action -eq 'QuickGo').Count -eq 1) 'Quick includes targeted Go tests'
Assert-VerifyLocalTest (@($quickPlan | Where-Object Action -eq 'QuickVitest').Count -eq 1) 'Quick includes frontend Vitest'
$testScriptText = Get-Content -LiteralPath (Join-Path $scriptRoot 'test.ps1') -Raw
Assert-VerifyLocalTest ($testScriptText -match '\[switch\]\$SerialGoPackages') 'test.ps1 exposes the serial package switch'
Assert-VerifyLocalTest ($testScriptText -match '\$goTestArguments \+= ''-p=1''') 'test.ps1 adds the exact serial package argument'
$verifyScriptText = Get-Content -LiteralPath (Join-Path $scriptRoot 'verify-local.ps1') -Raw
Assert-VerifyLocalTest ($verifyScriptText -match "test', '-count=1', '-p=1', './internal/process'") 'Quick uses serial targeted Go package tests'
Assert-VerifyLocalTest ($verifyScriptText.Contains("test.ps1') -SerialGoPackages")) 'Full invokes test.ps1 with serial package scheduling'
$packageScriptText = Get-Content -LiteralPath (Join-Path $scriptRoot 'package.ps1') -Raw
Assert-VerifyLocalTest ($packageScriptText.Contains("artifact-contract.ps1")) 'package.ps1 imports the shared artifact contract helper'
Assert-VerifyLocalTest ($packageScriptText.Contains('Invoke-ArtifactCommandContract')) 'package.ps1 delegates artifact verification to the shared helper'
Assert-VerifyLocalTest (-not $packageScriptText.Contains('ProcessStartInfo')) 'package.ps1 has no direct artifact process runner'

$fakeContext = [pscustomobject]@{ Root = '.' }
$fakeStages = @(
    [pscustomobject]@{ Name = 'fake pass'; DependsOn = @(); WindowsOnly = $false; Action = 'pass' },
    [pscustomobject]@{ Name = 'fake native failure'; DependsOn = @(); WindowsOnly = $false; Action = 'native-failure' },
    [pscustomobject]@{ Name = 'dependent'; DependsOn = @('fake native failure'); WindowsOnly = $false; Action = 'pass' },
    [pscustomobject]@{ Name = 'windows row'; DependsOn = @(); WindowsOnly = $true; Action = 'pass' }
)
$fakeExecutor = {
    param($stage, $context)
    if ($stage.Action -eq 'native-failure') {
        $errorObject = New-Object System.InvalidOperationException 'fixture native exit 7'
        $errorObject.Data['ExitCode'] = 7
        throw $errorObject
    }
}
$fakeResults = @(Invoke-VerifyPlan -Stages $fakeStages -Context $fakeContext -Executor $fakeExecutor)
Assert-VerifyLocalTest (($fakeResults | Where-Object Name -eq 'fake pass').Status -eq 'PASS') 'fake pass is reported as PASS'
Assert-VerifyLocalTest (($fakeResults | Where-Object Name -eq 'fake native failure').Status -eq 'FAIL') 'native nonzero is reported as FAIL'
Assert-VerifyLocalTest (($fakeResults | Where-Object Name -eq 'dependent').Status -eq 'BLOCKED') 'dependent stage is blocked after failure'
Assert-VerifyLocalTest ((Get-VerifyExitCode -Results $fakeResults -CleanupPass:$true) -eq 1) 'FAIL takes precedence over BLOCKED in exit status'
Assert-VerifyLocalTest ((Get-VerifyExitCode -Results @([pscustomobject]@{ Status = 'BLOCKED' }) -CleanupPass:$true) -eq 2) 'blocked-only plan returns exit status 2'
Assert-VerifyLocalTest ((Get-VerifyExitCode -Results @([pscustomobject]@{ Status = 'PASS' }) -CleanupPass:$false) -eq 1) 'cleanup failure returns exit status 1'
$platformExpected = if (Get-VerifyIsWindowsAmd64) { 'PASS' } else { 'BLOCKED' }
Assert-VerifyLocalTest (($fakeResults | Where-Object Name -eq 'windows row').Status -eq $platformExpected) 'Windows-only platform status is explicit'

$realPlatformProbe = (Get-Item Function:\Get-VerifyIsWindowsAmd64).ScriptBlock
try {
    Set-Item Function:\Get-VerifyIsWindowsAmd64 -Value { return $false }
    $forcedNonWindows = @(Invoke-VerifyPlan -Stages $fakeStages -Context $fakeContext -Executor $fakeExecutor)
    Assert-VerifyLocalTest (($forcedNonWindows | Where-Object Name -eq 'windows row').Status -eq 'BLOCKED') 'forced non-Windows blocks Windows-only row'
    $forcedQuick = @(Invoke-VerifyPlan -Stages (New-VerifyStagePlan -Mode Quick) -Context $fakeContext -Executor { param($stage, $context) })
    Assert-VerifyLocalTest (($forcedQuick | Where-Object Name -eq 'PowerShell self-tests').Status -eq 'BLOCKED') 'forced non-Windows blocks Quick self-tests'
    $forcedFull = @(Invoke-VerifyPlan -Stages (New-VerifyStagePlan -Mode Full) -Context $fakeContext -Executor { param($stage, $context) })
    Assert-VerifyLocalTest (($forcedFull | Where-Object Name -eq 'Repository tests').Status -eq 'BLOCKED') 'forced non-Windows blocks Full repository tests'
    Assert-VerifyLocalTest (($forcedFull | Where-Object Name -eq 'Development build').Status -eq 'BLOCKED') 'forced non-Windows blocks development build'
    Assert-VerifyLocalTest ((Get-VerifyExitCode -Results $forcedFull -CleanupPass:$true) -eq 2) 'forced non-Windows Full cannot report PASS'
} finally { Set-Item Function:\Get-VerifyIsWindowsAmd64 -Value $realPlatformProbe }

$realPrerequisiteProbe = (Get-Item Function:\Assert-DevelopmentPrerequisites).ScriptBlock
try {
    Set-Item Function:\Assert-DevelopmentPrerequisites -Value { throw 'fixture prerequisite missing' }
    $prerequisiteBlocked = $false
    try { Invoke-VerifyStageAction -Action 'Prerequisites' -Context ([pscustomobject]@{ Root = '.'; Tooling = $null; Head = $null; InitialHead = $null }) } catch { $prerequisiteBlocked = $_.Exception.Data['VerifyStatus'] -eq 'BLOCKED' }
    Assert-VerifyLocalTest $prerequisiteBlocked 'missing prerequisite is BLOCKED'
} finally { Set-Item Function:\Assert-DevelopmentPrerequisites -Value $realPrerequisiteProbe }

$nativeCaught = $false
try {
    Invoke-VerifyNativeCommand -FilePath 'fixture.exe' -ArgumentList @('--arg') -Invoker {
        param($file, $arguments)
        $errorObject = New-Object System.InvalidOperationException 'fixture exit'
        $errorObject.Data['ExitCode'] = 9
        throw $errorObject
    }
} catch { $nativeCaught = $_.Exception.Data['ExitCode'] -eq 9 }
Assert-VerifyLocalTest $nativeCaught 'native invoker preserves nonzero exit evidence'

$envProbeRoot = Join-Path ([IO.Path]::GetTempPath()) ('roborev-dashboard-verify-selftest-' + [guid]::NewGuid().ToString('N'))
$envTempBase = (Get-FullPathSafe -Path ([IO.Path]::GetTempPath())).TrimEnd('\')
New-Item -ItemType Directory -Path $envProbeRoot -Force | Out-Null
$envProbe = Join-Path $envProbeRoot 'temp'
New-Item -ItemType Directory -Path $envProbe -Force | Out-Null
$trackedEnvironment = @('TEMP', 'TMP', 'GOOS', 'GOARCH', 'CGO_ENABLED', 'RELEASE_E2E_ARTIFACT_PATH', 'LOCAL_VERIFY_ARTIFACT_PATH', 'LOCAL_VERIFY_EXPECTED_VERSION', 'LOCAL_VERIFY_EXPECTED_COMMIT', 'LOCAL_VERIFY_EXPECTED_RELEASE')
$savedEnvironment = @{}
foreach ($name in $trackedEnvironment) { $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process'); [Environment]::SetEnvironmentVariable($name, "verify-old-$name", 'Process') }
try {
    Invoke-VerifyWithEnvironment -TempPath $envProbe -Action {
        Assert-VerifyLocalTest ($env:TEMP -ceq $envProbe -and $env:TMP -ceq $envProbe) 'scoped TEMP/TMP are owned by verifier'
        Assert-VerifyLocalTest ([string]::IsNullOrEmpty($env:GOOS) -and [string]::IsNullOrEmpty($env:GOARCH)) 'inherited cross target is cleared'
        Assert-VerifyLocalTest ([string]::IsNullOrEmpty($env:RELEASE_E2E_ARTIFACT_PATH)) 'inherited release artifact is cleared'
        Assert-VerifyLocalTest ([string]::IsNullOrEmpty($env:LOCAL_VERIFY_ARTIFACT_PATH) -and [string]::IsNullOrEmpty($env:LOCAL_VERIFY_EXPECTED_VERSION) -and [string]::IsNullOrEmpty($env:LOCAL_VERIFY_EXPECTED_COMMIT) -and [string]::IsNullOrEmpty($env:LOCAL_VERIFY_EXPECTED_RELEASE)) 'inherited local contract values are cleared'
    }
    foreach ($name in $trackedEnvironment) { Assert-VerifyLocalTest ([Environment]::GetEnvironmentVariable($name, 'Process') -ceq "verify-old-$name") "environment restored: $name" }
    $restorationCaught = $false
    try { Invoke-VerifyWithEnvironment -TempPath $envProbe -Action { throw 'fixture scoped failure' } } catch { $restorationCaught = $true }
    Assert-VerifyLocalTest $restorationCaught 'scoped failure is surfaced'
    foreach ($name in $trackedEnvironment) { Assert-VerifyLocalTest ([Environment]::GetEnvironmentVariable($name, 'Process') -ceq "verify-old-$name") "environment restored after failure: $name" }
} finally {
    foreach ($name in $trackedEnvironment) {
        if ($null -eq $savedEnvironment[$name]) { [Environment]::SetEnvironmentVariable($name, $null, 'Process') } else { [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], 'Process') }
    }
    Remove-VerifySelfTestTree -Path $envProbeRoot -TempBase $envTempBase
}

$contractEnvironmentNames = @('LOCAL_VERIFY_ARTIFACT_PATH', 'LOCAL_VERIFY_EXPECTED_VERSION', 'LOCAL_VERIFY_EXPECTED_COMMIT', 'LOCAL_VERIFY_EXPECTED_RELEASE')
$contractEnvironmentSaved = @{}
foreach ($name in $contractEnvironmentNames) { $contractEnvironmentSaved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process'); [Environment]::SetEnvironmentVariable($name, "contract-old-$name", 'Process') }
$contractCalls = New-Object 'System.Collections.Generic.List[object]'
$contractPassEvent = '{"Action":"pass","Package":"github.com/froze/roborev-dashboard/internal/releasee2e","Test":"TestArtifactCommandContract"}'
$contractInvoker = {
    param($file, $arguments)
    [void]$contractCalls.Add([pscustomobject]@{ File = $file; Arguments = [string[]]$arguments; Artifact = $env:LOCAL_VERIFY_ARTIFACT_PATH; Version = $env:LOCAL_VERIFY_EXPECTED_VERSION; Commit = $env:LOCAL_VERIFY_EXPECTED_COMMIT; Release = $env:LOCAL_VERIFY_EXPECTED_RELEASE })
    $contractPassEvent
}
try {
    Assert-VerifyExecutableContract -GoPath 'fixture-go.exe' -ArtifactPath (Join-Path ([IO.Path]::GetTempPath()) 'fixture-dashboard.exe') -ExpectedVersion 'dev' -ExpectedCommit ([string]::new('a', 40)) -ReleaseBuild:$false -NativeInvoker $contractInvoker
    Assert-VerifyLocalTest ($contractCalls.Count -eq 1) 'artifact contract invokes exactly one typed Go test'
    Assert-VerifyLocalTest (([string]::Join('|', $contractCalls[0].Arguments)) -ceq 'test|-json|-p=1|-count=1|./internal/releasee2e|-run|^TestArtifactCommandContract$') 'artifact contract uses the exact Go test arguments'
    Assert-VerifyLocalTest ($contractCalls[0].Release -ceq 'false' -and $contractCalls[0].Version -ceq 'dev' -and $contractCalls[0].Commit -ceq ([string]::new('a', 40))) 'artifact contract passes exact metadata environment'
    foreach ($name in $contractEnvironmentNames) { Assert-VerifyLocalTest ([Environment]::GetEnvironmentVariable($name, 'Process') -ceq "contract-old-$name") "artifact contract restores environment: $name" }
    $contractFailureCaught = $false
    try {
        Assert-VerifyExecutableContract -GoPath 'fixture-go.exe' -ArtifactPath (Join-Path ([IO.Path]::GetTempPath()) 'fixture-dashboard.exe') -ExpectedVersion 'dev' -ExpectedCommit ([string]::new('a', 40)) -ReleaseBuild:$false -NativeInvoker {
            param($file, $arguments)
            throw 'fixture contract failure'
        }
    } catch { $contractFailureCaught = $true }
    Assert-VerifyLocalTest $contractFailureCaught 'artifact contract surfaces native failure'
    foreach ($name in $contractEnvironmentNames) { Assert-VerifyLocalTest ([Environment]::GetEnvironmentVariable($name, 'Process') -ceq "contract-old-$name") "artifact contract restores environment after failure: $name" }

    foreach ($negative in @(
        [pscustomobject]@{ Name = 'zero output'; Invoker = { param($file, $arguments) } },
        [pscustomobject]@{ Name = 'package-only pass'; Invoker = { param($file, $arguments) '{"Action":"pass","Package":"github.com/froze/roborev-dashboard/internal/releasee2e"}' } },
        [pscustomobject]@{ Name = 'skipped test'; Invoker = { param($file, $arguments) '{"Action":"skip","Package":"github.com/froze/roborev-dashboard/internal/releasee2e","Test":"TestArtifactCommandContract"}' } },
        [pscustomobject]@{ Name = 'malformed event'; Invoker = { param($file, $arguments) 'not-json' } }
    )) {
        $negativeCaught = $false
        try { Assert-VerifyExecutableContract -GoPath 'fixture-go.exe' -ArtifactPath (Join-Path ([IO.Path]::GetTempPath()) 'fixture-dashboard.exe') -ExpectedVersion 'dev' -ExpectedCommit ([string]::new('a', 40)) -ReleaseBuild:$false -NativeInvoker $negative.Invoker } catch { $negativeCaught = $true }
        Assert-VerifyLocalTest $negativeCaught "artifact contract rejects $($negative.Name)"
        foreach ($name in $contractEnvironmentNames) { Assert-VerifyLocalTest ([Environment]::GetEnvironmentVariable($name, 'Process') -ceq "contract-old-$name") "artifact contract restores environment after $($negative.Name): $name" }
    }
} finally {
    foreach ($name in $contractEnvironmentNames) {
        if ($null -eq $contractEnvironmentSaved[$name]) { [Environment]::SetEnvironmentVariable($name, $null, 'Process') } else { [Environment]::SetEnvironmentVariable($name, $contractEnvironmentSaved[$name], 'Process') }
    }
}

$ownershipRoot = Join-Path ([IO.Path]::GetTempPath()) ('roborev-dashboard-verify-ownership-' + [guid]::NewGuid().ToString('N'))
$ownershipTempBase = (Get-FullPathSafe -Path ([IO.Path]::GetTempPath())).TrimEnd('\')
New-Item -ItemType Directory -Path $ownershipRoot -Force | Out-Null
$ownedCandidate = Join-Path $ownershipRoot 'verify-local'
New-Item -ItemType Directory -Path $ownedCandidate -Force | Out-Null
$marker = Join-Path $ownedCandidate 'keep.txt'
[IO.File]::WriteAllText($marker, 'preserve')
try {
    $ownershipRejected = $false
    try { Assert-VerifyWorkspaceAvailable -Path $ownedCandidate -RepositoryRoot $ownershipRoot | Out-Null } catch { $ownershipRejected = $_.Exception.Data['VerifyStatus'] -eq 'BLOCKED' }
    Assert-VerifyLocalTest $ownershipRejected 'existing verifier workspace is refused'
    Assert-VerifyLocalTest ((Get-Content -LiteralPath $marker -Raw) -ceq 'preserve') 'refused workspace remains untouched'
} finally {
    Remove-VerifySelfTestTree -Path $ownershipRoot -TempBase $ownershipTempBase
}

$initializationRoot = Join-Path ([IO.Path]::GetTempPath()) ('roborev-dashboard-verify-init-' + [guid]::NewGuid().ToString('N'))
$initializationTempBase = (Get-FullPathSafe -Path ([IO.Path]::GetTempPath())).TrimEnd('\')
New-Item -ItemType Directory -Path $initializationRoot -Force | Out-Null
$realNoReparsePoint = (Get-Item Function:\Assert-NoReparsePoint).ScriptBlock
$fixtureWorkspacePath = Join-Path (Join-Path $initializationRoot '.tmp') 'verify-local'
try {
    Set-Item Function:\Assert-NoReparsePoint -Value {
        param([string]$Path)
        if ([IO.Path]::GetFullPath($Path).TrimEnd('\', '/') -ieq [IO.Path]::GetFullPath($fixtureWorkspacePath).TrimEnd('\', '/')) { throw 'fixture cleanup refusal' }
        Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    }
    $initializationFailure = $null
    try { New-VerifyWorkspace -RepositoryRoot $initializationRoot | Out-Null } catch { $initializationFailure = $_.Exception }
    Assert-VerifyLocalTest ($null -ne $initializationFailure) 'workspace initialization failure is surfaced'
    Assert-VerifyLocalTest ($initializationFailure.Message.Contains('verify-local') -and $initializationFailure.Message.Contains('Cleanup failed: fixture cleanup refusal')) 'workspace cleanup failure names owned path and both failures'
} finally {
    Set-Item Function:\Assert-NoReparsePoint -Value $realNoReparsePoint
    Remove-VerifySelfTestTree -Path $initializationRoot -TempBase $initializationTempBase
}

Write-Host 'Local verifier self-tests passed.'
