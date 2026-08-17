$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'native-command.ps1')
$goCommand = Get-Command go -ErrorAction SilentlyContinue
if ($goCommand) {
    $goPath = $goCommand.Source
} else {
    $goPath = 'C:\Program Files\Go\bin\go.exe'
    if (-not (Test-Path -LiteralPath $goPath)) { throw 'Go is required. Install it from https://go.dev/dl/' }
}

Push-Location $root
try {
    Invoke-NativeCommand -FilePath 'npm' -ArgumentList @('--prefix', 'web', 'run', 'build')
    Invoke-NativeCommand -FilePath $goPath -ArgumentList (@('run', './cmd/dashboard') + $args)
} finally {
    Pop-Location
}
