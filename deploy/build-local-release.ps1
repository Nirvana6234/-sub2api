[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9._-]*$')]
    [string]$ReleaseTag,

    [string]$OutputDirectory = (Join-Path (Get-Location) 'artifacts\releases')
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
$imageTag = "sub2api:$ReleaseTag"
$archiveName = "sub2api-$ReleaseTag-linux-amd64.tar"
$stageDirectory = Join-Path ([System.IO.Path]::GetTempPath()) "sub2api-build-$([Guid]::NewGuid().ToString('N'))"

$baseImages = @(
    'sub2api-build-node:24-alpine',
    'sub2api-build-golang:1.26.5-alpine',
    'sub2api-build-postgres:18-alpine',
    'sub2api-build-alpine:3.21'
)

foreach ($baseImage in $baseImages) {
    & docker image inspect $baseImage *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Missing local build base image: $baseImage. Restore the approved local cache before releasing; do not build on the server."
    }
}

New-Item -ItemType Directory -Path $stageDirectory | Out-Null

try {
    # Windows pnpm junctions can be unreadable to Docker even when .dockerignore
    # excludes them, so construct a clean one-use build context first.
    & robocopy $repoRoot $stageDirectory /E /XD node_modules .pnpm-store .git artifacts backend\artifacts *> $null
    if ($LASTEXITCODE -gt 7) {
        throw "Failed to prepare the local Docker build context (robocopy exit code $LASTEXITCODE)."
    }

    & docker build --platform linux/amd64 `
        -t $imageTag `
        --build-arg NODE_IMAGE=sub2api-build-node:24-alpine `
        --build-arg GOLANG_IMAGE=sub2api-build-golang:1.26.5-alpine `
        --build-arg POSTGRES_IMAGE=sub2api-build-postgres:18-alpine `
        --build-arg ALPINE_IMAGE=sub2api-build-alpine:3.21 `
        --build-arg GOPROXY=https://goproxy.cn,direct `
        --build-arg GOSUMDB=sum.golang.google.cn `
        -f (Join-Path $stageDirectory 'Dockerfile.local-build') `
        $stageDirectory
    if ($LASTEXITCODE -ne 0) {
        throw "Local Docker image build failed for $imageTag."
    }

    & docker image inspect $imageTag *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Docker did not produce the expected image: $imageTag."
    }

    New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
    $archivePath = Join-Path $OutputDirectory $archiveName
    $checksumPath = "$archivePath.sha256"

    & docker save -o $archivePath $imageTag
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to export Docker image archive: $archivePath."
    }

    $hash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    Set-Content -LiteralPath $checksumPath -Value "$hash  $archiveName" -NoNewline -Encoding ascii

    Write-Host "Image: $imageTag"
    Write-Host "Archive: $archivePath"
    Write-Host "Checksum: $checksumPath"
}
finally {
    if (Test-Path -LiteralPath $stageDirectory) {
        Remove-Item -LiteralPath $stageDirectory -Recurse -Force
    }
}
