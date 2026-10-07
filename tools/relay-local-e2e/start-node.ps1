# Start a relay node on 18081 (plain http, no database, no redis).
# Needs the master's root fingerprint: run "python tools\relay-local-e2e\drive.py fingerprint" first.
param(
    [string]$Fingerprint = '',
    [string]$BindHost = '192.168.216.1'
)
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path "$PSScriptRoot\..\..").Path
Set-Location $repo
$e2e = '.local\relay-e2e'
if (-not (Test-Path "$e2e\sub2api-relay.exe")) { throw "missing $e2e\sub2api-relay.exe (run tools\relay-local-e2e\build.ps1)" }
if (-not $Fingerprint) {
    if (-not (Test-Path "$e2e\root-fingerprint.txt")) { throw "no fingerprint: run 'python tools\relay-local-e2e\drive.py fingerprint' or pass -Fingerprint" }
    $Fingerprint = (Get-Content "$e2e\root-fingerprint.txt" -Raw).Trim()
}
New-Item -ItemType Directory -Force "$e2e\node" | Out-Null

# A node needs only these. Everything else is pushed down by the master after activation.
$env:NODE_ROLE = 'relay'
$env:DATA_DIR = (Resolve-Path "$e2e\node").Path            # long-term identity key, certificate, local billing queue, logs
$env:SERVER_HOST = $BindHost
$env:SERVER_PORT = '18081'
$env:RELAY_NODE_MASTER_ADDR = '127.0.0.1:17443'
$env:RELAY_NODE_MASTER_URL = 'http://127.0.0.1:18080'
$env:RELAY_NODE_ROOT_FINGERPRINTS = $Fingerprint
$env:RELAY_NODE_DISPLAY_NAME = 'local-node'
# No public HTTPS locally: serve plain http on SERVER_PORT instead of :443 + Let's Encrypt.
$env:RELAY_NODE_TLS_DISABLED = 'true'
# The node must not see database / redis / admin credentials.
Remove-Item env:DATABASE_PASSWORD, env:RELAY_KEY_ENCRYPTION_KEY, env:JWT_SECRET, env:ADMIN_PASSWORD, env:RELAY_MASTER_LISTEN_ADDR -ErrorAction SilentlyContinue

Start-Process "$e2e\sub2api-relay.exe" -WorkingDirectory $repo `
    -RedirectStandardOutput "$e2e\node.out.log" -RedirectStandardError "$e2e\node.err.log" -WindowStyle Hidden
Write-Host "node starting on http://$BindHost`:18081 ; it shows up as 'pending' in the admin page until you activate it"
