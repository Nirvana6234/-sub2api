param([string]$BindHost = '0.0.0.0')
# Start the master (admin API on 18080, master/node channel on 17443). Logs go to .local\relay-e2e\master.*.log
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path "$PSScriptRoot\..\..").Path
Set-Location $repo
$e2e = '.local\relay-e2e'
if (-not (Test-Path "$e2e\secrets.ps1")) { throw "missing $e2e\secrets.ps1 (copy tools\relay-local-e2e\secrets.example.ps1 and fill it in)" }
if (-not (Test-Path "$e2e\sub2api-relay.exe")) { throw "missing $e2e\sub2api-relay.exe (run tools\relay-local-e2e\build.ps1)" }
New-Item -ItemType Directory -Force "$e2e\master" | Out-Null
. "$e2e\secrets.ps1"
$env:SERVER_HOST = $BindHost
Start-Process "$e2e\sub2api-relay.exe" -WorkingDirectory $repo `
    -RedirectStandardOutput "$e2e\master.out.log" -RedirectStandardError "$e2e\master.err.log" -WindowStyle Hidden
Write-Host "master starting on $BindHost`:18080; give it ~15 seconds, then: python tools\relay-local-e2e\drive.py status"
