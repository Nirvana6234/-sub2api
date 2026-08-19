[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$Server,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$SshKey,

    [ValidateNotNullOrEmpty()]
    [string]$User = "ec2-user",

    [switch]$StatusOnly
)

$ErrorActionPreference = "Stop"

function Invoke-NativeChecked {
    param(
        [Parameter(Mandatory = $true)]
        [string]$FilePath,
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$FilePath failed with exit code $LASTEXITCODE"
    }
}

function Invoke-SshChecked {
    param(
        [Parameter(Mandatory = $true)]
        [string]$RemoteCommand,
        [int]$Attempts = 8
    )

    $payload = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($RemoteCommand))
    $runner = "printf '%s' '$payload' | base64 -d | bash"
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        & ssh @commonOptions $target $runner
        if ($LASTEXITCODE -eq 0) {
            return
        }
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 255) {
            throw "remote command failed with exit code $exitCode"
        }
        if ($attempt -lt $Attempts) {
            Write-Warning "SSH attempt $attempt failed; waiting 15 seconds before retrying."
            Start-Sleep -Seconds 15
        }
    }
    throw "ssh failed after $Attempts low-frequency attempts"
}

if (-not (Test-Path -LiteralPath $SshKey -PathType Leaf)) {
    throw "SSH key not found: $SshKey"
}

$target = "${User}@${Server}"
$commonOptions = @(
    "-o", "ClearAllForwardings=yes",
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=10",
    "-o", "ServerAliveInterval=5",
    "-o", "ServerAliveCountMax=2",
    "-o", "StrictHostKeyChecking=accept-new",
    "-i", (Resolve-Path -LiteralPath $SshKey).Path
)

if ($StatusOnly) {
    Invoke-SshChecked -RemoteCommand "sudo /usr/local/sbin/transit-host-guard status" -Attempts 3
    exit 0
}

$packageDir = $PSScriptRoot
$files = @(
    "transit-host-guard.sh",
    "transit-host-guard.service",
    "transit-host-guard.conf.example",
    "docker-wrapper.sh",
    "README.md"
)
foreach ($file in $files) {
    $path = Join-Path $packageDir $file
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Protection package file not found: $path"
    }
}

$remoteDir = "/tmp/transit-host-guard-$([Guid]::NewGuid().ToString('N'))"
$capacityCheck = @'
set -eu
elapsed=0
max_wait=600
while :; do
  mem_kb=$(awk '/^MemAvailable:/ {print $2; exit}' /proc/meminfo)
  load_1m=$(awk '{print $1}' /proc/loadavg)
  cpus=$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf '1')
  disk_kb=$(df -Pk / | awk 'NR == 2 {print $4}')
  load_limit=$(awk -v cpus="$cpus" 'BEGIN { value=cpus*1.5; if (value<1.5) value=1.5; printf "%.2f", value }')
  ready=$(awk -v current_load="$load_1m" -v limit="$load_limit" -v mem="$mem_kb" -v disk="$disk_kb" \
    'BEGIN { print (current_load<=limit && mem>=98304 && disk>=65536) ? 1 : 0 }')
  printf 'capacity check: load=%s/%s mem_available=%sKB disk_available=%sKB waited=%ss\n' \
    "$load_1m" "$load_limit" "$mem_kb" "$disk_kb" "$elapsed"
  if [ "$ready" -eq 1 ]; then
    exit 0
  fi
  if [ "$elapsed" -ge "$max_wait" ]; then
    printf 'capacity check timed out without changing the server\n' >&2
    exit 75
  fi
  sleep 15
  elapsed=$((elapsed + 15))
done
'@
Invoke-SshChecked -RemoteCommand $capacityCheck
Invoke-SshChecked -RemoteCommand "install -d -m 700 '$remoteDir'" -Attempts 3

$scpOptions = @(
    "-o", "ClearAllForwardings=yes",
    "-o", "BatchMode=yes",
    "-o", "ConnectTimeout=10",
    "-o", "StrictHostKeyChecking=accept-new",
    "-i", (Resolve-Path -LiteralPath $SshKey).Path,
    "-l", "512"
)
$localFiles = $files | ForEach-Object { Join-Path $packageDir $_ }
Invoke-NativeChecked -FilePath "scp" -Arguments ($scpOptions + $localFiles + @("${target}:$remoteDir/"))

$cleanupFiles = ($files | ForEach-Object { "'$remoteDir/$_'" }) -join " "
$remoteCommand = @"
set -eu
cleanup() {
  rm -f -- $cleanupFiles
  rmdir -- '$remoteDir' 2>/dev/null || true
}
trap cleanup EXIT
chmod 700 '$remoteDir/transit-host-guard.sh'
if command -v ionice >/dev/null 2>&1; then
  sudo ionice -c3 nice -n 10 bash '$remoteDir/transit-host-guard.sh' install
else
  sudo nice -n 10 bash '$remoteDir/transit-host-guard.sh' install
fi
sudo /usr/local/sbin/transit-host-guard status
"@

Invoke-SshChecked -RemoteCommand $remoteCommand -Attempts 3

Write-Host "Host protection installed on $target. No cloud build or source compilation was performed."
