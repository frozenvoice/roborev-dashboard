# Shared, side-effect-light release artifact contract runner.
# Callers must dot-source native-command.ps1 before using this function.
Set-StrictMode -Version Latest

function Invoke-ArtifactCommandContract {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$GoPath,
        [Parameter(Mandatory = $true)][string]$ArtifactPath,
        [Parameter(Mandatory = $true)][string]$ExpectedVersion,
        [Parameter(Mandatory = $true)][string]$ExpectedCommit,
        [Parameter(Mandatory = $true)][bool]$ExpectedRelease,
        [scriptblock]$NativeInvoker
    )

    $names = @(
        'LOCAL_VERIFY_ARTIFACT_PATH',
        'LOCAL_VERIFY_EXPECTED_VERSION',
        'LOCAL_VERIFY_EXPECTED_COMMIT',
        'LOCAL_VERIFY_EXPECTED_RELEASE'
    )
    $saved = @{}
    foreach ($name in $names) { $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
    try {
        $env:LOCAL_VERIFY_ARTIFACT_PATH = [IO.Path]::GetFullPath($ArtifactPath)
        $env:LOCAL_VERIFY_EXPECTED_VERSION = $ExpectedVersion
        $env:LOCAL_VERIFY_EXPECTED_COMMIT = $ExpectedCommit
        $env:LOCAL_VERIFY_EXPECTED_RELEASE = if ($ExpectedRelease) { 'true' } else { 'false' }
        $arguments = @('test', '-json', '-p=1', '-count=1', './internal/releasee2e', '-run', '^TestArtifactCommandContract$')
        $lines = New-Object System.Collections.Generic.List[string]
        $characters = 0
        if ($NativeInvoker) {
            $rawOutput = @(& $NativeInvoker $GoPath $arguments)
            $exitCode = 0
        } else {
            $rawOutput = @(& $GoPath @arguments 2>&1)
            $exitCode = if ($null -eq $LASTEXITCODE) { 0 } else { [int]$LASTEXITCODE }
        }
        foreach ($item in $rawOutput) {
            $line = [string]$item
            $characters += $line.Length + 1
            if ($characters -gt (128 * 1024)) { throw 'Artifact contract output exceeded the 128 KiB limit.' }
            [void]$lines.Add($line)
            Write-Host $line
        }
        if ($exitCode -ne 0) {
            $exception = New-Object System.InvalidOperationException "Artifact contract Go test failed with exit code $exitCode."
            $exception.Data['ExitCode'] = $exitCode
            $exception.Data['FilePath'] = $GoPath
            throw $exception
        }
        $observedPass = $false
        foreach ($line in $lines) {
            if ([string]::IsNullOrWhiteSpace($line)) { continue }
            try { $event = $line | ConvertFrom-Json -ErrorAction Stop } catch { throw 'Artifact contract returned malformed Go JSON events.' }
            $action = $event.PSObject.Properties['Action']
            $package = $event.PSObject.Properties['Package']
            $test = $event.PSObject.Properties['Test']
            if ($null -ne $action -and $null -ne $package -and $null -ne $test -and
                [string]$action.Value -ceq 'pass' -and [string]$package.Value -ceq 'github.com/froze/roborev-dashboard/internal/releasee2e' -and
                [string]$test.Value -ceq 'TestArtifactCommandContract') {
                $observedPass = $true
            }
        }
        if (-not $observedPass) { throw 'Artifact contract did not report a passing TestArtifactCommandContract event.' }
    } finally {
        foreach ($name in $names) {
            if ($null -eq $saved[$name]) { [Environment]::SetEnvironmentVariable($name, $null, 'Process') }
            else { [Environment]::SetEnvironmentVariable($name, [string]$saved[$name], 'Process') }
        }
    }
}
