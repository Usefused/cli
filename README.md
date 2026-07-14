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

### Import an API Spec (`import`)

The `import` command registers (or updates) a service in the Registry directly from a supported API specification -- no conversational import agent, no endpoint-picking prompt, always the whole spec. This is the non-interactive path for teams adding their own internal service to Fused, e.g. from a CI step.

The Registry auto-detects the source format; no `--format` flag is required. Supported formats are:

- OpenAPI
- AsyncAPI
- Postman Collection
- GraphQL SDL or an introspectable GraphQL endpoint

```bash
# Plan: parse the spec, diff it against the live service (if one exists via --slug),
# and pick a publish strategy -- read-only, nothing is written yet.
fused-cli import plan ./openapi.json --name "Internal Billing API" --slug billing-api

# The same command accepts an online source directly.
fused-cli import plan https://developer.example.com/asyncapi.yaml --name "Events API" --slug events-api

# Apply: commit the most recently planned import.
fused-cli import apply
```

`<spec-path-or-url>` can be a local file path or an `http(s)://` URL -- a local file's contents are read and sent directly; a URL is processed by the Registry. Registry first tries a normal `GET`; if the response is not a recognized specification, it sends a standard GraphQL introspection query to the same URL. A successful introspection import also uses that URL as the service's GraphQL base URL.

`--slug` is required and is the service identity chosen by the producer. Slugs are unique within the producer's account, so another account may use the same slug under its own provider namespace. If the slug already exists in the caller's account, `import plan` updates that service; otherwise it creates a service with that exact slug. It never guesses from `--name`, because display names are not unique. Updating an existing service also auto-selects a strategy from the spec's declared version: the same version re-imported merges into the live default in place, while a bumped version becomes a new version alongside it. If anything (a generated SDK or workspace) is pinned to the version about to be modified, `import plan` reports it without blocking `plan` or `apply`.

```bash
fused-cli import plan ./openapi.json --name "Internal Billing API" --slug billing-api
# Update to "Internal Billing API" (slug: billing-api, strategy: modify_existing) -- plan ID: 5e1e...
# Diff: 0 added, 2 changed, 0 removed
#   ~ createInvoice
#   ~ listInvoices
# 1 SDKs / 0 workspaces use this version:
#   - SDK billing-sdk (uses a changed/removed endpoint)
# Run `fused import apply` to commit this plan.

fused-cli import apply
# Applied modify_existing to service 3f8c... (version 2026-07-14)
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

#### `sdk sync`
Full-mirror a local SDK config from the most recently generated remote SDK with the given name.

Usage: `fused-cli sdk sync <name> -f .fused/sdks/<name>.yaml`

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

#### `workspace sync`
Full-mirror the local workspace config from the Engine's current activation state. Engine state wins: services activated remotely are added or updated locally, and local services no longer activated remotely are removed.

Usage: `fused-cli workspace sync -f .fused/workspace.yaml`

#### `workspace services list`
List workspace services along with their enabled versions.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive service selection | `false` |

#### `workspace has`
Check if a specific service is available in the workspace and output its enabled versions.
Usage: `fused-cli workspace has <service_name>`

#### `workspace service add`
Add a service to your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Default version to add | `""` |
| `--service-id` | | Optional service ID to store when editing a local config directly | `""` |

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

#### `import plan`
Parse an auto-detected OpenAPI, AsyncAPI, Postman Collection, GraphQL SDL, or introspectable GraphQL endpoint (local file or URL), resolve create-vs-update via `--slug`, diff it against the live service, and pick a publish strategy. Read-only.

Usage: `fused-cli import plan <spec-path-or-url>`

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--name` | | Service name (required) | `""` |
| `--slug` | | Service slug to create or update (required; unique within your account) | `""` |
| `--public` | | Mark a new service public | `false` |
| `--category` | | Category for a new service | `""` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |
| `--json` | | Print the raw plan response as JSON instead of a summary | `false` |

#### `import apply`
Commit the plan produced by `import plan`: a new service goes live immediately; an existing service is appended and published in the same request, using the strategy already resolved at plan time.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID (requires `--source-hash`) | `""` |
| `--source-hash` | | Source hash to pair with `--plan-id` | `""` |
| `--receipt` | | Read a specific plan receipt (default: most recent local receipt) | `""` |

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
The service keys are Registry service slugs. Engine resolves those slugs to service IDs during workspace planning, so teams do not need to know UUIDs. If `default` is not provided, the Engine will automatically pin the latest version in the `versions` array as the default. To edit this file directly from the CLI, use `fused-cli workspace service add <slug> --version <version> -f .fused/workspace.yaml`.

### Syncing Local Config From Remote State

UI actions and service imports can update the Engine or Registry before your local YAML knows about the change. Use sync when you want local config-as-code to mirror the current remote truth:

```bash
fused-cli workspace sync -f .fused/workspace.yaml
fused-cli sdk sync my-sdk -f .fused/sdks/my-sdk.yaml
```

`workspace sync` mirrors the Engine's active workspace services. `sdk sync` mirrors the most recently generated SDK with the given name, including its selected services, resolved versions, and operation names.

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
