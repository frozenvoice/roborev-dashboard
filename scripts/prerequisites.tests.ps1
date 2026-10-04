$ErrorActionPreference = 'Stop'
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
. (Join-Path $scriptRoot 'prerequisites.ps1')

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw "Assertion failed: $Message" }
}
function Assert-Equal {
    param($Actual, $Expected, [string]$Message)
    if ($Actual -ne $Expected) { throw "Assertion failed: $Message (actual '$Actual', expected '$Expected')" }
}

Assert-Equal (ConvertTo-DevelopmentVersion -ToolName 'Node.js' -Output 'v22.14.0') ([version]'22.14.0') 'node version parsing'
Assert-Equal (ConvertTo-DevelopmentVersion -ToolName 'Go' -Output 'go version go1.26.8 windows/amd64') ([version]'1.26.8') 'go version parsing'
Assert-Equal (ConvertTo-DevelopmentVersion -ToolName 'Git' -Output 'git version 2.47.1.windows.1') ([version]'2.47.1') 'git version parsing'
Assert-True ((Test-DevelopmentVersionMinimum -Version ([version]'22.0.0') -MinimumVersion ([version]'22.0.0')) -eq $true) 'minimum threshold equality'
Assert-True ((Test-DevelopmentVersionMinimum -Version ([version]'21.9.9') -MinimumVersion ([version]'22.0.0')) -eq $false) 'minimum threshold rejection'
Assert-True ($null -eq (ConvertTo-DevelopmentVersion -ToolName 'Node.js' -Output 'not a version')) 'malformed version rejection'

$definitions = @(Get-DevelopmentToolDefinitions)
Assert-Equal $definitions.Count 4 'required tool definition count'
Assert-Equal (@($definitions | Where-Object { $_.WingetId -eq 'Git.Git' }).Count) 1 'git allowlist ID'
Assert-Equal (@($definitions | Where-Object { $_.WingetId -eq 'OpenJS.NodeJS.LTS' }).Count) 2 'node/npm allowlist ID'
Assert-Equal (@($definitions | Where-Object { $_.WingetId -eq 'GoLang.Go' }).Count) 1 'go allowlist ID'

$resolver = {
    param($definition)
    if ($definition.Name -eq 'Git') { [pscustomobject]@{ Path = 'C:\Tools\git.exe' } }
    elseif ($definition.Name -eq 'Node.js') { [pscustomobject]@{ Path = 'C:\Tools\node.exe' } }
    elseif ($definition.Name -eq 'npm') { [pscustomobject]@{ Path = 'C:\Tools\npm.cmd' } }
    else { $null }
}
$invoker = {
    param($path, $arguments)
    if ($path -like '*git.exe') { [pscustomobject]@{ Output = 'git version 2.47.1'; ExitCode = 0 } }
    elseif ($path -like '*node.exe') { [pscustomobject]@{ Output = 'v22.1.0'; ExitCode = 0 } }
    elseif ($path -like '*npm.cmd') { [pscustomobject]@{ Output = '10.2.0'; ExitCode = 0 } }
    else { [pscustomobject]@{ Output = 'bad'; ExitCode = 7 } }
}
$inventory = @(Get-DevelopmentInventory -CommandResolver $resolver -VersionInvoker $invoker)
$plan = @(Get-DevelopmentInstallPlan -Inventory $inventory)
Assert-Equal (($plan | Where-Object { $_.Name -eq 'Go' }).Action) 'Install' 'missing tool install plan'
Assert-Equal (@($plan | Where-Object { $_.Name -eq 'Node.js' }).Count) 0 'sufficient tool has no plan'
Assert-Equal (@($plan | Where-Object { $_.Name -eq 'npm' }).Count) 0 'sufficient npm has no plan'
Assert-Equal (($plan | Where-Object { $_.Name -eq 'Go' }).WingetId) 'GoLang.Go' 'plan uses allowlisted ID'
$allResolver = {
    param($definition)
    [pscustomobject]@{ Path = "C:\Tools\$($definition.CommandName).exe" }
}
$malformedInvoker = {
    param($path, $arguments)
    if ($path -like '*go.exe') { [pscustomobject]@{ Output = 'not a Go version'; ExitCode = 0 } }
    elseif ($path -like '*node.exe') { [pscustomobject]@{ Output = 'v22.1.0'; ExitCode = 0 } }
    elseif ($path -like '*npm.exe') { [pscustomobject]@{ Output = '10.2.0'; ExitCode = 0 } }
    else { [pscustomobject]@{ Output = 'git version 2.47.1'; ExitCode = 17 } }
}
$validatedInventory = @(Get-DevelopmentInventory -CommandResolver $allResolver -VersionInvoker $malformedInvoker)
$validatedGo = $validatedInventory | Where-Object { $_.Name -eq 'Go' }
$validatedGit = $validatedInventory | Where-Object { $_.Name -eq 'Git' }
Assert-Equal $validatedGo.Action 'Install' 'malformed version is an install action'
Assert-True ($validatedGo.Error -like '*malformed version*') 'malformed invocation is actionable'
Assert-Equal $validatedGit.Action 'Install' 'nonzero version invocation is an install action'
Assert-True ($validatedGit.Error -like '*exited with code 17*') 'nonzero invocation is actionable'
Assert-Equal (@(Get-DevelopmentInstallPlan -Inventory @()).Count) 0 'empty install plan'
$dedupPlan = @(Get-DevelopmentInstallPlan -Inventory @(
    [pscustomobject]@{ Name = 'Node.js'; WingetId = 'OpenJS.NodeJS.LTS'; Action = 'Upgrade' }
    [pscustomobject]@{ Name = 'npm'; WingetId = 'OpenJS.NodeJS.LTS'; Action = 'Install' }
))
Assert-Equal $dedupPlan.Count 1 'duplicate package IDs are collapsed'
Assert-Equal $dedupPlan[0].Action 'Install' 'install takes precedence over upgrade for one package'

foreach ($operation in @('Show', 'Install', 'Upgrade')) {
    $spec = Get-DevelopmentWingetCommandSpec -Operation $operation -WingetId 'GoLang.Go'
    Assert-True ($spec.ArgumentList -contains '--id' -and $spec.ArgumentList -contains 'GoLang.Go') "$operation uses exact allowlisted ID"
    Assert-True ($spec.ArgumentList -contains '--exact' -and $spec.ArgumentList -contains '--source' -and $spec.ArgumentList -contains 'winget') "$operation pins winget source"
    Assert-True ($spec.ArgumentList -contains '--disable-interactivity' -and $spec.ArgumentList -contains '--accept-source-agreements') "$operation is noninteractive with source agreement"
    if ($operation -ne 'Show') { Assert-True ($spec.ArgumentList -contains '--accept-package-agreements') "$operation accepts package agreements" }
}
$wingetCalls = New-Object System.Collections.Generic.List[object]
$resolverCalls = New-Object System.Collections.Generic.List[int]
$fakeWingetResolver = {
    [void]$resolverCalls.Add(1)
    [pscustomobject]@{ Path = 'C:\Tools\winget.exe' }
}.GetNewClosure()
$fakeNativeInvoker = {
    param($path, [string[]]$argumentList)
    [void]$wingetCalls.Add([pscustomobject]@{ Path = $path; Arguments = @($argumentList) })
}.GetNewClosure()
$fakePlan = @(
    [pscustomobject]@{ Name = 'Node.js'; WingetId = 'OpenJS.NodeJS.LTS'; Action = 'Install' }
    [pscustomobject]@{ Name = 'Go'; WingetId = 'GoLang.Go'; Action = 'Upgrade' }
)
Invoke-DevelopmentInstallPlan -Plan $fakePlan -WingetResolver $fakeWingetResolver -NativeInvoker $fakeNativeInvoker
Assert-Equal $resolverCalls.Count 1 'winget resolver is called once for a nonempty plan'
Assert-Equal $wingetCalls.Count 4 'each package is shown before its action'
Assert-True (($wingetCalls[0].Arguments -contains 'OpenJS.NodeJS.LTS') -and ($wingetCalls[0].Arguments[0] -eq 'show')) 'install package show argv'
Assert-True (($wingetCalls[1].Arguments -contains 'OpenJS.NodeJS.LTS') -and ($wingetCalls[1].Arguments[0] -eq 'install')) 'install package install argv'
Assert-True (($wingetCalls[2].Arguments -contains 'GoLang.Go') -and ($wingetCalls[2].Arguments[0] -eq 'show')) 'upgrade package show argv'
Assert-True (($wingetCalls[3].Arguments -contains 'GoLang.Go') -and ($wingetCalls[3].Arguments[0] -eq 'upgrade')) 'upgrade package upgrade argv'
$emptyPlanSucceeded = $true
try {
    Invoke-DevelopmentInstallPlan -Plan @() -WingetResolver { throw 'winget must not be resolved for an empty plan' } -NativeInvoker { throw 'native must not be invoked for an empty plan' }
} catch { $emptyPlanSucceeded = $false }
Assert-True $emptyPlanSucceeded 'empty plan does not resolve or invoke winget'
$nonzeroPropagated = $false
try {
    Invoke-DevelopmentInstallPlan -Plan @([pscustomobject]@{ Name = 'Go'; WingetId = 'GoLang.Go'; Action = 'Install' }) -WingetResolver { [pscustomobject]@{ Path = 'C:\Tools\winget.exe' } } -NativeInvoker { throw 'native exit code 7' }
} catch { $nonzeroPropagated = $_.Exception.Message -like '*native exit code 7*' }
Assert-True $nonzeroPropagated 'nonzero native failure propagates'

$merged = Merge-DevelopmentProcessPath -MachinePath 'C:\Machine;C:\Shared' -UserPath 'c:\user;C:\shared' -CurrentPath 'C:\Current;c:\machine'
Assert-Equal $merged 'C:\Machine;C:\Shared;c:\user;C:\Current' 'path order and case-insensitive deduplication'

$tempRoot = Join-Path ([IO.Path]::GetTempPath()) ("roborev-dashboard-prereq-" + $PID)
try {
    New-Item -ItemType Directory -Force -Path (Join-Path $tempRoot 'web\node_modules\.bin') | Out-Null
    Set-Content -LiteralPath (Join-Path $tempRoot 'go.mod') -Value "module example.test/prerequisites`ngo 1.26.8" -Encoding UTF8
    $fixtureDefinitions = @(Get-DevelopmentToolDefinitions -RootPath $tempRoot)
    Assert-Equal (($fixtureDefinitions | Where-Object { $_.Name -eq 'Go' }).MinimumVersion) ([version]'1.26.8') 'Go requirement comes from checkout go.mod'
    $versionCalls = New-Object System.Collections.Generic.List[object]
    $fixtureInvoker = {
        param($path, $arguments)
        [void]$versionCalls.Add([pscustomobject]@{ Path = $path; Arguments = @($arguments) })
        if ($path -like '*go.exe') { [pscustomobject]@{ Output = 'go version go1.26.5 windows/amd64'; ExitCode = 0 } }
        elseif ($path -like '*node.exe') { [pscustomobject]@{ Output = 'v22.1.0'; ExitCode = 0 } }
        elseif ($path -like '*npm.exe') { [pscustomobject]@{ Output = '10.2.0'; ExitCode = 0 } }
        else { [pscustomobject]@{ Output = 'git version 2.47.1'; ExitCode = 0 } }
    }.GetNewClosure()
    Push-Location $scriptRoot
    try {
        $fixtureInventory = @(Get-DevelopmentInventory -RootPath $tempRoot -CommandResolver $allResolver -VersionInvoker $fixtureInvoker)
    } finally { Pop-Location }
    $fixtureGo = $fixtureInventory | Where-Object { $_.Name -eq 'Go' }
    Assert-Equal $fixtureGo.Action 'Upgrade' 'unpatched effective Go compiler requires upgrade'
    Assert-True ($fixtureGo.Error -like '*1.26.8*') 'upgrade reports module patch requirement'
    $goCall = $versionCalls | Where-Object { $_.Path -like '*go.exe' }
    Assert-Equal ([string]::Join('|', $goCall.Arguments)) "-C|$tempRoot|version" 'Go version resolves in checkout independently of caller location'

    Set-Content -LiteralPath (Join-Path $tempRoot 'go.mod') -Value 'module example.test/prerequisites' -Encoding UTF8
    $invalidModuleRejected = $false
    try { Get-DevelopmentToolDefinitions -RootPath $tempRoot | Out-Null } catch { $invalidModuleRejected = $_.Exception.Message -like '*invalid Go version directive*' }
    Assert-True $invalidModuleRejected 'missing module Go version fails closed'
    Set-Content -LiteralPath (Join-Path $tempRoot 'go.mod') -Value "module example.test/prerequisites`ngo 1.26.8" -Encoding UTF8
    $ready = Test-FrontendShimReadiness -WebRoot (Join-Path $tempRoot 'web')
    Assert-True (-not $ready.Ready -and $ready.Missing.Count -eq 3) 'frontend missing shim detection'
    foreach ($shim in @('tsc', 'vite', 'vitest')) { New-Item -ItemType File -Force -Path (Join-Path $tempRoot "web\node_modules\.bin\$shim.cmd") | Out-Null }
    $ready = Test-FrontendShimReadiness -WebRoot (Join-Path $tempRoot 'web')
    Assert-True $ready.Ready 'frontend shim readiness'

    $goodInventory = @($inventory | ForEach-Object {
        $copy = $_.PSObject.Copy()
        if ($copy.Name -eq 'Go') { $copy.Path = 'C:\Tools\go.exe'; $copy.Version = [version]'1.26.8'; $copy.MeetsMinimum = $true; $copy.Error = '' }
        $copy
    })
    $resolved = Assert-DevelopmentPrerequisites -RootPath $tempRoot -Inventory $goodInventory
    Assert-Equal $resolved.NpmPath 'C:\Tools\npm.cmd' 'absolute npm path resolution'
    Assert-Equal $resolved.GoPath 'C:\Tools\go.exe' 'absolute go path resolution'

    $staleInventory = @($goodInventory | ForEach-Object {
        $copy = $_.PSObject.Copy()
        if ($copy.Name -eq 'Go') { $copy.Version = [version]'1.26.5' }
        $copy
    })
    $staleInventoryRejected = $false
    try { Assert-DevelopmentPrerequisites -RootPath $tempRoot -Inventory $staleInventory | Out-Null } catch { $staleInventoryRejected = $_.Exception.Message -like '*Go 1.26.5 is below required 1.26.8*' }
    Assert-True $staleInventoryRejected 'cached minimum flag cannot override current module requirement'

    $badInventory = @($goodInventory | ForEach-Object {
        $copy = $_.PSObject.Copy()
        if ($copy.Name -eq 'Go') { $copy.MeetsMinimum = $false; $copy.Error = 'Go 1.26.8+ is required.' }
        $copy
    })
    $actionable = $false
    try { Assert-DevelopmentPrerequisites -RootPath $tempRoot -Inventory $badInventory | Out-Null } catch { $actionable = $_.Exception.Message -like '*Go 1.26.8+ is required*' -and $_.Exception.Message -like '*scripts\install.ps1*' }
    Assert-True $actionable 'actionable aggregate prerequisite error'
} finally {
    if (Test-Path -LiteralPath $tempRoot) { Remove-Item -LiteralPath $tempRoot -Recurse -Force }
}

Write-Host 'Prerequisite definition tests passed.'
