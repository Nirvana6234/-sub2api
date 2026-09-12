[CmdletBinding()]
param(
    [string]$Version = "latest",
    # Relative to this script, which lives at the root of the client tree. The
    # build passes this explicitly; the default only serves a manual run.
    #
    # It used to read "$PSScriptRoot\..\src\..." — one level too high, so even when
    # it resolved it wrote to tools/src/, which is not where the project looks. And
    # under MSBuild $PSScriptRoot came out empty, making the whole thing relative to
    # the drive root: the download landed in C:\src\. Between the two, the bundled
    # filter had never once reached the output directory.
    [string]$OutputDirectory = (Join-Path $PSScriptRoot "src\LanAi.RelayClient\bin\context-filter"),

    # GitHub API token. Optional locally, close to mandatory on CI: the anonymous
    # API allows 60 requests/hour **per IP**, and Actions runners share their egress
    # IPs across everyone building on GitHub. Unauthenticated, this call fails
    # intermittently on CI for reasons that have nothing to do with this repo — and
    # since the release workflow now refuses to ship without the filter, that would
    # turn a shared rate limit into a failed release.
    #
    # Defaults from the environment so the workflows only need to set GITHUB_TOKEN
    # once at job level rather than threading a parameter through every call.
    [string]$Token = $env:GITHUB_TOKEN
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    throw "OutputDirectory is empty; refusing to write to an unknown location."
}
# Refuse a relative path outright rather than silently resolving it against
# whatever the caller's working directory happens to be.
if (-not [IO.Path]::IsPathRooted($OutputDirectory)) {
    throw "OutputDirectory must be an absolute path, got '$OutputDirectory'."
}
New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
$OutputDirectory = (Resolve-Path -LiteralPath $OutputDirectory).Path
$api = if ($Version -eq "latest") { "https://api.github.com/repos/LiangMu-Studio/context-filter/releases/latest" } else { "https://api.github.com/repos/LiangMu-Studio/context-filter/releases/tags/$Version" }
# Only the API call is authenticated. The asset and checksum downloads below redirect
# to objects.githubusercontent.com, which rejects a request carrying someone else's
# Authorization header — sending it there turns a working download into a 400.
$apiHeaders = @{ "User-Agent" = "gongfei-relay-client" }
if (-not [string]::IsNullOrWhiteSpace($Token)) { $apiHeaders["Authorization"] = "Bearer $Token" }
$release = Invoke-RestMethod -Uri $api -Headers $apiHeaders
$Version = $release.tag_name
$asset = $release.assets | Where-Object { $_.name -eq "context-filter-windows-amd64.zip" } | Select-Object -First 1
if ($null -eq $asset) { throw "Context Filter release $Version has no Windows amd64 package." }

$temp = Join-Path ([IO.Path]::GetTempPath()) ("context-filter-" + [guid]::NewGuid().ToString("N"))
$zip = Join-Path $temp $asset.name
New-Item -ItemType Directory -Path $temp -Force | Out-Null
try {
    $versionFile = Join-Path $OutputDirectory "VERSION"
    $installed = if (Test-Path $versionFile) { (Get-Content -LiteralPath $versionFile -Raw).Trim() } else { "" }
    if ($installed -eq $Version -and (Test-Path (Join-Path $OutputDirectory "context-filter.exe"))) {
        Write-Output "Context Filter $Version is already installed; skipping download."
        return
    }
    & curl.exe -L --fail --retry 2 -A "gongfei-relay-client" -o $zip $asset.browser_download_url
    if ($LASTEXITCODE -ne 0) { throw "Failed to download Context Filter archive." }
    $sumsUrl = "https://github.com/LiangMu-Studio/context-filter/releases/download/$Version/SHA256SUMS"
    $sumsPath = Join-Path $temp "SHA256SUMS"
    & curl.exe -L --fail --retry 2 -A "gongfei-relay-client" -o $sumsPath $sumsUrl
    if ($LASTEXITCODE -ne 0) { throw "Failed to download Context Filter checksums." }
    $sums = Get-Content -LiteralPath $sumsPath -Raw
    $expected = ($sums -split "`n" | Where-Object { $_ -match [regex]::Escape($asset.name) } | Select-Object -First 1) -replace '^\s*([0-9a-fA-F]{64}).*$', '$1'
    $actual = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
    if ([string]::IsNullOrWhiteSpace($expected) -or $actual -ne $expected.ToLowerInvariant()) { throw "Context Filter archive SHA-256 does not match SHA256SUMS." }
    $windowsDirectory = Join-Path $OutputDirectory "windows-x64"
    if (Test-Path $windowsDirectory) { Remove-Item -LiteralPath $windowsDirectory -Recurse -Force }
    New-Item -ItemType Directory -Path $windowsDirectory -Force | Out-Null
    Expand-Archive -LiteralPath $zip -DestinationPath $windowsDirectory -Force
    $exe = Get-ChildItem -LiteralPath $windowsDirectory -Filter context-filter.exe -File -Recurse | Select-Object -First 1
    if ($null -eq $exe) { throw "Downloaded package does not contain context-filter.exe." }
    Copy-Item -LiteralPath $exe.FullName -Destination (Join-Path $OutputDirectory "context-filter.exe") -Force
    Set-Content -LiteralPath $versionFile -Value $Version -NoNewline
    Write-Output "Installed Windows $Version to $OutputDirectory"
}
finally { if (Test-Path $temp) { Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue } }
