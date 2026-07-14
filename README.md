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

The `fused-cli` requires an **API Key** and an **Engine URL** to connect to the Fused data plane. You can set these up once using the `config` command.

```bash
# Set your API Key (from the Fused Dashboard)
fused-cli config set api-key "sk_test_..."

# Set the URL for your Fused Engine
fused-cli config set engine-url "http://localhost:8080"
```

To view your current configuration, run:
```bash
fused-cli config list
```

### Resolution Order
The CLI resolves configuration in the following order (highest precedence first):
1. **Command Line Flags**: `--key` and `--engine-url`
2. **Environment Variables**: `FUSED_API_KEY` and `FUSED_ENGINE_URL`
3. **Config File**: Set via `fused-cli config set` (stored in `~/.config/fused/config.json`)

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
If you are generating a TypeScript MCP server (`--type=mcp`), you can choose to deploy it directly to Fused-Run (a managed service) by passing the `--deploy` flag instead of downloading the source code. The CLI will output the active Fused-Run URL for your AI agents to connect to immediately via SSE. *(Note: Python MCP servers cannot be deployed and must be downloaded locally).*

```bash
fused-cli create --name sales-mcp -t mcp --deploy -d "Read Salesforce leads and fetch Intercom conversations"
```

### Command Reference

#### Global Flags
All commands support the following global flags:

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--key` | | API key (overrides config & `FUSED_API_KEY`) | `""` |
| `--engine-url` | | Fused Engine URL (overrides config & `FUSED_ENGINE_URL`) | `""` |
| `--file` | `-f` | Path to a Fused config file (disables `.fused/` discovery) | `""` |
| `--readme` | | Print the full CLI README text and exit | `false` |

#### `create`
Generate a brand new SDK from natural language.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--name` | `-n` | Name of the generated SDK (e.g., 'stripe-sdk') | `""` |
| `--version` | `-v` | Version of the generated SDK | `"1.0.0"` |
| `--type` | `-t` | Target type for the SDK (e.g., 'sdk', 'mcp') | `"sdk"` |
| `--language` | `-l` | Target language for the SDK (e.g., 'typescript', 'python') | `"typescript"` |
| `--deploy` | | Deploy to Fused Sandbox immediately (MCP TypeScript only) | `false` |
| `--yes` | `-y` | Skip interactive menu and automatically proceed | `false` |
| `--description` | `-d` | Description of the SDK to create | `""` |
| `--output` | `-o` | Directory to save the generated SDK zip | `"."` |

#### `config`
Manage your local CLI configuration (`set`, `get`, `list`, `reset`). Inherits global flags.

#### `sdk plan`
Preview changes that will be made to your Fused environment for SDKs.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan receipt JSON instead of writing default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |

#### `sdk apply`
Apply a generated plan to deploy SDK changes.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--download` | | Download generated SDK after apply | `false` |
| `--plan-id` | | Apply a specific remote plan ID for a single SDK config | `""` |
| `--receipt` | | Read a specific plan receipt for a single SDK config | `""` |

#### `sdk validate`
Validates an SDK configuration file. Inherits global flags.

#### `sdk download`
Download a generated SDK manually.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--out` | `-o` | Output directory for the SDK | `"."` |

#### `sdk add-service`
Add a service to an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Specific version to use for the service | `""` |

#### `sdk add-operation`
Add an operation to an existing service in an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive operation selection | `false` |
| `--apply` | | Apply changes after adding operation | `false` |
| `--download` | | Download SDK after apply (implies `--apply`) | `false` |

#### `sdk remove-operation`
Remove an operation from an existing service in an SDK configuration. Inherits global flags.

#### `workspace plan`
Preview changes that will be made to your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan receipt JSON instead of writing default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |

#### `workspace apply`
Apply a generated plan to activate Workspace changes.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID | `""` |
| `--receipt` | | Read a specific plan receipt | `""` |

#### `workspace services list`
List workspace services. Inherits global flags.

#### `workspace service add`
Add a service to your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Default version to add | `""` |

#### `workspace service remove`
Remove a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal when the generated plan action is applied | `false` |

#### `workspace service deprecate`
Deprecate a service in your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--at` | | Deprecation effective date in YYYY-MM-DD | `""` |
| `--reason` | | Reason for deprecation | `""` |

#### `workspace service-version add`
Add an allowed version to a workspace service. Inherits global flags.

#### `workspace service-version remove`
Remove a specific version of a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal | `false` |

#### `workspace service-version deprecate`
Deprecate a specific version of a service in your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--at` | | Deprecation effective date in YYYY-MM-DD | `""` |
| `--reason` | | Reason for deprecation | `""` |

#### Global `plan` / `apply` / `validate`
The CLI also supports top-level `plan`, `apply`, and `validate` commands to process all configurations (both SDKs and workspaces).

**`validate`**
Validates the syntax and references for all Fused configurations in the target directory or file. Inherits global flags.

**`plan`**

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan receipt JSON instead of writing default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |

**`apply`**

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--download` | | Download generated SDKs after apply | `false` |
| `--plan-id` | | Apply a specific remote plan ID for a single config | `""` |
| `--receipt` | | Read a specific plan receipt for a single config | `""` |

## Config-as-Code (GitOps)

Fused now supports managing your SDKs and Workspace service whitelists via declarative YAML configurations stored in a `.fused/` folder in your repository.

### Defining an SDK

Create `.fused/sdks/my-sdk.yaml`:
```yaml
kind: "sdk"
version: 1
name: "my-sdk"
sdkVersion: "1.0.0"
language: "typescript"
target: "sdk"
services:
  okta:
    version: "2026-07-09"
    operations:
      - "listLogEvents"
      - "getUser"
```

The `operations` values are OpenAPI `operationId`s for the selected service version. To browse available operations and add several at once:

```bash
fused-cli sdk add-service okta -f .fused/sdks/my-sdk.yaml --version 2026-07-09
fused-cli sdk add-operation -f .fused/sdks/my-sdk.yaml --interactive
```

`add-service` creates or updates the service entry; the config becomes valid once that service has at least one operationId.

### Defining a Workspace Configuration

You can also manage the services activated for your entire workspace via config.

Create `.fused/workspace.yaml`:
```yaml
kind: "workspace"
version: 1
services:
  stripe:
    versions:
      - "2026-07-09"
      - "2026-08-01"
    default: "2026-08-01"
  okta:
    versions:
      - "1.0.0"
```
The service keys are Registry service slugs. Engine resolves those slugs to service IDs during workspace planning, so teams do not need to know UUIDs. If `default` is not provided, the Engine will automatically pin the latest version in the `versions` array as the default. You can populate this file automatically by using the `fused-cli workspace service add <slug>` command.

### Applying the Config

You can preview the changes that will be made to your Fused environment and generate a deployment plan. For SDKs:
```bash
fused-cli sdk plan
```

For Workspaces:
```bash
fused-cli workspace plan
```

Once you're satisfied with the plan, you can apply it. The CLI will automatically trigger the respective processes (e.g., Registry generation for SDKs, or Engine activations for Workspaces):
```bash
fused-cli sdk apply --download
```
Using the `--download` flag automatically fetches the compiled SDK binary into your local directory upon successful generation.

```bash
fused-cli workspace apply
```

Run `fused-cli --help` for more information on available commands and flags.
