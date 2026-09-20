<#!
.SYNOPSIS
    Run one local-build child script and preserve its typed exit code.

The parent passes the child path and argument array as PowerShell parameters;
the JSON envelope prevents child switches such as -OutputPath from being
interpreted as parameters of this driver.  This file has no production
failure-injection or bypass switches.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ScriptPath,
    [Parameter(Mandatory = $true)][AllowEmptyString()][string]$ArgumentsJson
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$utilityManifest = Join-Path $PSHOME 'Modules\Microsoft.PowerShell.Utility\Microsoft.PowerShell.Utility.psd1'
if (Test-Path -LiteralPath $utilityManifest -PathType Leaf) {
    Import-Module -Name $utilityManifest -Global -ErrorAction Stop
} else {
    Import-Module -Name Microsoft.PowerShell.Utility -Global -ErrorAction Stop
}
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = $utf8NoBom
$OutputEncoding = $utf8NoBom

try {
    $scriptPathFull = [IO.Path]::GetFullPath($ScriptPath)
    if (-not (Test-Path -LiteralPath $scriptPathFull -PathType Leaf)) { throw "Child script was not found: $scriptPathFull" }
    $decoded = ConvertFrom-Json -InputObject $ArgumentsJson -ErrorAction Stop
    $rawArguments = @($decoded | ForEach-Object { [string]$_ })
    $parameters = @{}
    for ($index = 0; $index -lt $rawArguments.Count; ) {
        $token = $rawArguments[$index]
        if ($token -notmatch '^-[A-Za-z][A-Za-z0-9]*$') { throw "Child argument is not a named parameter: $token" }
        $name = $token.Substring(1)
        if (($index + 1) -lt $rawArguments.Count -and $rawArguments[$index + 1] -notmatch '^-[A-Za-z][A-Za-z0-9]*$') {
            $parameters[$name] = $rawArguments[$index + 1]
            $index += 2
        } else {
            $parameters[$name] = $true
            $index++
        }
    }
    $global:LASTEXITCODE = 0
    & $scriptPathFull @parameters
    $childCode = if ($null -eq $LASTEXITCODE) { 0 } else { [int]$LASTEXITCODE }
    exit $childCode
} catch {
    $childCode = 1
    if ($_.Exception.Data.Contains('ExitCode')) { $childCode = [int]$_.Exception.Data['ExitCode'] }
    try { [Console]::Error.WriteLine($_.Exception.Message) } catch { Write-Host $_.Exception.Message }
    exit $childCode
}
