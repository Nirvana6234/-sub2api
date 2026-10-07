# Build the server binary used by both the master and the relay node.
# Run from anywhere: powershell -File tools\relay-local-e2e\build.ps1
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path "$PSScriptRoot\..\..").Path
$out = Join-Path $repo '.local\relay-e2e'
New-Item -ItemType Directory -Force $out | Out-Null
Push-Location (Join-Path $repo 'backend')
try {
    # Add "-tags embed" after building the frontend (cd frontend; pnpm build) if you want the admin UI served by the master itself.
    go build -tags timetzdata -o (Join-Path $out 'sub2api-relay.exe') ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
    Pop-Location
}
Write-Host "built $out\sub2api-relay.exe"
