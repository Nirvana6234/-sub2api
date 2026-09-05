$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut('C:\Users\Administrator\Desktop\AI Switcher.lnk')
$shortcut.TargetPath = 'C:\Users\Administrator\ai-switch-gui-app\LocalGatewayManager.exe'
$shortcut.WorkingDirectory = 'C:\Users\Administrator\ai-switch-gui-app'
$shortcut.Save()
