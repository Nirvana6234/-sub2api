@echo off
setlocal
set "CLIENT_DIR=%~dp0"

powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference = 'Stop'; $directory = [IO.Path]::GetFullPath($env:CLIENT_DIR); $exe = Get-ChildItem -LiteralPath $directory -Filter '*.exe' -File | Where-Object { $_.Name -notlike '*Setup*' } | Select-Object -First 1; if ($null -eq $exe) { throw 'Client executable was not found next to this script.' }; $shortcutName = $exe.BaseName + '.lnk'; foreach ($path in @((Join-Path ([Environment]::GetFolderPath('DesktopDirectory')) $shortcutName), (Join-Path ([Environment]::GetFolderPath('StartMenu')) ('Programs\' + $shortcutName)))) { Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue }"
if errorlevel 1 (
  echo Shortcut removal failed.
  pause
  exit /b 1
)

echo Desktop and Start Menu shortcuts were removed.
pause
