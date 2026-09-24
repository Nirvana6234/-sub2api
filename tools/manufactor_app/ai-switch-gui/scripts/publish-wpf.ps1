[CmdletBinding()]
param(
    [ValidateSet("FrameworkDependent", "SelfContained")]
    [string]$Mode = "FrameworkDependent",

    [switch]$SkipSmokeTest
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectDir = Split-Path -Parent $scriptDir
$projectFile = Join-Path $projectDir "src\AiSwitch.Wpf\AiSwitch.Wpf.csproj"
$artifactsRoot = Join-Path $projectDir "artifacts\LanAi.Workspace"
$runtimeIdentifier = "win-x64"
$isSelfContained = $Mode -eq "SelfContained"
$packageFlavor = if ($isSelfContained) { "self-contained" } else { "framework-dependent" }
$packageName = "LanAi.Workspace-$runtimeIdentifier-$packageFlavor"
$publishDir = Join-Path $artifactsRoot $packageName
$zipPath = Join-Path $artifactsRoot "$packageName.zip"
$launcherSource = Join-Path $scriptDir "start-wpf.cmd"
$launcherTarget = Join-Path $publishDir "Start-LanAi-Workspace.cmd"
$installerScriptSource = Join-Path $scriptDir "install-wpf-update.ps1"
$installerScriptTarget = Join-Path $publishDir "install-wpf-update.ps1"
$installerLauncherSource = Join-Path $scriptDir "install-wpf-update.cmd"
$installerLauncherTarget = Join-Path $publishDir "Install-Update.cmd"
$readmeCandidates = @(Get-ChildItem -LiteralPath $projectDir -Filter "WPF-*.md" -File)
$readmeSource = if ($readmeCandidates.Count -eq 1) { $readmeCandidates[0].FullName } else { $null }
$readmeTarget = Join-Path $publishDir "README.zh-CN.md"

function Assert-PathInsideArtifacts {
    param([Parameter(Mandatory)][string]$Path)

    $artifactsFullPath = [System.IO.Path]::GetFullPath($artifactsRoot).TrimEnd('\') + '\'
    $candidateFullPath = [System.IO.Path]::GetFullPath($Path)
    if (-not $candidateFullPath.StartsWith($artifactsFullPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to modify a path outside the WPF artifacts directory: $candidateFullPath"
    }
}

function Stop-SmokeProcess {
    param([Parameter(Mandatory)][System.Diagnostics.Process]$Process)

    $Process.Refresh()
    if ($Process.HasExited) {
        return
    }

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

if (-not (Test-Path -LiteralPath $projectFile)) {
    throw "WPF project not found: $projectFile"
}

if (-not (Test-Path -LiteralPath $launcherSource)) {
    throw "Launcher template not found: $launcherSource"
}

foreach ($installerFile in @($installerScriptSource, $installerLauncherSource)) {
    if (-not (Test-Path -LiteralPath $installerFile)) {
        throw "Upgrade installer template not found: $installerFile"
    }
}

if ($null -eq $readmeSource -or -not (Test-Path -LiteralPath $readmeSource)) {
    throw "Expected exactly one WPF startup guide matching WPF-*.md in: $projectDir"
}

$dotnetCommand = Get-Command dotnet -ErrorAction SilentlyContinue
if (-not $dotnetCommand) {
    throw ".NET SDK 8 was not found. Install the SDK and rerun this script."
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
$restoreArguments = @(
    "restore",
    $projectFile,
    "-r", $runtimeIdentifier,
    "--nologo",
    "-p:SelfContained=$selfContainedValue"
)
$cleanArguments = @(
    "clean",
    $projectFile,
    "-c", "Release",
    "-r", $runtimeIdentifier,
    "--nologo",
    "-m:1",
    "-p:BuildInParallel=false",
    "-p:UseSharedCompilation=false",
    "-p:SelfContained=$selfContainedValue"
)
$publishArguments = @(
    "publish",
    $projectFile,
    "-c", "Release",
    "-r", $runtimeIdentifier,
    "--self-contained", $selfContainedValue,
    "-o", $publishDir,
    "--no-restore",
    "--nologo",
    "-m:1",
    "-p:BuildInParallel=false",
    "-p:UseSharedCompilation=false",
    "-p:PublishSingleFile=false",
    "-p:PublishTrimmed=false",
    "-p:PublishReadyToRun=false",
    "-p:DebugSymbols=false",
    "-p:DebugType=None"
)

Write-Host "Restoring $packageName dependencies for $runtimeIdentifier ..."
& $dotnetCommand.Source @restoreArguments
if ($LASTEXITCODE -ne 0) {
    throw "dotnet restore failed with exit code $LASTEXITCODE."
}

Write-Host "Cleaning $packageName build outputs ..."
& $dotnetCommand.Source @cleanArguments
if ($LASTEXITCODE -ne 0) {
    throw "dotnet clean failed with exit code $LASTEXITCODE."
}

Write-Host "Publishing $packageName ..."
& $dotnetCommand.Source @publishArguments
if ($LASTEXITCODE -ne 0) {
    throw "dotnet publish failed with exit code $LASTEXITCODE."
}

$exePath = Join-Path $publishDir "LanAi.Workspace.exe"
if (-not (Test-Path -LiteralPath $exePath)) {
    throw "Published executable not found: $exePath"
}

Copy-Item -LiteralPath $launcherSource -Destination $launcherTarget
Copy-Item -LiteralPath $installerScriptSource -Destination $installerScriptTarget
Copy-Item -LiteralPath $installerLauncherSource -Destination $installerLauncherTarget
Copy-Item -LiteralPath $readmeSource -Destination $readmeTarget

if (-not $SkipSmokeTest) {
    Write-Host "Running hidden-start smoke test ..."
    $smokeProcess = $null
    $smokeProfileRoot = Join-Path $artifactsRoot ("smoke-profile-" + [Guid]::NewGuid().ToString("N"))
    try {
        Assert-PathInsideArtifacts -Path $smokeProfileRoot
        $smokeLocalAppData = Join-Path $smokeProfileRoot "AppData\Local"
        $smokeRoamingAppData = Join-Path $smokeProfileRoot "AppData\Roaming"
        New-Item -ItemType Directory -Path $smokeLocalAppData -Force | Out-Null
        New-Item -ItemType Directory -Path $smokeRoamingAppData -Force | Out-Null
        $previousUserProfile = $env:USERPROFILE
        $previousLocalAppData = $env:LOCALAPPDATA
        $previousAppData = $env:APPDATA
        try {
            $env:USERPROFILE = $smokeProfileRoot
            $env:LOCALAPPDATA = $smokeLocalAppData
            $env:APPDATA = $smokeRoamingAppData
            $smokeProcess = Start-Process `
                -FilePath $exePath `
                -WorkingDirectory $publishDir `
                -WindowStyle Hidden `
                -PassThru
        }
        finally {
            $env:USERPROFILE = $previousUserProfile
            $env:LOCALAPPDATA = $previousLocalAppData
            $env:APPDATA = $previousAppData
        }

        Start-Sleep -Seconds 5
        $smokeProcess.Refresh()
        if ($smokeProcess.HasExited) {
            throw "The published application exited during startup smoke testing (exit code $($smokeProcess.ExitCode))."
        }

        Write-Host "Hidden-start smoke test passed."
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

$packageFiles = Get-ChildItem -LiteralPath $publishDir -Recurse -File
$packageBytes = ($packageFiles | Measure-Object -Property Length -Sum).Sum
if ($null -eq $packageBytes) {
    $packageBytes = 0
}

$zipBytes = (Get-Item -LiteralPath $zipPath).Length
$packageMiB = [Math]::Round($packageBytes / 1MB, 2)
$zipMiB = [Math]::Round($zipBytes / 1MB, 2)

Write-Host ""
Write-Host "WPF publish completed."
Write-Host "Mode      : $Mode"
Write-Host "Directory : $publishDir ($packageMiB MiB)"
Write-Host "Executable: $exePath"
Write-Host "ZIP       : $zipPath ($zipMiB MiB)"

[pscustomobject]@{
    Mode = $Mode
    RuntimeIdentifier = $runtimeIdentifier
    PublishDirectory = $publishDir
    Executable = $exePath
    Zip = $zipPath
    PackageBytes = [long]$packageBytes
    ZipBytes = [long]$zipBytes
    SmokeTest = if ($SkipSmokeTest) { "Skipped" } else { "Passed" }
}
