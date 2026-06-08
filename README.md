# Fused CLI

The `fused-cli` is the official command-line interface for OpenSync. It allows you to rapidly generate hyper-targeted API SDKs directly from natural language using the power of AI.

## Installation

### Option 1: One-Line Install Script (Recommended)

**Install a specific version:**
```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | VERSION=v1.0.0 bash
```

You can easily install the pre-compiled binary for macOS or Linux using our install script:
```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | bash
```

### Option 2: Go Install
If you have Go installed on your machine, you can install the latest version directly via:
```bash
go install github.com/Usefused/cli@latest
```

### Option 3: Manual Download
Head over to the [Releases](https://github.com/Usefused/cli/releases) page and download the `.tar.gz` or `.zip` file for your operating system and architecture. Extract the binary and move it to `/usr/local/bin` (or any directory in your `$PATH`).

## Configuration

To use `fused-cli`, you need to set your API Key. You can get your API Key from the Fused Dashboard.

Export it as an environment variable in your terminal (or add it to your `~/.bashrc` / `~/.zshrc`):
```bash
export FUSED_API_KEY="sk_test_..."
```

*(You can also pass it explicitly with the `--api-key` flag)*

## Usage

### Generate an SDK (`create`)

The `create` command uses AI to understand your exact intent and instantly compiles a perfectly scoped SDK containing only the endpoints you need.

```bash
# Generate an SDK by describing what you want to build
fused-cli create --name stripe-sdk --version 1.0.0 -d "I want to accept payments using Stripe and use Plunk for sending emails"
```

The AI will parse your intent, map the endpoints, and provide an interactive Cart UI where you can review, add, or refine the endpoints before downloading the generated SDK `.zip` file to your current directory!

### Available Commands
- `create`: Generate a brand new SDK from natural language
- `update`: Update an existing Fused SDK directory

Run `fused-cli --help` for more information on available commands and flags.

## Developing & Releasing

To create a new release for users to download:
1. Make sure your changes are merged to the `main` branch.
2. From the `cli` directory, run:
```bash
make release VERSION=v1.0.0
```
3. GitHub Actions will automatically compile the binaries for Linux, macOS, and Windows and publish them to the GitHub Releases page!
