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
# Generate a standard SDK around a business capability
fused-cli create --name onboarding-sdk --version 1.0.0 -d "When a new employee joins, use Jira to create an onboarding ticket, use GitHub to provision repository access, and use Slack to send a welcome message"
```

Fused parses the use case, uses the services you name for each task, selects the relevant operations, and opens an interactive Cart UI where you can review, add, or refine the SDK before either downloading the generated `.zip` file to your current directory or deploying it directly as an MCP server.

#### Targets and Languages
You can specify the type of integration and its programming language:
- `--type` (or `-t`): Set the target type. Options are `sdk` (default) or `mcp`.
- `--language` (or `-l`): Set the programming language. Options include `typescript` (default) and `python`.
- `-y` (or `--yes`): Skip the interactive Cart UI and automatically proceed with generation (non-interactive mode).

```bash
# Generate a Python MCP server non-interactively
fused-cli create --name support-agent-mcp -t mcp -l python -y -d "Search Zendesk for tickets and use Linear to update corresponding issues"
```

#### Deploying MCP Servers
If you are generating a TypeScript MCP server (`--type=mcp`), you can choose to deploy it directly to the Fused Sandbox by passing the `--deploy` flag instead of downloading the source code. The CLI will output the active Sandbox URL for your AI agents to connect to immediately via SSE. *(Note: Python MCP servers cannot be deployed and must be downloaded locally).*

```bash
fused-cli create --name sales-mcp -t mcp --deploy -d "Read Salesforce leads and fetch Intercom conversations"
```

### Update an SDK (`update`)

The `update` command allows you to seamlessly iterate on an existing SDK. By default, it will look up your most recently generated SDK with that name and use its configurations as the baseline. 

```bash
# Update the most recent 'support-agent-mcp' SDK
fused-cli update support-agent-mcp

# Update a specific version of the SDK
fused-cli update support-agent-mcp@1.0.0
```
Just like `create`, you can also specify the target language and whether to deploy it:
```bash
fused-cli update support-agent-mcp -t mcp -l python
```

### Download an SDK (`download`)

If you've already generated an SDK (perhaps via the web UI or an earlier CLI session) and just need to download the `.zip` archive or extract the source code locally, use the `download` command.

```bash
# Download the most recently generated 'sales-mcp'
fused-cli download sales-mcp

# Download a specific version
fused-cli download sales-mcp@1.2.0

# Download and output to a specific directory
fused-cli download sales-mcp@1.2.0 --output ./my-agent
```

### Available Commands
- `create`: Generate a brand new SDK from natural language
- `update`: Update an existing SDK by its ID or name. You can specify a version by appending `@<version>` (e.g., `fused-cli update my-sdk@1.2.0`). (Supports `--type`, `--language`, and `--deploy` flags).
- `download`: Download an already built SDK by its ID or name. You can specify a version by appending `@<version>` (e.g., `fused-cli download my-sdk@1.2.0`).

Run `fused-cli --help` for more information on available commands and flags.
