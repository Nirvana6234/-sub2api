# AI Switch GUI Remote Deployment

If `LocalGatewayManager.exe` cannot be opened on the remote desktop, the most common cause is using the wrong artifact.

Use this package:

```text
tools\manufactor_app\ai-switch-gui\bin\Release\net8.0-windows\win-x64\publish\
```

Do not deploy this by itself:

```text
tools\manufactor_app\ai-switch-gui\bin\Release\net8.0-windows\win-x64\LocalGatewayManager.exe
```

That file is a normal build output. It is not the deployment target to copy around by itself.

## Recommended build

Run:

```powershell
powershell -ExecutionPolicy Bypass -File tools\manufactor_app\ai-switch-gui\publish.ps1
```

This produces:

```text
tools\manufactor_app\ai-switch-gui\bin\Release\net8.0-windows\win-x64\publish\LocalGatewayManager.exe
tools\manufactor_app\ai-switch-gui\bin\Release\net8.0-windows\win-x64\LocalGatewayManager-publish.zip
```

## Remote replacement steps

1. On `192.168.31.238`, close any running `LocalGatewayManager.exe`.
2. Replace the files under:

```text
C:\Users\Administrator\ai-switch-gui-app
```

3. Make sure the desktop shortcut target is:

```text
C:\Users\Administrator\ai-switch-gui-app\LocalGatewayManager.exe
```

4. Start it again from the desktop shortcut.

## Quick checks if it still will not open

1. Confirm the remote machine received the `publish` version, not the root `win-x64` build output.
2. Confirm the old process is fully closed before overwriting files.
3. Confirm the desktop shortcut target still points to `C:\Users\Administrator\ai-switch-gui-app\LocalGatewayManager.exe`.
4. If the shortcut points elsewhere, recreate it.
