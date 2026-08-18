$ErrorActionPreference = 'Stop'

if ($env:OS -ne 'Windows_NT') {
    throw 'Development bootstrap is supported on Windows only.'
}

$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..') -ErrorAction Stop).Path
. (Join-Path $PSScriptRoot 'prerequisites.ps1')
. (Join-Path $PSScriptRoot 'native-command.ps1')

$inventory = @(Get-DevelopmentInventory)
$plan = @(Get-DevelopmentInstallPlan -Inventory $inventory)
$nativeInvoker = {
    param($filePath, [string[]]$argumentList)
    Invoke-NativeCommand -FilePath $filePath -ArgumentList $argumentList
}
Invoke-DevelopmentInstallPlan -Plan $plan -NativeInvoker $nativeInvoker

# Refresh only this process. Machine entries precede user entries, with the
# pre-existing process PATH retained as the final source and deduplicated.
$env:Path = Merge-DevelopmentProcessPath -MachinePath ([Environment]::GetEnvironmentVariable('Path', 'Machine')) -UserPath ([Environment]::GetEnvironmentVariable('Path', 'User')) -CurrentPath $env:Path
try {
    $tooling = Assert-DevelopmentPrerequisites -RootPath $root -SkipFrontendShims
} catch {
    if ($plan.Count -gt 0) {
        throw "winget completed, but installed development tools are not visible in this PowerShell process. Open a new PowerShell and rerun .\scripts\install.ps1.`nUnderlying prerequisite details: $($_.Exception.Message)"
    }
    throw
}

$lockPath = Join-Path $root 'web\package-lock.json'
if (-not (Test-Path -LiteralPath $lockPath -PathType Leaf)) {
    throw "Frontend lockfile is missing: $lockPath. Restore web/package-lock.json before running npm ci."
}

Push-Location $root
try {
    Invoke-NativeCommand -FilePath $tooling.NpmPath -ArgumentList @('--prefix', 'web', 'ci')
} finally {
    Pop-Location
}

$final = Assert-DevelopmentPrerequisites -RootPath $root
Write-Host "Development prerequisites ready. npm: $($final.NpmPath); go: $($final.GoPath)"
