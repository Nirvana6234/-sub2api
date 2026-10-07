# Start the fake OpenAI-compatible upstream on 18090 (only needed when you do NOT use a real OpenAI account).
$ErrorActionPreference = 'Stop'
$repo = (Resolve-Path "$PSScriptRoot\..\..").Path
Set-Location $repo
$e2e = '.local\relay-e2e'
New-Item -ItemType Directory -Force $e2e | Out-Null
Start-Process python -ArgumentList "tools\relay-local-e2e\fake_upstream.py" `
    -RedirectStandardOutput "$e2e\upstream.out.log" -RedirectStandardError "$e2e\upstream.err.log" -WindowStyle Hidden
Write-Host "fake upstream on http://127.0.0.1:18090"
