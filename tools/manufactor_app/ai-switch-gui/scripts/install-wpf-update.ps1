[CmdletBinding()]
param(
    [string]$InstallRoot = (Join-Path ([Environment]::GetFolderPath("UserProfile")) "ai-switch-gui-app"),

    [switch]$NoStart,

    [switch]$SkipShortcut
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$applicationName = "共飞AI工作台"
$executableName = "LanAi.Workspace.exe"
$installerFiles = @("install-wpf-update.ps1", "一键升级安装.cmd")
$sourceRoot = [System.IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\')
$installRootFull = [System.IO.Path]::GetFullPath($InstallRoot).TrimEnd('\')
$installParent = Split-Path -Parent $installRootFull
$installLeaf = Split-Path -Leaf $installRootFull
$sourcePrefix = $sourceRoot + '\'
$installPrefix = $installRootFull + '\'

function Set-RestrictedInstallationAcl {
    param([Parameter(Mandatory)][string]$Path)

    $currentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
    if ($null -eq $currentUser) {
        throw "无法确定当前 Windows 用户，拒绝安装到未受保护的目录。"
    }

    $acl = New-Object System.Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($identity in @($currentUser, 'NT AUTHORITY\SYSTEM', 'BUILTIN\Administrators')) {
        $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
            $identity,
            [System.Security.AccessControl.FileSystemRights]::FullControl,
            [System.Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit',
            [System.Security.AccessControl.PropagationFlags]::None,
            [System.Security.AccessControl.AccessControlType]::Allow)
        [void]$acl.AddAccessRule($rule)
    }
    Set-Acl -LiteralPath $Path -AclObject $acl
}

function Get-PayloadFiles {
    param([Parameter(Mandatory)][string]$Root)

    @(Get-ChildItem -LiteralPath $Root -Recurse -Force -File | Where-Object {
        $installerFiles -inotcontains $_.Name
    })
}

function Get-PayloadManifest {
    param([Parameter(Mandatory)][string]$Root)

    $rootPrefix = [System.IO.Path]::GetFullPath($Root).TrimEnd('\') + '\'
    $manifest = @{}
    foreach ($file in (Get-PayloadFiles -Root $Root)) {
        $relativePath = $file.FullName.Substring($rootPrefix.Length)
        $manifest[$relativePath] = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
    }
    $manifest
}

function Assert-MatchingPayload {
    param(
        [Parameter(Mandatory)][string]$ExpectedRoot,
        [Parameter(Mandatory)][string]$ActualRoot
    )

    $expected = Get-PayloadManifest -Root $ExpectedRoot
    $actual = Get-PayloadManifest -Root $ActualRoot
    if ($expected.Count -ne $actual.Count) {
        throw "文件校验失败：文件数量不一致（源 $($expected.Count)，目标 $($actual.Count)）。"
    }

    foreach ($relativePath in $expected.Keys) {
        if (-not $actual.ContainsKey($relativePath)) {
            throw "文件校验失败：目标缺少 $relativePath"
        }
        if ($expected[$relativePath] -ne $actual[$relativePath]) {
            throw "文件校验失败：SHA-256 不一致：$relativePath"
        }
    }
}

function Copy-Payload {
    param(
        [Parameter(Mandatory)][string]$Source,
        [Parameter(Mandatory)][string]$Destination
    )

    $sourcePrefixForCopy = [System.IO.Path]::GetFullPath($Source).TrimEnd('\') + '\'
    foreach ($file in (Get-PayloadFiles -Root $Source)) {
        $relativePath = $file.FullName.Substring($sourcePrefixForCopy.Length)
        $destinationFile = Join-Path $Destination $relativePath
        $destinationDirectory = Split-Path -Parent $destinationFile
        New-Item -ItemType Directory -Path $destinationDirectory -Force | Out-Null
        Copy-Item -LiteralPath $file.FullName -Destination $destinationFile -Force
    }
}

function Stop-InstalledApplication {
    param([Parameter(Mandatory)][string]$ExecutablePath)

    $expectedPath = [System.IO.Path]::GetFullPath($ExecutablePath)
    $processes = @(Get-Process -Name "LanAi.Workspace" -ErrorAction SilentlyContinue | Where-Object {
        try {
            [string]::Equals([System.IO.Path]::GetFullPath($_.Path), $expectedPath, [System.StringComparison]::OrdinalIgnoreCase)
        }
        catch {
            $false
        }
    })

    foreach ($process in $processes) {
        Write-Host "正在关闭已安装的应用（PID $($process.Id)）..."
        $null = $process.CloseMainWindow()
        if (-not $process.WaitForExit(5000)) {
            Stop-Process -Id $process.Id -Force
            $process.WaitForExit(5000)
        }
    }
}

function Set-ApplicationShortcut {
    param([Parameter(Mandatory)][string]$ExecutablePath)

    $shell = New-Object -ComObject WScript.Shell
    $startMenuDirectory = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\AI 本地管理工具"
    New-Item -ItemType Directory -Path $startMenuDirectory -Force | Out-Null

    $shortcutPaths = @((Join-Path $startMenuDirectory "$applicationName.lnk"))
    $desktopShortcut = Join-Path ([Environment]::GetFolderPath("Desktop")) "$applicationName.lnk"
    if (Test-Path -LiteralPath $desktopShortcut) {
        $shortcutPaths += $desktopShortcut
    }

    foreach ($shortcutPath in $shortcutPaths) {
        $shortcut = $shell.CreateShortcut($shortcutPath)
        $shortcut.TargetPath = $ExecutablePath
        $shortcut.WorkingDirectory = Split-Path -Parent $ExecutablePath
        $shortcut.IconLocation = "$ExecutablePath,0"
        $shortcut.Description = $applicationName
        $shortcut.Save()
    }
}

if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot $executableName))) {
    throw "安装包不完整：缺少 $executableName。请先完整解压 ZIP，再运行一键升级安装。"
}

if ([string]::IsNullOrWhiteSpace($installParent) -or
    [string]::Equals([System.IO.Path]::GetPathRoot($installRootFull).TrimEnd('\'), $installRootFull, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "拒绝使用磁盘根目录作为安装目录：$installRootFull"
}

if ($sourceRoot.StartsWith($installPrefix, [System.StringComparison]::OrdinalIgnoreCase) -or
    $installRootFull.StartsWith($sourcePrefix, [System.StringComparison]::OrdinalIgnoreCase) -or
    [string]::Equals($sourceRoot, $installRootFull, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "安装包目录与安装目录不能相同或互相包含。请把 ZIP 解压到桌面后再运行。"
}

New-Item -ItemType Directory -Path $installParent -Force | Out-Null
$stagingRoot = Join-Path $installParent (".$installLeaf.update-staging-" + [Guid]::NewGuid().ToString("N"))

try {
    Write-Host "正在准备 $applicationName 升级文件..."
    New-Item -ItemType Directory -Path $stagingRoot -Force | Out-Null
    Copy-Payload -Source $sourceRoot -Destination $stagingRoot
    Assert-MatchingPayload -ExpectedRoot $sourceRoot -ActualRoot $stagingRoot
    Write-Host "升级文件 SHA-256 校验通过。"

    Stop-InstalledApplication -ExecutablePath (Join-Path $installRootFull $executableName)

    if (Test-Path -LiteralPath $installRootFull) {
        Write-Host "正在直接移除旧版本程序文件（不创建备份）..."
        Remove-Item -LiteralPath $installRootFull -Recurse -Force
    }

    Move-Item -LiteralPath $stagingRoot -Destination $installRootFull
    Set-RestrictedInstallationAcl -Path $installRootFull
    Assert-MatchingPayload -ExpectedRoot $sourceRoot -ActualRoot $installRootFull

    $installedExecutable = Join-Path $installRootFull $executableName
    if (-not $SkipShortcut) {
        try {
            Set-ApplicationShortcut -ExecutablePath $installedExecutable
            Write-Host "开始菜单快捷方式已更新。"
        }
        catch {
            Write-Warning "应用已升级，但快捷方式更新失败：$($_.Exception.Message)"
        }
    }

    Write-Host ""
    Write-Host "$applicationName 升级安装完成。"
    Write-Host "安装目录：$installRootFull"
    Write-Host "旧版本已直接替换，未创建任何安装备份。"
    Write-Host "本地账号、数据库和用户配置目录未被修改。"

    if (-not $NoStart) {
        Start-Process -FilePath $installedExecutable -WorkingDirectory $installRootFull
    }
}
catch {
    Write-Warning "升级失败。按照安装策略，未创建或保留原安装备份。"
    throw
}
finally {
    if (Test-Path -LiteralPath $stagingRoot) {
        Remove-Item -LiteralPath $stagingRoot -Recurse -Force
    }
}
