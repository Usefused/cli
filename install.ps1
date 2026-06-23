# Fused CLI Installation Script for Windows
# Run with: irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex
# Or to install a specific version:
#   $env:VERSION="v1.0.0"; irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex

$ErrorActionPreference = "Stop"

$Repo    = "Usefused/cli"
$Binary  = "fused-cli"
$InstallDir = "$env:LOCALAPPDATA\Programs\fused-cli"

# Detect architecture
$Arch = if ([System.Environment]::Is64BitOperatingSystem) { "x86_64" } else {
    Write-Error "Unsupported architecture. Only x86_64 is supported on Windows."
    exit 1
}

Write-Host "=> Detected Windows $Arch"

# Determine version
$TargetVersion = $env:VERSION
if (-not $TargetVersion) {
    Write-Host "=> Fetching latest release version..."
    $Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
    $TargetVersion = $Release.tag_name
}

if (-not $TargetVersion) {
    Write-Error "Could not determine release version."
    exit 1
}

Write-Host "=> Installing version $TargetVersion"

# Construct download URL (GoReleaser naming: fused-cli_Windows_x86_64.zip)
$ZipName = "${Binary}_Windows_${Arch}.zip"
$DownloadUrl = "https://github.com/$Repo/releases/download/$TargetVersion/$ZipName"

# Create a temporary directory
$TmpDir = New-TemporaryFile | ForEach-Object { Remove-Item $_; New-Item -ItemType Directory -Path $_ }

try {
    $ZipPath = Join-Path $TmpDir $ZipName
    Write-Host "=> Downloading $DownloadUrl..."
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $ZipPath -UseBasicParsing

    Write-Host "=> Extracting archive..."
    Expand-Archive -Path $ZipPath -DestinationPath $TmpDir -Force

    # Create install directory and move binary
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir | Out-Null
    }

    $BinaryPath = Join-Path $TmpDir "${Binary}.exe"
    Write-Host "=> Installing to $InstallDir..."
    Move-Item -Path $BinaryPath -Destination (Join-Path $InstallDir "${Binary}.exe") -Force
} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}

# Add to user PATH if not already present
$UserPath = [System.Environment]::GetEnvironmentVariable("PATH", "User")
if ($UserPath -notlike "*$InstallDir*") {
    Write-Host "=> Adding $InstallDir to your user PATH..."
    [System.Environment]::SetEnvironmentVariable(
        "PATH",
        "$UserPath;$InstallDir",
        "User"
    )
    $env:PATH = "$env:PATH;$InstallDir"
    Write-Host "=> PATH updated. You may need to restart your terminal for the change to take effect."
} else {
    Write-Host "=> $InstallDir is already on your PATH."
}

Write-Host ""
Write-Host "=> Installation complete!"
Write-Host "=> Run 'fused-cli --help' to get started."
