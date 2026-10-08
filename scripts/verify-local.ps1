[CmdletBinding()]
param(
    [ValidateSet('Quick', 'Full', 'Release')]
    [string]$Mode = 'Full',

    [ValidatePattern('^(0|[1-9][0-9]*)\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$')]
    [string]$Version = '0.0.0-localverify.1'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$script:VerifyScriptRoot = $PSScriptRoot
$script:VerifyArtifactAliases = @(
    'RELEASE_E2E_ARTIFACT_PATH',
    'ROBOREV_DASHBOARD_E2E_ARTIFACT',
    'RELEASE_E2E_ARTIFACT',
    'RELEASE_E2E_EXPECTED_VERSION',
    'LOCAL_VERIFY_ARTIFACT_PATH',
    'LOCAL_VERIFY_EXPECTED_VERSION',
    'LOCAL_VERIFY_EXPECTED_COMMIT',
    'LOCAL_VERIFY_EXPECTED_RELEASE'
)

. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')
. (Join-Path $PSScriptRoot 'release-common.ps1')
. (Join-Path $PSScriptRoot 'artifact-contract.ps1')

function New-VerifyBlockedException {
    param([Parameter(Mandatory = $true)][string]$Message)
    $exception = New-Object System.InvalidOperationException $Message
    $exception.Data['VerifyStatus'] = 'BLOCKED'
    return $exception
}

function Get-VerifyIsWindowsAmd64 {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { return $false }
    try { return ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq [System.Runtime.InteropServices.Architecture]::X64) } catch { return ($env:PROCESSOR_ARCHITECTURE -eq 'AMD64') }
}

function Assert-VerifyWorkspaceAvailable {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$RepositoryRoot
    )

    $root = Get-FullPathSafe -Path $RepositoryRoot
    $candidate = Get-FullPathSafe -Path $Path
    if (-not (Test-PathWithin -Path $candidate -Root $root)) {
        throw (New-VerifyBlockedException "Verifier workspace is outside the repository: $candidate")
    }
    if (Test-Path -LiteralPath $candidate -PathType Any) {
        throw (New-VerifyBlockedException "Refusing to reuse existing verifier workspace: $candidate")
    }
    [void](Assert-SafeExistingParent -Path (Split-Path -Parent $candidate))
    return $candidate
}

function New-VerifyWorkspace {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][string]$RepositoryRoot)

    $root = Get-FullPathSafe -Path $RepositoryRoot
    $tmpParent = Join-Path $root '.tmp'
    if (Test-Path -LiteralPath $tmpParent -PathType Any) {
        [void](Assert-NoReparsePoint -Path $tmpParent)
    } else {
        [void](Assert-SafeExistingParent -Path $tmpParent)
        New-Item -ItemType Directory -Path $tmpParent -ErrorAction Stop | Out-Null
        [void](Assert-NoReparsePoint -Path $tmpParent)
    }
    $workspace = Assert-VerifyWorkspaceAvailable -Path (Join-Path $tmpParent 'verify-local') -RepositoryRoot $root
    $created = $false
    try {
        New-Item -ItemType Directory -Path $workspace -ErrorAction Stop | Out-Null
        $created = $true
        [void](Assert-NoReparsePoint -Path $workspace)
        $temp = Join-Path $workspace 'temp'
        $dev = Join-Path $workspace 'dev'
        $release = Join-Path $workspace 'release'
        foreach ($path in @($temp, $dev, $release)) {
            New-Item -ItemType Directory -Path $path -ErrorAction Stop | Out-Null
            [void](Assert-NoReparsePoint -Path $path)
        }
        return [pscustomobject]([ordered]@{
            Root = $root
            Path = $workspace
            Temp = $temp
            Dev = $dev
            Release = $release
            CreatedUtc = (Get-Item -LiteralPath $workspace -Force).CreationTimeUtc
        })
    } catch {
        $initialException = $_.Exception
        if ($created -and (Test-Path -LiteralPath $workspace -PathType Container)) {
            $cleanupException = $null
            try { Assert-NoReparseTree -Path $workspace; Remove-Item -LiteralPath $workspace -Recurse -Force -ErrorAction Stop } catch { $cleanupException = $_.Exception }
            if ($cleanupException) {
                $message = "Verifier workspace initialization failed for '$workspace': $($initialException.Message) Cleanup failed: $($cleanupException.Message)"
                $wrapped = [System.InvalidOperationException]::new($message, $initialException)
                $wrapped.Data['OwnedWorkspace'] = $workspace
                $wrapped.Data['InitializationException'] = $initialException
                $wrapped.Data['CleanupException'] = $cleanupException
                throw $wrapped
            }
        }
        throw $initialException
    }
}

function Remove-VerifyWorkspace {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][object]$Workspace)

    if (-not (Test-Path -LiteralPath $Workspace.Path -PathType Any)) { return $true }
    try {
        $resolved = (Resolve-Path -LiteralPath $Workspace.Path -ErrorAction Stop).Path
        if (-not (Test-PathWithin -Path $resolved -Root $Workspace.Root)) { throw 'Verifier workspace escaped repository root.' }
        $item = Get-Item -LiteralPath $resolved -Force -ErrorAction Stop
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Verifier workspace became a reparse point.' }
        if ($item.CreationTimeUtc -ne $Workspace.CreatedUtc) { throw 'Verifier workspace identity changed.' }
        Assert-NoReparseTree -Path $resolved
        Remove-Item -LiteralPath $resolved -Recurse -Force -ErrorAction Stop
        return $true
    } catch {
        Write-Host ("CLEANUP | FAIL | $($_.Exception.Message)")
        return $false
    }
}

function Invoke-VerifyWithEnvironment {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$TempPath,
        [Parameter(Mandatory = $true)][scriptblock]$Action
    )

    $names = @('TEMP', 'TMP', 'GOOS', 'GOARCH', 'CGO_ENABLED') + $script:VerifyArtifactAliases
    $saved = @{}
    foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
    try {
        [Environment]::SetEnvironmentVariable('TEMP', $TempPath, 'Process')
        [Environment]::SetEnvironmentVariable('TMP', $TempPath, 'Process')
        foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED') + $script:VerifyArtifactAliases) {
            [Environment]::SetEnvironmentVariable($name, $null, 'Process')
        }
        & $Action
    } finally {
        foreach ($name in $names) {
            if ($null -eq $saved[$name]) {
                [Environment]::SetEnvironmentVariable($name, $null, 'Process')
            } else {
                [Environment]::SetEnvironmentVariable($name, [string]$saved[$name], 'Process')
            }
        }
    }
}

function Invoke-VerifyNativeCommand {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter()][AllowEmptyCollection()][string[]]$ArgumentList = @(),
        [scriptblock]$Invoker
    )
    if ($Invoker) { & $Invoker $FilePath $ArgumentList | Out-Host }
    else { Invoke-NativeCommand -FilePath $FilePath -ArgumentList $ArgumentList | Out-Host }
}

function Assert-VerifyExecutableContract {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$GoPath,
        [Parameter(Mandatory = $true)][string]$ArtifactPath,
        [Parameter(Mandatory = $true)][string]$ExpectedVersion,
        [Parameter(Mandatory = $true)][string]$ExpectedCommit,
        [Parameter(Mandatory = $true)][bool]$ReleaseBuild,
        [scriptblock]$NativeInvoker
    )
    Invoke-ArtifactCommandContract -GoPath $GoPath -ArtifactPath $ArtifactPath -ExpectedVersion $ExpectedVersion -ExpectedCommit $ExpectedCommit -ExpectedRelease:$ReleaseBuild -NativeInvoker $NativeInvoker
}

function Assert-VerifyCleanCheckout {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RepositoryRoot,
        [Parameter(Mandatory = $true)][string]$GitPath
    )
    $root = Get-FullPathSafe -Path $RepositoryRoot
    [void](Assert-SafeExistingParent -Path $root)
    $top = (& $GitPath -C $root rev-parse --show-toplevel 2>$null | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($top) -or (Get-FullPathSafe -Path $top).TrimEnd('\') -ine $root.TrimEnd('\')) { throw (New-VerifyBlockedException 'Current path is not the Git checkout root.') }
    $head = Get-GitCommit -RepositoryRoot $root -GitPath $GitPath
    $status = & $GitPath -C $root status --porcelain=v1 --untracked-files=all 2>$null
    if ($LASTEXITCODE -ne 0) { throw (New-VerifyBlockedException 'Git working tree status could not be read.') }
    if (-not [string]::IsNullOrWhiteSpace(([string]::Join("`n", @($status))))) {
        throw (New-VerifyBlockedException 'Release verification requires a clean Git working tree.')
    }
    return $head
}

function New-VerifyStagePlan {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][ValidateSet('Quick', 'Full', 'Release')][string]$Mode)

    $stages = New-Object System.Collections.Generic.List[object]
    $add = {
        param($Name, $DependsOn, $WindowsOnly, $Action)
        [void]$stages.Add([pscustomobject]([ordered]@{ Name = $Name; DependsOn = @($DependsOn); WindowsOnly = [bool]$WindowsOnly; Action = $Action }))
    }
    & $add 'Prerequisites' @() $false 'Prerequisites'
    if ($Mode -eq 'Quick') {
        & $add 'PowerShell self-tests' @() $true 'SelfTests'
        & $add 'Targeted Go tests' @('Prerequisites') $false 'QuickGo'
        & $add 'Frontend Vitest' @('Prerequisites') $false 'QuickVitest'
    } else {
        & $add 'Repository tests' @('Prerequisites') $true 'FullTests'
        & $add 'Go vet' @('Prerequisites') $false 'Vet'
        & $add 'Development build' @('Prerequisites', 'Repository tests') $true 'DevBuild'
        & $add 'Development executable contract' @('Development build') $true 'DevContract'
    }
    if ($Mode -eq 'Release') {
        & $add 'Windows Go vulnerability scan' @('Prerequisites') $true 'VulnerabilityScan'
        & $add 'Safe clean checkout bound to HEAD' @('Prerequisites') $true 'Checkout'
        & $add 'Reproducible Windows package' @('Safe clean checkout bound to HEAD') $true 'Package'
        & $add 'Packaged executable contract' @('Reproducible Windows package') $true 'ReleaseContract'
        & $add 'Packaged release smoke' @('Packaged executable contract') $true 'ReleaseE2E'
    }
    return $stages.ToArray()
}

function Invoke-VerifyStageAction {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Action,
        [Parameter(Mandatory = $true)][object]$Context
    )
    $root = $Context.Root
    switch ($Action) {
        'Prerequisites' {
            try {
                $Context.Tooling = Assert-DevelopmentPrerequisites -RootPath $root
            } catch {
                throw (New-VerifyBlockedException ("Development prerequisites are unavailable: " + $_.Exception.Message))
            }
            $Context.Head = Get-GitCommit -RepositoryRoot $root -GitPath $Context.Tooling.GitPath
            $Context.InitialHead = $Context.Head
            return
        }
        'SelfTests' {
            foreach ($name in @('prerequisites.tests.ps1', 'package.tests.ps1', 'local-build.tests.ps1', 'installer.tests.ps1', 'verify-local.tests.ps1')) {
                & (Join-Path $script:VerifyScriptRoot $name) | Out-Host
            }
            return
        }
        'QuickGo' {
            Invoke-VerifyNativeCommand -FilePath $Context.Tooling.GoPath -ArgumentList @('test', '-count=1', '-p=1', './internal/process', './internal/pathsafety', './internal/gitx', './internal/roborev'); return
        }
        'QuickVitest' { Invoke-VerifyNativeCommand -FilePath $Context.Tooling.NpmPath -ArgumentList @('--prefix', 'web', 'test', '--', '--run'); return }
        'FullTests' { & (Join-Path $script:VerifyScriptRoot 'test.ps1') -SerialGoPackages | Out-Host; return }
        'Vet' { Invoke-VerifyNativeCommand -FilePath $Context.Tooling.GoPath -ArgumentList @('vet', './...'); return }
        'DevBuild' {
            $Context.DevExecutable = Join-Path $Context.Workspace.Dev 'roborev-dashboard.exe'
            $target = @(& $Context.Tooling.GoPath 'env' 'GOOS' 'GOARCH' 2>$null)
            if ($LASTEXITCODE -ne 0 -or $target.Count -ne 2 -or $target[0].Trim() -cne 'windows' -or $target[1].Trim() -cne 'amd64') { throw (New-VerifyBlockedException 'Native Go target is not windows/amd64.') }
            & (Join-Path $script:VerifyScriptRoot 'build.ps1') -SkipFrontendBuild -OutputPath $Context.DevExecutable | Out-Host
            return
        }
        'DevContract' {
            Assert-VerifyExecutableContract -GoPath $Context.Tooling.GoPath -ArtifactPath $Context.DevExecutable -ExpectedVersion 'dev' -ExpectedCommit $Context.Head -ReleaseBuild:$false; return
        }
        'VulnerabilityScan' {
            $old = @{}; foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) { $old[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
            try {
                $env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
                Invoke-VerifyNativeCommand -FilePath $Context.Tooling.GoPath -ArgumentList @('run', 'golang.org/x/vuln/cmd/govulncheck@v1.8.0', '-show', 'verbose', './...')
            } finally {
                foreach ($name in @('GOOS', 'GOARCH', 'CGO_ENABLED')) { if ($null -eq $old[$name]) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue } else { Set-Item "Env:$name" $old[$name] } }
            }
            return
        }
        'Checkout' {
            $head = Assert-VerifyCleanCheckout -RepositoryRoot $root -GitPath $Context.Tooling.GitPath
            if ($Context.InitialHead -and $head -cne $Context.InitialHead) { throw (New-VerifyBlockedException 'Git HEAD changed during verification.') }
            $Context.Head = $head
            return
        }
        'Package' {
            $before = Get-GitCommit -RepositoryRoot $root -GitPath $Context.Tooling.GitPath
            if ($before -cne $Context.Head) { throw (New-VerifyBlockedException 'Git HEAD changed before packaging.') }
            $Context.ReleaseDirectory = Join-Path $Context.Workspace.Release $Context.Version
            & (Join-Path $script:VerifyScriptRoot 'package.ps1') -Version $Context.Version -OutputDirectory $Context.ReleaseDirectory -VerifyReproducible | Out-Host
            $Context.ReleaseExecutable = Join-Path $Context.ReleaseDirectory $script:ReleaseArtifactName
            $after = Get-GitCommit -RepositoryRoot $root -GitPath $Context.Tooling.GitPath
            if ($after -cne $Context.Head) { throw (New-VerifyBlockedException 'Git HEAD changed during packaging.') }
            return
        }
        'ReleaseContract' { Assert-VerifyExecutableContract -GoPath $Context.Tooling.GoPath -ArtifactPath $Context.ReleaseExecutable -ExpectedVersion $Context.Version -ExpectedCommit $Context.Head -ReleaseBuild:$true; return }
        'ReleaseE2E' {
            & (Join-Path $script:VerifyScriptRoot 'release-e2e.ps1') -ArtifactPath $Context.ReleaseExecutable -Version $Context.Version | Out-Host
            $after = Get-GitCommit -RepositoryRoot $root -GitPath $Context.Tooling.GitPath
            if ($after -cne $Context.Head) { throw (New-VerifyBlockedException 'Git HEAD changed during release smoke.') }
            return
        }
        default { throw "Unknown verifier action: $Action" }
    }
}

function Invoke-VerifyPlan {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][object[]]$Stages,
        [Parameter(Mandatory = $true)][object]$Context,
        [scriptblock]$Executor
    )
    if (-not $Executor) { $Executor = { param($stage, $ctx) Invoke-VerifyStageAction -Action $stage.Action -Context $ctx } }
    $results = [ordered]@{}
    foreach ($stage in $Stages) {
        $blockedDependency = @($stage.DependsOn | Where-Object { $results[$_] -and $results[$_].Status -ne 'PASS' })
        if ($blockedDependency.Count -gt 0) {
            $results[$stage.Name] = [pscustomobject]@{ Name = $stage.Name; Status = 'BLOCKED'; Detail = "Dependency did not pass: $($blockedDependency -join ', ')" }
            continue
        }
        if ($stage.WindowsOnly -and -not (Get-VerifyIsWindowsAmd64)) {
            $results[$stage.Name] = [pscustomobject]@{ Name = $stage.Name; Status = 'BLOCKED'; Detail = 'Windows amd64 is required.' }
            continue
        }
        try {
            & $Executor $stage $Context | Out-Null
            $results[$stage.Name] = [pscustomobject]@{ Name = $stage.Name; Status = 'PASS'; Detail = '' }
        } catch {
            $status = if ($_.Exception.Data['VerifyStatus'] -eq 'BLOCKED') { 'BLOCKED' } else { 'FAIL' }
            $results[$stage.Name] = [pscustomobject]@{ Name = $stage.Name; Status = $status; Detail = $_.Exception.Message }
        }
    }
    return @($results.Values)
}

function Get-VerifyExitCode {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][object[]]$Results,
        [Parameter(Mandatory = $true)][bool]$CleanupPass
    )
    if (-not $CleanupPass -or @($Results | Where-Object Status -eq 'FAIL').Count -gt 0) { return 1 }
    if (@($Results | Where-Object Status -eq 'BLOCKED').Count -gt 0) { return 2 }
    return 0
}

function Invoke-VerifyLocal {
    [CmdletBinding()]
    param(
        [ValidateSet('Quick', 'Full', 'Release')][string]$Mode = 'Full',
        [string]$Version = '0.0.0-localverify.1'
    )
    [void](Assert-StrictSemVer -Value $Version)
    $root = (Resolve-Path -LiteralPath (Join-Path $script:VerifyScriptRoot '..') -ErrorAction Stop).Path
    $workspace = $null
    $cleanupPass = $true
    try {
        $workspace = New-VerifyWorkspace -RepositoryRoot $root
        $context = [pscustomobject]@{ Root = $root; Workspace = $workspace; Version = $Version; Tooling = $null; InitialHead = $null; Head = $null; DevExecutable = $null; ReleaseExecutable = $null; ReleaseDirectory = $null; Results = @() }
        Invoke-VerifyWithEnvironment -TempPath $workspace.Temp -Action {
            Push-Location $root
            try {
                $stages = New-VerifyStagePlan -Mode $Mode
                $results = Invoke-VerifyPlan -Stages $stages -Context $context
                Write-Host ''
                Write-Host "Local verification mode: $Mode"
                foreach ($result in $results) {
                    $detail = if ($result.Detail) { " - $($result.Detail)" } else { '' }
                    Write-Host ("{0} | {1}{2}" -f $result.Name, $result.Status, $detail)
                }
                $context.Results = $results
                if (@($results | Where-Object Status -eq 'FAIL').Count -gt 0) { throw (New-Object System.InvalidOperationException 'Local verification has failed checks.') }
            } finally { Pop-Location }
        }
    } finally {
        if ($workspace) { $cleanupPass = Remove-VerifyWorkspace -Workspace $workspace }
    }
    return [pscustomobject]@{ Results = @($context.Results); CleanupPass = $cleanupPass }
}

if ($MyInvocation.InvocationName -ne '.') {
    $exitCode = 0
    try {
        $outcome = Invoke-VerifyLocal -Mode $Mode -Version $Version
        $exitCode = Get-VerifyExitCode -Results $outcome.Results -CleanupPass:$outcome.CleanupPass
    } catch {
        $isBlocked = $_.Exception.Data['VerifyStatus'] -eq 'BLOCKED'
        Write-Host ("Verifier | {0} | {1}" -f ($(if ($isBlocked) { 'BLOCKED' } else { 'FAIL' }), $_.Exception.Message))
        $exitCode = if ($isBlocked) { 2 } else { 1 }
    }
    exit $exitCode
}
