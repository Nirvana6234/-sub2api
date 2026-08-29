$ErrorActionPreference = "Stop"

$projectDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectFile = Join-Path $projectDir "AiSwitchGui.csproj"
$publishDir = Join-Path $projectDir "bin\Release\net8.0-windows\win-x64\publish"
$zipPath = Join-Path $projectDir "bin\Release\net8.0-windows\win-x64\LocalGatewayManager-publish.zip"

$dotnetCandidates = @(
    (Get-Command dotnet -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -ErrorAction SilentlyContinue),
    "C:\Users\user\AppData\Local\dotnet\dotnet.exe",
    "C:\Program Files\dotnet\dotnet.exe",
    "C:\Program Files (x86)\dotnet\dotnet.exe"
) | Where-Object { $_ -and (Test-Path $_) }

if (-not $dotnetCandidates) {
    throw "dotnet.exe not found. Install .NET SDK 8 and rerun."
}

$dotnet = $dotnetCandidates[0]

if (Test-Path -LiteralPath $publishDir) {
    Remove-Item -LiteralPath $publishDir -Recurse -Force
}

Write-Host "Publishing AI Switch GUI..."
& $dotnet publish $projectFile -c Release

if (-not (Test-Path $publishDir)) {
    throw "Publish directory not found: $publishDir"
}

if (Test-Path $zipPath) {
    Remove-Item -LiteralPath $zipPath -Force
}

Compress-Archive -Path (Join-Path $publishDir "*") -DestinationPath $zipPath

Write-Host ""
Write-Host "Publish complete."
Write-Host "EXE : $publishDir\LocalGatewayManager.exe"
Write-Host "ZIP : $zipPath"
