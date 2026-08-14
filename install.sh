#!/bin/bash
set -e

# Fused CLI Installation Script
# This script detects the OS and Architecture, downloads the latest release from GitHub,
# and installs the `fused-cli` binary to /usr/local/bin.
# For Windows, use install.ps1 instead.

REPO="Usefused/cli"
BINARY="fused-cli"
INSTALL_DIR="${FUSED_CLI_INSTALL_DIR:-${HOME}/.local/bin}"

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
RELEASE_BASE_URL="https://github.com/${REPO}/releases/download/${TARGET_VERSION}"
DOWNLOAD_URL="${RELEASE_BASE_URL}/${TAR_NAME}"
CHECKSUMS_URL="${RELEASE_BASE_URL}/checksums.txt"

# This script is most commonly run piped from curl (curl ... | bash), which
# means it downloads and executes a remote release with root (via sudo) and
# no code review in between. Require an explicit, informed confirmation
# before it touches the filesystem -- skippable for automation/CI via
# -y/--yes or ASSUME_YES=1 (e.g. `curl ... | ASSUME_YES=1 bash`).
ASSUME_YES="${ASSUME_YES:-0}"
for arg in "$@"; do
    case "$arg" in
        -y|--yes) ASSUME_YES=1 ;;
    esac
done

if [ "$ASSUME_YES" -ne 1 ]; then
    echo ""
    echo "About to download and install:"
    echo "  ${DOWNLOAD_URL}"
    echo "  -> ${INSTALL_DIR}/${BINARY}"
    if [ -r /dev/tty ]; then
        printf "Proceed? [y/N] "
        read -r REPLY < /dev/tty
        case "$REPLY" in
            y|Y|yes|YES) ;;
            *) echo "Aborted."; exit 1 ;;
        esac
    else
        echo "Error: no terminal available to confirm interactively."
        echo "Re-run with -y/--yes, or ASSUME_YES=1, to skip this prompt:"
        echo "  curl -sSL https://raw.githubusercontent.com/${REPO}/main/install.sh | ASSUME_YES=1 bash"
        exit 1
    fi
fi

# Create a temporary directory
TMP_DIR=$(mktemp -d)
cd "$TMP_DIR"

echo "=> Downloading ${DOWNLOAD_URL}..."
curl -sL -o "${TAR_NAME}" "${DOWNLOAD_URL}"

# Verify the archive against the release's published checksums before
# extracting/running anything from it. Best-effort: older releases without a
# checksums.txt just skip verification rather than blocking the install.
if curl -sL -o "checksums.txt" "${CHECKSUMS_URL}" && [ -s "checksums.txt" ]; then
    echo "=> Verifying checksum..."
    if ! sha256sum --ignore-missing --check checksums.txt 2>/dev/null; then
        echo "Error: checksum verification failed for ${TAR_NAME}. Aborting install."
        exit 1
    fi
else
    echo "=> No checksums.txt found for ${TARGET_VERSION}; skipping checksum verification."
fi

# Extract the archive
echo "=> Extracting archive..."
tar -xzf "${TAR_NAME}"

SKILL_VERSION="${TARGET_VERSION#v}"
SKILLS_SOURCE="skills/${SKILL_VERSION}"
HAS_BUNDLED_SKILLS=0
if [ -f "${SKILLS_SOURCE}/fused-cli/SKILL.md" ]; then
    HAS_BUNDLED_SKILLS=1
else
    echo "=> This older release has no bundled skills; the CLI will use its fallback source."
fi

# Ensure the install directory exists
mkdir -p "${INSTALL_DIR}"

# Move the binary to the install directory
echo "=> Installing to ${INSTALL_DIR}/${BINARY}..."
mv "${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

# Keep the immutable skill snapshot beside the executable. The CLI resolves
# this copy first, so installation works offline and cannot drift from its binary.
if [ "$HAS_BUNDLED_SKILLS" -eq 1 ]; then
    SKILLS_DEST="${INSTALL_DIR}/skills/${SKILL_VERSION}"
    mkdir -p "${SKILLS_DEST}"
    cp -R "${SKILLS_SOURCE}/." "${SKILLS_DEST}/"
    echo "=> Installed bundled skills to ${SKILLS_DEST}"
fi

echo "=> Note: Installed to ${INSTALL_DIR} to avoid requiring sudo."
echo "=> If you prefer a system-wide installation, you can move it to /usr/local/bin using sudo."

# Clean up
cd - > /dev/null
rm -rf "$TMP_DIR"

echo "=> Installation complete!"
echo "=> Run 'fused-cli --help' to get started."

# Best-effort hint only -- never write into another tool's config on the
# user's behalf. We don't know which agent/app (if any) they use, so just
# point at the explicit, opt-in command for whichever ones look present.
# Each entry: binary:--for-value:display name
AGENT_HINTS=(
    "claude:claude:Claude Code"
    "codex:codex:Codex"
    "cursor:cursor:Cursor"
    "windsurf:windsurf:Windsurf"
    "agy:antigravity:Antigravity"
)
for hint in "${AGENT_HINTS[@]}"; do
    bin="${hint%%:*}"
    rest="${hint#*:}"
    forval="${rest%%:*}"
    name="${rest#*:}"
    if command -v "$bin" >/dev/null 2>&1; then
        echo ""
        echo "=> Detected ${name}. Run 'fused-cli skill install --for ${forval}' to add"
        echo "   a skill that teaches it to configure Fused workspaces, connection/"
        echo "   execution policies, and SDK/MCP configs."
    fi
done

# Verify the install directory is on PATH
if ! echo ":${PATH}:" | grep -q ":${INSTALL_DIR}:"; then
    echo ""
    echo "WARNING: ${INSTALL_DIR} is not in your PATH."
    echo "Add the following line to your ~/.bashrc or ~/.zshrc and restart your terminal:"
    echo "  export PATH=\"\$PATH:${INSTALL_DIR}\""
fi
