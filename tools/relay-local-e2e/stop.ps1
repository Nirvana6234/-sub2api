# Stop the master, the node and the fake upstream started by the start-*.ps1 scripts.
Get-Process sub2api-relay -ErrorAction SilentlyContinue | Stop-Process -Force
Get-CimInstance Win32_Process -Filter "Name='python.exe'" |
    Where-Object { $_.CommandLine -like '*fake_upstream*' } |
    ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
Write-Host "stopped"
