[CmdletBinding()]
param(
    [string]$ReleaseName = "Ver0.5-正式-20260913-r1"
)

$ErrorActionPreference = "Stop"
$clientRoot = Split-Path -Parent $PSScriptRoot
$project = Join-Path $clientRoot "src\LanAi.RelayClient\LanAi.RelayClient.csproj"
$artifact = Join-Path $clientRoot "artifacts\$ReleaseName"
$stage = Join-Path $artifact "_publish"
$mainExeName = ([char]0x5171)+([char]0x98de)+"-ChatGPT"+([char]0x52a9)+([char]0x624b)+".exe"
$installScript = ([char]0x6ce8)+([char]0x518c)+"桌面和开始菜单快捷方式.cmd"
$removeScript = ([char]0x5220)+([char]0x9664)+"桌面和开始菜单快捷方式.cmd"

if (Test-Path -LiteralPath $artifact) { Remove-Item -LiteralPath $artifact -Recurse -Force }
New-Item -ItemType Directory -Path $stage -Force | Out-Null

dotnet publish $project -c Release -r win-x64 --self-contained false `
    -o $stage -p:PublishSingleFile=true -p:PublishTrimmed=false `
    -p:PublishReadyToRun=false --verbosity minimal
if ($LASTEXITCODE -ne 0) { throw "dotnet publish failed ($LASTEXITCODE)" }

Copy-Item (Join-Path $clientRoot $installScript) $artifact
Copy-Item (Join-Path $clientRoot $removeScript) $artifact
Move-Item (Join-Path $stage "LanAi.RelayClient.exe") (Join-Path $artifact $mainExeName)
New-Item -ItemType Directory -Path (Join-Path $artifact "context-filter") -Force | Out-Null
$filterSource = Join-Path $clientRoot "src\LanAi.RelayClient\bin\context-filter\context-filter.exe"
if (Test-Path (Join-Path $stage "context-filter.exe")) { $filterSource = Join-Path $stage "context-filter.exe" }
if (-not (Test-Path $filterSource)) { throw "Bundled context-filter.exe is missing: $filterSource" }
Copy-Item $filterSource (Join-Path $artifact "context-filter\context-filter.exe") -Force
if (Test-Path (Join-Path $stage "codex-installer")) { Move-Item (Join-Path $stage "codex-installer") $artifact }
Remove-Item -LiteralPath $stage -Recurse -Force

$zip = Join-Path $artifact "codex-relay-client_v0.5_x64.zip"
Compress-Archive -Path (Join-Path $artifact "*") -DestinationPath $zip -Force
Write-Output "Windows package: $artifact"
Get-ChildItem -LiteralPath $artifact -Recurse -File | Select-Object FullName,Length
