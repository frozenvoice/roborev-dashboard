# Creates a disposable Windows Sandbox test bundle. It never starts an installer
# on the host and does not enable Windows features or change security policy.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$SetupPath,
    [Parameter(Mandatory = $true)][string]$ExecutablePath
)

$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'release-common.ps1')
$root = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
foreach ($inputPath in @($SetupPath, $ExecutablePath)) {
    if (-not (Test-Path -LiteralPath $inputPath -PathType Leaf)) { throw "Missing test artifact: $inputPath" }
    [void](Assert-SafeExistingParent -Path $inputPath)
    [void](Assert-NoReparsePoint -Path $inputPath)
}
$bundle = Join-Path $root ('dist\installer-sandbox\' + [guid]::NewGuid().ToString('N'))
[void](Assert-SafeExistingParent -Path $bundle)
$inputDirectory = Join-Path $bundle 'input'
$outputDirectory = Join-Path $bundle 'results'
New-Item -ItemType Directory -Path $inputDirectory, $outputDirectory | Out-Null
Copy-Item -LiteralPath $SetupPath -Destination (Join-Path $inputDirectory 'Setup.exe')
Copy-Item -LiteralPath $ExecutablePath -Destination (Join-Path $inputDirectory 'expected.exe')
Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'installer-sandbox-test.ps1') -Destination $inputDirectory
$expected = [ordered]@{
    setupSha256 = Get-FileSha256Lower -Path (Join-Path $inputDirectory 'Setup.exe')
    executableSha256 = Get-FileSha256Lower -Path (Join-Path $inputDirectory 'expected.exe')
}
Write-Utf8NoBom -Path (Join-Path $inputDirectory 'expected.json') -Content (ConvertTo-DeterministicJsonText -Value $expected)
$inputXml = [Security.SecurityElement]::Escape($inputDirectory)
$outputXml = [Security.SecurityElement]::Escape($outputDirectory)
$configuration = @"
<Configuration>
  <Networking>Disable</Networking>
  <ClipboardRedirection>Disable</ClipboardRedirection>
  <PrinterRedirection>Disable</PrinterRedirection>
  <AudioInput>Disable</AudioInput>
  <VideoInput>Disable</VideoInput>
  <MappedFolders>
    <MappedFolder><HostFolder>$inputXml</HostFolder><SandboxFolder>C:\DashboardInstallerInput</SandboxFolder><ReadOnly>true</ReadOnly></MappedFolder>
    <MappedFolder><HostFolder>$outputXml</HostFolder><SandboxFolder>C:\DashboardInstallerResults</SandboxFolder><ReadOnly>false</ReadOnly></MappedFolder>
  </MappedFolders>
  <LogonCommand><Command>powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:\DashboardInstallerInput\installer-sandbox-test.ps1</Command></LogonCommand>
</Configuration>
"@
$configurationPath = Join-Path $bundle 'installer-test.wsb'
Write-Utf8NoBom -Path $configurationPath -Content $configuration
Write-Host "Sandbox bundle: $configurationPath"
Write-Host "Results: $outputDirectory"
Write-Host 'Open the .wsb only on a Windows host with Windows Sandbox already available.'
Write-Host 'This command prepared test inputs; it did not run install/uninstall E2E.'
