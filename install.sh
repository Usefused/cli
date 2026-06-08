#!/bin/bash
set -e

# Fused CLI Installation Script
# This script detects the OS and Architecture, downloads the latest release from GitHub,
# and installs the `fused-cli` binary to /usr/local/bin.

REPO="Usefused/cli"
BINARY="fused-cli"
INSTALL_DIR="/usr/local/bin"

# Detect OS
OS="$(uname -s)"
case "${OS}" in
    Linux*)     OS="Linux";;
    Darwin*)    OS="Darwin";;
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

# Fetch the latest release tag from GitHub API
echo "=> Fetching latest release version..."
LATEST_TAG=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_TAG" ]; then
    echo "Error: Could not fetch latest release version from GitHub."
    exit 1
fi

echo "=> Latest version is ${LATEST_TAG}"

# Construct the download URL based on GoReleaser naming convention
# Example: fused-cli_Darwin_arm64.tar.gz
TAR_NAME="${BINARY}_${OS}_${ARCH}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${TAR_NAME}"

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

echo "=> Installation complete! 🎉"
echo "=> Run 'fused-cli --help' to get started."
