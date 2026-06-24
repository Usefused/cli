# Fused CLI

The `fused-cli` is the official command-line interface for [Fused](https://usefused.com). It allows you to rapidly generate hyper-targeted API SDKs directly from natural language using the power of AI.

## Installation

### Option 1: One-Line Install Script (Recommended)

#### macOS / Linux

```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | bash
```

Install a specific version:

```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | VERSION=v1.0.0 bash
```

The binary is installed to `/usr/local/bin`. If that directory is not already on your `PATH`, the script will print the exact line to add to your shell profile.

#### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex
```

Install a specific version:

```powershell
$env:VERSION="v1.0.0"; irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex
```

The binary is installed to `%LOCALAPPDATA%\Programs\fused-cli` and that directory is automatically added to your user `PATH`.

### Option 2: Go Install

If you have Go installed on your machine, you can install the latest version directly via:

```bash
go install github.com/Usefused/cli@latest
```

### Option 3: Manual Download

Head over to the [Releases](https://github.com/Usefused/cli/releases) page and download the `.tar.gz` (macOS/Linux) or `.zip` (Windows) file for your operating system and architecture. Extract the binary and place it in a directory on your `PATH`:

- macOS / Linux: `/usr/local/bin`
- Windows: `%LOCALAPPDATA%\Programs\fused-cli` (then add it to your user `PATH` via System Properties → Environment Variables)

## Configuration

To use `fused-cli`, you need to set your API Key. You can get your API Key from the Fused Dashboard.

Export it as an environment variable in your terminal (or add it to your `~/.bashrc` / `~/.zshrc`):

```bash
export FUSED_API_KEY="sk_test_..."
```

*(You can also pass it explicitly with the `--api-key` flag)*

## Usage

### Generate an SDK (`create`)

The `create` command uses Fused intent AI to turn a business use case into a Business Capability SDK. Describe the workflow your team wants to ship, and Fused maps the right services and endpoints into a single, scoped SDK with the authentication, retries, tracing, and typed errors wired in.

```bash
# Generate an SDK around a business capability
fused-cli create --name onboarding-sdk --version 1.0.0 -d "When a new employee joins, use Jira to create an onboarding ticket, use GitHub to provision repository access, and use Slack to send a welcome message"
```

Fused parses the use case, uses the services you name for each task, selects the relevant operations, and opens an interactive Cart UI where you can review, add, or refine the SDK before downloading the generated `.zip` file to your current directory.

### Available Commands
- `create`: Generate a brand new SDK from natural language
- `update`: Update an existing Fused SDK directory

Run `fused-cli --help` for more information on available commands and flags.
