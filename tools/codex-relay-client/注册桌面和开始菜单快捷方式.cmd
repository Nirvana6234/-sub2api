@echo off
setlocal
set "CLIENT_DIR=%~dp0"

powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference = 'Stop'; $directory = [IO.Path]::GetFullPath($env:CLIENT_DIR); $exe = Get-ChildItem -LiteralPath $directory -Filter '*.exe' -File | Where-Object { $_.Name -notlike '*Setup*' } | Select-Object -First 1; if ($null -eq $exe) { throw 'Client executable was not found next to this script.' }; $shell = New-Object -ComObject WScript.Shell; $shortcutName = $exe.BaseName + '.lnk'; foreach ($path in @((Join-Path ([Environment]::GetFolderPath('DesktopDirectory')) $shortcutName), (Join-Path ([Environment]::GetFolderPath('StartMenu')) ('Programs\' + $shortcutName)))) { $parent = Split-Path -Parent $path; New-Item -ItemType Directory -Path $parent -Force | Out-Null; $link = $shell.CreateShortcut($path); $link.TargetPath = $exe.FullName; $link.WorkingDirectory = $directory; $link.IconLocation = $exe.FullName + ',0'; $link.Save() }"
if errorlevel 1 (
  echo Shortcut registration failed.
  pause
  exit /b 1
)

echo Desktop and Start Menu shortcuts were created.
pause
