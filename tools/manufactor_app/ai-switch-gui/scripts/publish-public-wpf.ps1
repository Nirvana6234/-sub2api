[CmdletBinding()]
param(
    [ValidateSet("FrameworkDependent", "SelfContained")]
    [string]$Mode = "SelfContained",

    [switch]$SkipSmokeTest
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectDir = Split-Path -Parent $scriptDir
$projectFile = Join-Path $projectDir "src\AiSwitch.Wpf\AiSwitch.Wpf.csproj"
$artifactsRoot = Join-Path $projectDir "artifacts\PublicRelease"
$runtimeIdentifier = "win-x64"
$isSelfContained = $Mode -eq "SelfContained"
$packageFlavor = if ($isSelfContained) { "self-contained" } else { "framework-dependent" }
$packageName = "LanAi.Workspace-Public-$runtimeIdentifier-$packageFlavor"
$publishDir = Join-Path $artifactsRoot $packageName
$zipPath = Join-Path $artifactsRoot "$packageName.zip"
$launcherSource = Join-Path $scriptDir "start-wpf.cmd"
$readmeSource = Join-Path $projectDir "PUBLIC-README.zh-CN.md"

function Assert-PathInsideArtifacts {
    param([Parameter(Mandatory)][string]$Path)

    $root = [System.IO.Path]::GetFullPath($artifactsRoot).TrimEnd('\') + '\'
    $candidate = [System.IO.Path]::GetFullPath($Path)
    if (-not $candidate.StartsWith($root, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to modify a path outside the public artifacts directory: $candidate"
    }
}

function Stop-SmokeProcess {
    param([Parameter(Mandatory)][System.Diagnostics.Process]$Process)

    $Process.Refresh()
    if ($Process.HasExited) { return }
    $null = $Process.CloseMainWindow()
    if (-not $Process.WaitForExit(3000)) {
        Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue
        $Process.WaitForExit(3000)
    }
}

function Assert-NoUserDataInPackage {
    param([Parameter(Mandatory)][string]$Directory)

    $blockedFileNames = @(
        "sub2api-local-account-session.bin",
        "local-control-token.bin",
        "profiles.json",
        "appsettings.json"
    )
    $blocked = @(Get-ChildItem -LiteralPath $Directory -Recurse -Force | Where-Object {
        ($_.PSIsContainer -and $_.Name -ieq "Auth") -or
        (-not $_.PSIsContainer -and $blockedFileNames -icontains $_.Name)
    })
    if ($blocked.Count -gt 0) {
        $paths = ($blocked.FullName -join [Environment]::NewLine)
        throw "Refusing to package local account or user configuration data:`n$paths"
    }
}

foreach ($required in @($projectFile, $launcherSource, $readmeSource)) {
    if (-not (Test-Path -LiteralPath $required)) {
        throw "Required public-release file not found: $required"
    }
}

$dotnetCommand = Get-Command dotnet -ErrorAction SilentlyContinue
if (-not $dotnetCommand) {
    throw ".NET SDK 8 was not found."
}

New-Item -ItemType Directory -Path $artifactsRoot -Force | Out-Null
Assert-PathInsideArtifacts -Path $publishDir
Assert-PathInsideArtifacts -Path $zipPath

if (Test-Path -LiteralPath $publishDir) {
    Remove-Item -LiteralPath $publishDir -Recurse -Force
}
if (Test-Path -LiteralPath $zipPath) {
    Remove-Item -LiteralPath $zipPath -Force
}

$selfContainedValue = if ($isSelfContained) { "true" } else { "false" }
$publishArguments = @(
    "publish", $projectFile,
    "-c", "Release",
    "-r", $runtimeIdentifier,
    "--self-contained", $selfContainedValue,
    "-o", $publishDir,
    "--no-restore",
    "--nologo",
    "-m:1",
    "-p:BuildInParallel=false",
    "-p:UseSharedCompilation=false",
    "-p:PublicRelease=true",
    "-p:PublishSingleFile=false",
    "-p:PublishTrimmed=false",
    "-p:PublishReadyToRun=false",
    "-p:DebugSymbols=false",
    "-p:DebugType=None"
)

& $dotnetCommand.Source @publishArguments
if ($LASTEXITCODE -ne 0) {
    throw "dotnet publish failed with exit code $LASTEXITCODE."
}

$originalExe = Join-Path $publishDir "LanAi.Workspace.exe"
if (-not (Test-Path -LiteralPath $originalExe)) {
    throw "Published executable not found: $originalExe"
}
Copy-Item -LiteralPath $launcherSource -Destination (Join-Path $publishDir "Start-LanAi-Workspace.cmd")
Copy-Item -LiteralPath $readmeSource -Destination (Join-Path $publishDir "README.zh-CN.md")

if (-not $SkipSmokeTest) {
    $smokeProcess = $null
    $smokeProfileRoot = Join-Path $artifactsRoot ("smoke-profile-" + [Guid]::NewGuid().ToString("N"))
    try {
        Assert-PathInsideArtifacts -Path $smokeProfileRoot
        $smokeLocalAppData = Join-Path $smokeProfileRoot "AppData\Local"
        $smokeRoamingAppData = Join-Path $smokeProfileRoot "AppData\Roaming"
        New-Item -ItemType Directory -Path $smokeLocalAppData -Force | Out-Null
        New-Item -ItemType Directory -Path $smokeRoamingAppData -Force | Out-Null
        $smokeProcess = Start-Process `
            -FilePath $originalExe `
            -WorkingDirectory $publishDir `
            -WindowStyle Hidden `
            -Environment @{
                USERPROFILE = $smokeProfileRoot
                LOCALAPPDATA = $smokeLocalAppData
                APPDATA = $smokeRoamingAppData
            } `
            -PassThru
        Start-Sleep -Seconds 5
        $smokeProcess.Refresh()
        if ($smokeProcess.HasExited) {
            throw "The public application exited during startup smoke testing (exit code $($smokeProcess.ExitCode))."
        }
    }
    finally {
        if ($null -ne $smokeProcess) {
            Stop-SmokeProcess -Process $smokeProcess
            $smokeProcess.Dispose()
        }
        if (Test-Path -LiteralPath $smokeProfileRoot) {
            Remove-Item -LiteralPath $smokeProfileRoot -Recurse -Force
        }
    }
}

Assert-NoUserDataInPackage -Directory $publishDir
Compress-Archive -Path (Join-Path $publishDir "*") -DestinationPath $zipPath -CompressionLevel Optimal

[pscustomobject]@{
    Mode = $Mode
    PublishDirectory = $publishDir
    Executable = $originalExe
    Zip = $zipPath
    SmokeTest = if ($SkipSmokeTest) { "Skipped" } else { "Passed" }
}
