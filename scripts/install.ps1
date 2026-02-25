# Amurg install script for Windows — downloads a pre-built binary from GitHub Releases.
#
# Usage (PowerShell):
#   irm https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.ps1 | iex
#   irm https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.ps1 | iex -Binary amurg-hub
#
# Or save and run with parameters:
#   .\install.ps1 -Binary amurg-runtime -Version 0.1.0

param(
    [string]$Binary = "amurg-runtime",
    [string]$Version = "",
    [string]$InstallDir = ""
)

$ErrorActionPreference = "Stop"
$Repo = "amurg-ai/amurg"

function Info($msg)  { Write-Host "  → $msg" -ForegroundColor Blue }
function Ok($msg)    { Write-Host "  ✓ $msg" -ForegroundColor Green }
function Err($msg)   { Write-Host "  ✗ $msg" -ForegroundColor Red }
function Fatal($msg) { Err $msg; exit 1 }

# ── Detection ────────────────────────────────────────────────────────────────

function Get-Arch {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    switch ($arch) {
        "X64"   { return "amd64" }
        "Arm64" { return "arm64" }
        default { Fatal "Unsupported architecture: $arch. Only amd64 and arm64 are supported." }
    }
}

function Get-InstallDir {
    if ($InstallDir -ne "") { return $InstallDir }

    $dir = Join-Path $env:LOCALAPPDATA "amurg\bin"
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
    return $dir
}

# ── Version ──────────────────────────────────────────────────────────────────

function Get-LatestVersion {
    if ($Version -ne "") { return $Version }

    Info "Fetching latest version..."
    $url = "https://api.github.com/repos/$Repo/releases/latest"
    try {
        $release = Invoke-RestMethod -Uri $url -Headers @{ "User-Agent" = "amurg-installer" }
        $tag = $release.tag_name -replace "^v", ""
        if ([string]::IsNullOrEmpty($tag)) {
            Fatal "Could not determine latest version. Specify one with -Version X.Y.Z"
        }
        return $tag
    }
    catch {
        Fatal "Failed to fetch latest release info from GitHub: $_"
    }
}

# ── Install ──────────────────────────────────────────────────────────────────

function Do-Install {
    $arch = Get-Arch
    $dir = Get-InstallDir
    $ver = Get-LatestVersion

    $baseUrl = "https://github.com/$Repo/releases/download/v$ver"
    $archive = "${Binary}_${ver}_windows_${arch}.zip"
    $archiveUrl = "$baseUrl/$archive"
    $checksumUrl = "$baseUrl/checksums.txt"

    $tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "amurg-install-$(Get-Random)"
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

    try {
        Info "Downloading $Binary v$ver for windows/$arch..."
        $archivePath = Join-Path $tmpDir $archive
        try {
            Invoke-WebRequest -Uri $archiveUrl -OutFile $archivePath -UseBasicParsing
        }
        catch {
            Fatal "Download failed. Check that v$ver exists at https://github.com/$Repo/releases"
        }

        Info "Verifying checksum..."
        $checksumPath = Join-Path $tmpDir "checksums.txt"
        try {
            Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumPath -UseBasicParsing
        }
        catch {
            Fatal "Failed to download checksums."
        }

        $checksumLine = Get-Content $checksumPath | Where-Object { $_ -match $archive }
        if (-not $checksumLine) {
            Fatal "Checksum not found for $archive in checksums.txt"
        }
        $expected = ($checksumLine -split '\s+')[0]

        $actual = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
        if ($expected -ne $actual) {
            Fatal "Checksum mismatch!`n  Expected: $expected`n  Actual:   $actual"
        }

        Info "Extracting..."
        Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force

        Info "Installing to $dir\$Binary.exe..."
        $src = Join-Path $tmpDir "$Binary.exe"
        $dest = Join-Path $dir "$Binary.exe"
        Copy-Item -Path $src -Destination $dest -Force
    }
    finally {
        Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
    }

    # Check if install dir is in PATH.
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$dir*") {
        Info "Adding $dir to your user PATH..."
        [Environment]::SetEnvironmentVariable("Path", "$userPath;$dir", "User")
        $env:Path = "$env:Path;$dir"
        Ok "Added to PATH. Restart your terminal for it to take effect."
    }

    Write-Host ""
    Ok "$Binary v$ver installed to $dest"
    Write-Host ""
    Info "Next steps:"
    Write-Host "    $Binary init      # interactive setup wizard"
    Write-Host "    $Binary run       # start with generated config"
    Write-Host ""
}

# ── Main ─────────────────────────────────────────────────────────────────────

Do-Install
