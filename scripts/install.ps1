# install.ps1 — install the latchet CLI on Windows (PowerShell 5.1+).
#
# One-liner:
#   iwr -useb https://raw.githubusercontent.com/thowd22/latchet/main/scripts/install.ps1 | iex
#
# Optional environment variables:
#   $env:LATCHET_VERSION       release tag to install (default: latest)
#   $env:LATCHET_INSTALL_DIR   target directory (default: $env:LOCALAPPDATA\Programs\latchet)

#Requires -Version 5.1
$ErrorActionPreference = 'Stop'

$repo = 'thowd22/latchet'
$version = if ($env:LATCHET_VERSION) { $env:LATCHET_VERSION } else { 'latest' }

# Detect arch.
$archEnum = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
switch ($archEnum) {
    'X64'   { $arch = 'amd64' }
    'Arm64' { $arch = 'arm64' }
    default { throw "unsupported architecture: $archEnum" }
}

# Resolve version.
if ($version -eq 'latest') {
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -UseBasicParsing
    $version = $rel.tag_name
    if (-not $version) { throw "could not determine latest release tag" }
}

# Install dir.
$destDir = if ($env:LATCHET_INSTALL_DIR) {
    $env:LATCHET_INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA 'Programs\latchet'
}
New-Item -ItemType Directory -Force -Path $destDir | Out-Null

$archive = "latchet-$version-windows-$arch.zip"
$urlBase = "https://github.com/$repo/releases/download/$version"

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "==> downloading $archive"
    $archivePath = Join-Path $tmp $archive
    Invoke-WebRequest -Uri "$urlBase/$archive" -OutFile $archivePath -UseBasicParsing

    Write-Host "==> downloading SHA256SUMS"
    $sumsPath = Join-Path $tmp 'SHA256SUMS'
    Invoke-WebRequest -Uri "$urlBase/SHA256SUMS" -OutFile $sumsPath -UseBasicParsing

    $line = Get-Content $sumsPath |
        Where-Object { $_ -match "\s$([regex]::Escape($archive))$" } |
        Select-Object -First 1
    if (-not $line) { throw "no checksum entry for $archive in SHA256SUMS" }
    $expected = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash -Algorithm SHA256 -Path $archivePath).Hash.ToLower()
    if ($expected -ne $got) { throw "checksum mismatch: expected $expected, got $got" }

    Expand-Archive -Path $archivePath -DestinationPath $tmp -Force
    $exeSrc = Join-Path $tmp 'latchet.exe'
    $exeDst = Join-Path $destDir 'latchet.exe'
    Move-Item -Force -Path $exeSrc -Destination $exeDst
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "installed: $exeDst"

# Append to user PATH if missing.
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$userPathParts = if ($userPath) { $userPath -split ';' } else { @() }
if ($userPathParts -notcontains $destDir) {
    $newPath = (@($destDir) + $userPathParts) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    Write-Host ""
    Write-Host "added $destDir to your user PATH"
    Write-Host "open a new PowerShell window for the change to take effect"
}

Write-Host ""
& $exeDst -version
