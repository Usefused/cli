#!/bin/bash
set -e

# Fused CLI Installation Script
# This script detects the OS and Architecture, downloads the latest release from GitHub,
# and installs the `fused-cli` binary to /usr/local/bin.
# For Windows, use install.ps1 instead.

REPO="Usefused/cli"
BINARY="fused-cli"
INSTALL_DIR="/usr/local/bin"

# Detect OS
OS="$(uname -s)"
case "${OS}" in
    Linux*)     OS="Linux";;
    Darwin*)    OS="Darwin";;
    MINGW*|MSYS*|CYGWIN*)
        echo "Windows detected. Please use the PowerShell install script instead:"
        echo "  irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex"
        exit 1;;
    *)          echo "Unsupported operating system: ${OS}"; exit 1;;
esac

# Detect Architecture
ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64)     ARCH="x86_64";;
    arm64|aarch64) ARCH="arm64";;
    *)          echo "Unsupported architecture: ${ARCH}"; exit 1;;
esac

echo "=> Detected ${OS} ${ARCH}"

# Determine version to install
if [ -n "$VERSION" ]; then
    TARGET_VERSION="$VERSION"
    echo "=> Using specified version ${TARGET_VERSION}"
else
    echo "=> Fetching latest release version..."
    TARGET_VERSION=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
fi

if [ -z "$TARGET_VERSION" ]; then
    echo "Error: Could not determine release version."
    exit 1
fi

echo "=> Installing version ${TARGET_VERSION}"

# Construct the download URL based on GoReleaser naming convention
# Example: fused-cli_Darwin_arm64.tar.gz
TAR_NAME="${BINARY}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${TARGET_VERSION}/${TAR_NAME}"

# Create a temporary directory
TMP_DIR=$(mktemp -d)
cd "$TMP_DIR"

echo "=> Downloading ${DOWNLOAD_URL}..."
curl -sL -o "${TAR_NAME}" "${DOWNLOAD_URL}"

# Extract the archive
echo "=> Extracting archive..."
tar -xzf "${TAR_NAME}"

# Move the binary to the install directory
echo "=> Installing to ${INSTALL_DIR}/${BINARY} (requires sudo)..."
sudo mv "${BINARY}" "${INSTALL_DIR}/${BINARY}"
sudo chmod +x "${INSTALL_DIR}/${BINARY}"

# Clean up
cd - > /dev/null
rm -rf "$TMP_DIR"

echo "=> Installation complete!"
echo "=> Run 'fused-cli --help' to get started."

# Verify the install directory is on PATH
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
    echo ""
    echo "WARNING: ${INSTALL_DIR} is not in your PATH."
    echo "Add the following line to your ~/.bashrc or ~/.zshrc and restart your terminal:"
    echo "  export PATH=\"\$PATH:${INSTALL_DIR}\""
fi
