@echo off
setlocal
chcp 65001 >nul
title 共飞AI工作台升级安装

where pwsh.exe >nul 2>nul
if %errorlevel%==0 (
    pwsh.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-wpf-update.ps1"
) else (
    powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-wpf-update.ps1"
)

set "INSTALL_EXIT=%errorlevel%"
echo.
if not "%INSTALL_EXIT%"=="0" (
    echo 升级安装失败，错误代码：%INSTALL_EXIT%
) else (
    echo 升级安装成功。
)
echo 按任意键关闭此窗口...
pause >nul
exit /b %INSTALL_EXIT%
