@echo off
setlocal
set "APP_DIR=%~dp0"
set "APP_EXE=%APP_DIR%LanAi.Workspace.exe"

if not exist "%APP_EXE%" (
    echo LanAi.Workspace.exe was not found in:
    echo %APP_DIR%
    pause
    exit /b 1
)

pushd "%APP_DIR%"
start "" "%APP_EXE%"
popd
endlocal
