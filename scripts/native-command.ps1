function Invoke-NativeCommand {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,

        [Parameter()]
        [string[]]$ArgumentList = @()
    )

    # The call operator receives an argument array, so paths and user-supplied
    # values are never rebuilt into a shell command line.  Preserve the code in
    # Exception.Data for wrappers that need to propagate the exact child code.
    & $FilePath @ArgumentList
    $exitCode = if ($null -eq $LASTEXITCODE) { 0 } else { [int]$LASTEXITCODE }
    if ($exitCode -ne 0) {
        $exception = New-Object System.InvalidOperationException ("Native command '$FilePath' failed with exit code $exitCode.")
        $exception.Data['ExitCode'] = $exitCode
        $exception.Data['FilePath'] = $FilePath
        throw $exception
    }
}
