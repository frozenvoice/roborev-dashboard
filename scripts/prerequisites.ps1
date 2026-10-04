# Development prerequisite definitions and pure/read-only helpers.
# This file intentionally performs no work when dot-sourced. It never looks up,
# installs, or executes RoboRev.

$script:DevelopmentRepositoryRoot = Join-Path $PSScriptRoot '..'

$script:DevelopmentToolDefinitions = @(
    [pscustomobject]@{
        Name = 'Git'
        CommandName = 'git'
        VersionArguments = @('--version')
        MinimumVersion = [version]'2.0.0'
        WingetId = 'Git.Git'
        VersionPattern = '(?m)git version\s+([0-9]+(?:\.[0-9]+){1,3})'
    }
    [pscustomobject]@{
        Name = 'Node.js'
        CommandName = 'node'
        VersionArguments = @('--version')
        MinimumVersion = [version]'22.0.0'
        WingetId = 'OpenJS.NodeJS.LTS'
        VersionPattern = '(?m)^\s*v?([0-9]+(?:\.[0-9]+){1,3})(?:\s|$)'
    }
    [pscustomobject]@{
        Name = 'npm'
        CommandName = 'npm'
        VersionArguments = @('--version')
        MinimumVersion = [version]'0.0.0'
        WingetId = 'OpenJS.NodeJS.LTS'
        VersionPattern = '(?m)^\s*v?([0-9]+(?:\.[0-9]+){1,3})(?:\s|$)'
    }
    [pscustomobject]@{
        Name = 'Go'
        CommandName = 'go'
        VersionArguments = @('version')
        # Filled from go.mod when definitions are requested, never on dot-source.
        MinimumVersion = $null
        WingetId = 'GoLang.Go'
        VersionPattern = '(?m)\bgo([0-9]+(?:\.[0-9]+){1,3})\b'
    }
)

function Get-DevelopmentToolDefinitions {
    [CmdletBinding()]
    param([string]$RootPath = $script:DevelopmentRepositoryRoot)

    $modulePath = Join-Path $RootPath 'go.mod'
    $module = Get-Content -LiteralPath $modulePath -Raw -ErrorAction Stop
    $goVersion = [regex]::Match($module, '(?m)^go[ \t]+([0-9]+\.[0-9]+(?:\.[0-9]+)?)[ \t]*(?://[^\r\n]*)?\r?$')
    if (-not $goVersion.Success) { throw "Missing or invalid Go version directive: $modulePath" }
    $minimumGoVersion = [version]$goVersion.Groups[1].Value
    return @($script:DevelopmentToolDefinitions | ForEach-Object {
        $copy = $_.PSObject.Copy()
        if ($copy.Name -eq 'Go') { $copy.MinimumVersion = $minimumGoVersion }
        $copy
    })
}

function ConvertTo-DevelopmentVersion {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyString()]
        [string]$Output,

        [Parameter(Mandatory = $true)]
        [string]$ToolName
    )

    $definition = @($script:DevelopmentToolDefinitions | Where-Object { $_.Name -eq $ToolName }) | Select-Object -First 1
    if (-not $definition) { return $null }
    $match = [regex]::Match($Output, $definition.VersionPattern)
    if (-not $match.Success) { return $null }
    try {
        return [version]$match.Groups[1].Value
    } catch {
        return $null
    }
}

function Test-DevelopmentVersionMinimum {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [AllowNull()]
        [version]$Version,

        [Parameter(Mandatory = $true)]
        [version]$MinimumVersion
    )

    return ($null -ne $Version -and $Version -ge $MinimumVersion)
}

function Get-DevelopmentInventory {
    [CmdletBinding()]
    param(
        [scriptblock]$CommandResolver,
        [scriptblock]$VersionInvoker,
        [string]$RootPath = $script:DevelopmentRepositoryRoot
    )

    if (-not $CommandResolver) {
        $CommandResolver = {
            param($definition)
            Get-Command -Name $definition.CommandName -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        }
    }
    if (-not $VersionInvoker) {
        $VersionInvoker = {
            param($path, $arguments)
            $output = ''
            $exitCode = 0
            try {
                $output = (& $path @arguments 2>&1 | Out-String)
                $exitCode = $LASTEXITCODE
            } catch {
                $output = $_.Exception.Message
                $exitCode = 1
            }
            [pscustomobject]@{ Output = $output; ExitCode = $exitCode }
        }
    }

    $items = @()
    $root = (Resolve-Path -LiteralPath $RootPath -ErrorAction Stop).Path
    foreach ($definition in Get-DevelopmentToolDefinitions -RootPath $root) {
        $command = & $CommandResolver $definition
        $path = $null
        if ($command) {
            $path = if ($command.Path) { $command.Path } elseif ($command.Source) { $command.Source } else { [string]$command }
            try { $path = [System.IO.Path]::GetFullPath($path) } catch { $path = $null }
        }

        $output = ''
        $exitCode = $null
        $version = $null
        $errorMessage = ''
        if (-not $path) {
            $errorMessage = "$($definition.Name) was not found on PATH."
        } else {
            try {
                $versionArguments = @($definition.VersionArguments)
                # Go's launcher may select a patched compiler from go.mod. Resolve
                # it in this checkout even when the caller runs from another cwd.
                if ($definition.Name -eq 'Go') { $versionArguments = @('-C', $root) + $versionArguments }
                $invocation = & $VersionInvoker $path $versionArguments
                $output = [string]$invocation.Output
                $exitCode = [int]$invocation.ExitCode
                if ($exitCode -ne 0) {
                    $errorMessage = "$($definition.Name) version command exited with code $exitCode."
                } else {
                    $version = ConvertTo-DevelopmentVersion -Output $output -ToolName $definition.Name
                    if ($null -eq $version) {
                        $errorMessage = "$($definition.Name) returned a malformed version."
                    }
                }
            } catch {
                $exitCode = 1
                $errorMessage = "$($definition.Name) version command failed: $($_.Exception.Message)"
            }
        }

        $meetsMinimum = Test-DevelopmentVersionMinimum -Version $version -MinimumVersion $definition.MinimumVersion
        $action = 'None'
        if (-not $path -or $null -eq $version -or [int]$exitCode -ne 0) {
            $action = 'Install'
        } elseif (-not $meetsMinimum) {
            $action = 'Upgrade'
            $errorMessage = "$($definition.Name) $version is below required $($definition.MinimumVersion)."
        }
        $items += [pscustomobject]@{
            Name = $definition.Name
            CommandName = $definition.CommandName
            Path = $path
            Version = $version
            MinimumVersion = $definition.MinimumVersion
            WingetId = $definition.WingetId
            ExitCode = $exitCode
            MeetsMinimum = $meetsMinimum
            Action = $action
            Error = $errorMessage
        }
    }
    return $items
}

function Get-DevelopmentInstallPlan {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [object[]]$Inventory
    )

    $byId = [ordered]@{}
    foreach ($item in @($Inventory | Where-Object { $_.Action -eq 'Install' -or $_.Action -eq 'Upgrade' })) {
        if (-not $byId.Contains($item.WingetId) -or ($item.Action -eq 'Install' -and $byId[$item.WingetId].Action -eq 'Upgrade')) {
            $byId[$item.WingetId] = [pscustomobject]@{ Name = $item.Name; WingetId = $item.WingetId; Action = $item.Action }
        }
    }
    return @($byId.Values)
}

function Get-DevelopmentWingetCommandSpec {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [ValidateSet('Show', 'Install', 'Upgrade')]
        [string]$Operation,

        [Parameter(Mandatory = $true)]
        [string]$WingetId
    )

    $allowedIds = @($script:DevelopmentToolDefinitions | Select-Object -ExpandProperty WingetId -Unique)
    if ($allowedIds -notcontains $WingetId) {
        throw "Refusing non-allowlisted winget package ID: $WingetId"
    }
    $verb = switch ($Operation) {
        'Show' { 'show'; break }
        'Install' { 'install'; break }
        'Upgrade' { 'upgrade'; break }
    }
    $arguments = @($verb, '--id', $WingetId, '--exact', '--source', 'winget', '--disable-interactivity')
    if ($Operation -eq 'Show') {
        $arguments += '--accept-source-agreements'
    } else {
        $arguments += @('--accept-source-agreements', '--accept-package-agreements')
    }
    return [pscustomobject]@{
        Operation = $Operation
        WingetId = $WingetId
        ArgumentList = [string[]]$arguments
    }
}

function Invoke-DevelopmentInstallPlan {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [object[]]$Plan,

        [scriptblock]$WingetResolver,
        [scriptblock]$NativeInvoker
    )

    if ($null -eq $Plan -or $Plan.Count -eq 0) { return }
    if (-not $WingetResolver) {
        $WingetResolver = {
            Get-Command winget -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        }
    }
    if (-not $NativeInvoker) {
        $NativeInvoker = {
            param($filePath, [string[]]$argumentList)
            & $filePath @argumentList
            if ($LASTEXITCODE -ne 0) {
                throw "$filePath exited with code $LASTEXITCODE."
            }
        }
    }
    $command = & $WingetResolver
    if (-not $command) {
        throw 'winget is required to install missing or outdated development tools. Install App Installer, then rerun .\scripts\install.ps1.'
    }
    $path = if ($command.Path) { $command.Path } elseif ($command.Source) { $command.Source } else { [string]$command }
    try { $path = [System.IO.Path]::GetFullPath($path) } catch { throw 'winget was found but its executable path is invalid.' }

    foreach ($item in @($Plan)) {
        $show = Get-DevelopmentWingetCommandSpec -Operation Show -WingetId $item.WingetId
        & $NativeInvoker $path $show.ArgumentList
        $action = Get-DevelopmentWingetCommandSpec -Operation $item.Action -WingetId $item.WingetId
        & $NativeInvoker $path $action.ArgumentList
    }
}

function Merge-DevelopmentProcessPath {
    [CmdletBinding()]
    param(
        [string]$MachinePath,
        [string]$UserPath,
        [string]$CurrentPath
    )

    if ($null -eq $MachinePath) { $MachinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine') }
    if ($null -eq $UserPath) { $UserPath = [Environment]::GetEnvironmentVariable('Path', 'User') }
    if ($null -eq $CurrentPath) { $CurrentPath = $env:Path }
    $seen = @{}
    $result = New-Object System.Collections.Generic.List[string]
    foreach ($source in @($MachinePath, $UserPath, $CurrentPath)) {
        foreach ($entry in ([string]$source -split ';')) {
            $value = $entry.Trim()
            if ([string]::IsNullOrWhiteSpace($value)) { continue }
            $key = $value.ToLowerInvariant()
            if (-not $seen.ContainsKey($key)) {
                $seen[$key] = $true
                [void]$result.Add($value)
            }
        }
    }
    return [string]::Join(';', $result.ToArray())
}

function Test-FrontendShimReadiness {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$WebRoot
    )

    $missing = @()
    foreach ($shim in @('tsc', 'vite', 'vitest')) {
        $found = $false
        foreach ($candidate in @((Join-Path $WebRoot "node_modules\.bin\$shim.cmd"), (Join-Path $WebRoot "node_modules\.bin\$shim"))) {
            if (Test-Path -LiteralPath $candidate -PathType Leaf) { $found = $true; break }
        }
        if (-not $found) { $missing += $shim }
    }
    return [pscustomobject]@{ Ready = ($missing.Count -eq 0); Missing = @($missing) }
}

function Assert-DevelopmentPrerequisites {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$RootPath,

        [object[]]$Inventory,

        [switch]$SkipFrontendShims
    )

    try {
        $root = (Resolve-Path -LiteralPath $RootPath -ErrorAction Stop).Path
    } catch {
        throw "Development checkout root does not exist: $RootPath"
    }
    if ($null -eq $Inventory) { $Inventory = @(Get-DevelopmentInventory -RootPath $root) }
    $issues = New-Object System.Collections.Generic.List[string]
    $byName = @{}
    foreach ($item in @($Inventory)) { $byName[$item.Name] = $item }
    foreach ($definition in Get-DevelopmentToolDefinitions -RootPath $root) {
        if (-not $byName.ContainsKey($definition.Name)) {
            [void]$issues.Add("$($definition.Name) inventory record is missing.")
            continue
        }
        $item = $byName[$definition.Name]
        if (-not $item.MeetsMinimum -or -not (Test-DevelopmentVersionMinimum -Version $item.Version -MinimumVersion $definition.MinimumVersion)) {
            $detail = if ($item.Error) { $item.Error } elseif ($null -ne $item.Version) { "$($item.Name) $($item.Version) is below required $($definition.MinimumVersion)." } else { "$($item.Name) is missing or invalid." }
            [void]$issues.Add($detail)
        }
    }
    $frontend = Test-FrontendShimReadiness -WebRoot (Join-Path $root 'web')
    if (-not $SkipFrontendShims -and -not $frontend.Ready) {
        [void]$issues.Add("Frontend dependencies are missing ($([string]::Join(', ', $frontend.Missing))). Run .\scripts\install.ps1.")
    }
    if ($issues.Count -gt 0) {
        [void]$issues.Add('Development prerequisites are not ready. Run .\scripts\install.ps1 to bootstrap this checkout.')
        throw ([string]::Join([Environment]::NewLine, $issues.ToArray()))
    }
    return [pscustomobject]@{
        RootPath = $root
        GitPath = $byName['Git'].Path
        NodePath = $byName['Node.js'].Path
        NpmPath = $byName['npm'].Path
        GoPath = $byName['Go'].Path
        Inventory = @($Inventory)
        Frontend = $frontend
    }
}
