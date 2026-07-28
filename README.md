# Fused CLI

The `fused-cli` is the official command-line interface for [Fused](https://usefused.com). It manages a Fused Engine from your terminal: connect the CLI to an Engine, import API services into the Registry, apply workspace configuration, manage buckets and secrets, configure webhooks, and operate SDK or MCP artifacts when you need them.

Use it as the config-as-code and operations CLI for the Fused integration layer. SDK and MCP generation are supported workflows, not the whole product.

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

The `fused-cli` requires an **API Key** and an **Engine URL** to connect to the Fused data plane. If you do not have an Engine running yet, install or run one from the [Fused Engine releases](https://github.com/Usefused/engine/releases).

```bash
# Set the URL for your Fused Engine
fused-cli config set engine-url "http://localhost:8081"

# Set the key the Engine should use to authenticate CLI requests
fused-cli config set api-key "<provided-by-fused>"
```

To view your current configuration, run:
```bash
fused-cli config list
```

To confirm the CLI is talking to the Engine, run:

```bash
fused-cli workspace services list
```

### Resolution Order
The CLI resolves configuration in the following order (highest precedence first):
1. **Command Line Flags**: `--key` and `--engine-url`
2. **Environment Variables**: `FUSED_API_KEY` and `FUSED_ENGINE_URL`
3. **Config File**: Set via `fused-cli config set` (stored in `~/.config/fused/config.json`)

## Usage

### Common Workflows

```bash
# Import a provider API into the Registry
fused-cli import plan ./openapi.json --name "Internal Billing API" --slug billing-api
fused-cli import apply

# Review and apply workspace config from .fused/
fused-cli workspace plan
fused-cli workspace apply

# Manage credentials and bucket-scoped values
fused-cli bucket list
fused-cli secret github set "$GITHUB_TOKEN"
```

### Generate an SDK or MCP Artifact (`sdk prompt`)

The `sdk prompt` command uses Fused intent AI to turn a business use case into a Business Capability SDK or MCP artifact. Describe the workflow your team wants to ship, and Fused maps the right services and endpoints into a single, scoped runtime surface with authentication, retries, tracing, and typed errors wired in.

If your query requires services you haven't added to your workspace yet, the Copilot will automatically discover the latest stable versions from the Global Registry and safely append them to your local `.fused/workspace.yaml`.

```bash
# Generate a standard SDK around a business capability
fused-cli sdk prompt --name onboarding-sdk --version 1.0.0 -d "When a new employee joins, use Jira to create an onboarding ticket, use GitHub to provision repository access, and use Slack to send a welcome message"
```

Fused parses the use case, finds the required services, and opens an interactive Cart UI where you can review, add, or refine the SDK before either downloading the generated `.zip` file to your current directory or deploying it directly as an MCP server.

#### Targets and Languages
You can specify the type of integration and its programming language:
- `--type` (or `-t`): Set the target type. Options are `sdk` (default) or `mcp`.
- `--language` (or `-l`): Set the programming language. Options include `typescript` (default) and `python`.
- `-y` (or `--yes`): Skip the interactive Cart UI and automatically proceed with generation (non-interactive mode).

```bash
# Generate a Python MCP server non-interactively
fused-cli sdk prompt --name support-agent-mcp -t mcp -l python -y -d "Search Zendesk for tickets and use Linear to update corresponding issues"
```

#### Deploying MCP Servers
If you are generating a TypeScript MCP server (`--type=mcp`), you can choose to deploy it directly to Fused-Run (a managed service) by passing the `--deploy` flag instead of downloading the source code. The CLI will output the active Fused-Run URL for your AI agents to connect to immediately via SSE. *(Note: Python MCP servers cannot be deployed and must be downloaded locally).*

```bash
fused-cli sdk prompt --name sales-mcp -t mcp --deploy -d "Read Salesforce leads and fetch Intercom conversations"
```

### Import a Provider API (`import`)

Use `import plan` / `import apply` when the source is already a supported API specification. This is the non-interactive path for teams adding their own internal service to Fused, e.g. from a CI step: no conversational import agent, no endpoint-picking prompt, always the whole spec.

The Registry auto-detects the source format; no `--format` flag is required. Supported formats are:

- OpenAPI
- AsyncAPI
- Postman Collection
- GraphQL SDL or an introspectable GraphQL endpoint

```bash
# Plan a local file. Slug is required for both create and update plans.
fused-cli import plan ./openapi.json --name "Internal Billing API" --slug billing-api

# Import an online source with --url. Format is auto-detected.
fused-cli import plan --url https://developer.example.com/asyncapi.yaml --name "Events API" --slug events-api

# Versionless sources, including GraphQL SDL and Postman collections without a
# version, require an explicit provider version.
fused-cli import plan ./schema.graphql --name "Graph API" --slug graph-api --version 1.0

# Apply: commit the most recently planned import.
fused-cli import apply

# Human-readable docs URL. The agent discovers endpoints, and the CLI selects
# every discovered endpoint by default.
fused-cli import docs --url https://docs.example.com/api --name "Docs API" --slug docs-api --version 1.0

# Optional review/partial import modes.
fused-cli import docs --url https://docs.example.com/api --name "Docs API" --slug docs-api --version 1.0 --review
fused-cli import docs --url https://docs.example.com/api --name "Docs API" --slug docs-api --version 1.0 --select GET:/users
```

The optional positional argument is a local file path. Use `--url` for an online source. Registry first tries `GET`; if the response is not a recognized specification, it sends a standard GraphQL introspection query to the same URL. Successful introspection also uses that URL as the service's GraphQL base URL.

Use `import docs` for normal HTML documentation pages rather than spec URLs.
Docs imports use the same agent-backed extraction flow as the web UI. They
select all discovered endpoints unless `--review` or one or more
`--select METHOD:/path` filters are provided. A partial docs import does not
imply that omitted endpoints were deleted.

`--slug` is required and is resolved within the caller's Registry account. An existing match updates that service; an unknown slug creates a service with that slug. The provider-declared version is authoritative: importing the same version creates a new internal revision, while importing a different version creates that provider version. Fused never asks for a publish strategy and never invents a provider version. If a generated SDK or workspace uses the version being corrected, the plan reports that usage without blocking apply.

```bash
fused-cli import plan ./openapi.json --name "Internal Billing API" --slug billing-api
# Plan update_version for "Internal Billing API" (slug: billing-api, version: 2026-07-14) -- plan ID: 5e1e...
# Diff: 0 added, 2 changed, 0 removed
#   ~ createInvoice
#   ~ listInvoices
# 1 SDKs / 0 workspaces use this version:
#   - SDK billing-sdk (uses a changed/removed endpoint)
# Run `fused-cli import apply` to commit this plan.

fused-cli import apply
# Applied update_version to service 3f8c... (version 2026-07-14, revision 2)
```

When the CLI sends apply through Engine, Engine attempts to register the service
in its sole workspace. This registration is best-effort: a workspace failure is
logged and traced without changing the successful Registry apply response. Use
the explicit workspace service-add flow when automatic registration fails.

### Command Reference

#### Global Flags
All commands support the following global flags:

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--key` | | API key (overrides config & `FUSED_API_KEY`) | `""` |
| `--engine-url` | | Fused Engine URL (overrides config & `FUSED_ENGINE_URL`) | `""` |
| `--file` | `-f` | Path to a Fused config file (disables `.fused/` discovery) | `""` |
| `--readme` | | Print the full CLI README text and exit | `false` |

#### `sdk prompt`
Generate a brand new SDK using Fused intent AI. Automatically discovers and adds missing services to your workspace.

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

#### `service <service-slug> versions`
List Registry versions visible to the current account for a service slug. Supports provider-qualified slugs such as `@provider/slug`.

#### `service <service-slug> show`
Show the base URL, servers, and supported authentication methods for a service.

#### `service <service-slug> operations`
List or search Registry operations for a service. Passing `--q` uses the server-side endpoint search path and supports pagination.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | | Search query for service operations | `""` |
| `--version` | | Service version for operations | `""` |
| `--limit` | | Maximum rows to read; requires `--q` | `20` |
| `--offset` | | Rows to skip before reading; requires `--q` | `0` |

#### `service <service-slug> webhooks`
List Registry webhook definitions for a service.

#### `secret <service-slug> set [value]`
Set an authentication secret for a workspace service.
If the service supports multiple authentication methods, use `--type` to specify the method, or use the `--interactive` flag to select from a menu.

> **Recommended Pattern:** Store API keys, tokens, and service credentials directly using `fused-cli secret set <service-slug> [value] --bucket <bucket>`. This stores secrets securely in Fused's encrypted vault.

> **Tip:** To see the available authentication methods (and their logical names for the `--type` flag) for a service, run `fused-cli service show <service-slug>`.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Force interactive mode to select the authentication method | `false` |
| `--type` | | Specify the logical authentication method name (e.g., bearerAuth) | `""` |
| `--bucket` | | Set secret as an override for a specific Bucket | `""` |
| `--expires-at` | | RFC3339 expiry timestamp (e.g. 2026-12-31T23:59:59Z) | `""` |

#### `secret list`
List secret metadata in a specific bucket. Secret values are never read back.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--list-bucket-id` | | Bucket name or ID to inspect | `""` |

#### `secret <service-slug> remove <key-name>`
Remove a workspace secret.

#### `bucket <name> create`
Create a new bucket for storing overrides and secrets.

#### `bucket list`
List workspace buckets from the Engine GraphQL page used by the UI.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

#### `bucket <name-or-id> show`
Show bucket counts and metadata.

#### `bucket <name-or-id> services`
List services represented in a bucket, including secret, value, Connect config, and connected-user counts.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

#### `bucket <name-or-id> secrets`
List secret metadata in a bucket without reading secret values.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

#### `bucket <name-or-id> values`
List non-secret values in a bucket.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

#### `bucket <name-or-id> connections`
List connected users in a bucket. Filters are sent to Engine GraphQL.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--service` | | Service slug to filter connections | `""` |
| `--user` | | End-user reference to filter connections | `""` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

#### `bucket <name-or-id> sdks`
List SDK or MCP scopes linked to a bucket.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

#### `bucket <name> remove`
Remove a workspace bucket.

#### `value <bucket-id> set <service-slug> <location> <key-name> <value>`
Set a non-secret configuration value in a bucket. Location can be `env`, `body`, `query`, `header`, or `path`.

#### `value <bucket-id> list`
List all non-secret values stored in a bucket.

#### `value <bucket-id> remove <service-slug> <key-name>`
Remove a non-secret value from a bucket.

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

#### `sdk list`
List generated SDK records.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--target` | | Target type to list (`sdk` or `mcp`) | `"sdk"` |
| `--language` | | Target language to filter by | `""` |
| `--latest-only` | | Only show the latest SDK per name | `true` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

#### `sdk <sdk-name[@version]> show`
Show the generated SDK ID, version, and sandbox URL.

#### `sdk <sdk-name[@version]> services`
List services selected by a generated SDK.

#### `sdk <sdk-name[@version]> buckets`
List credential buckets linked to a generated SDK scope.

#### `sdk <sdk-name[@version]> tokens`
List tokens for the generated SDK.

#### `sdk <sdk-name> download`
Download generated SDK artifacts from Registry records through the Engine. Pass an SDK name to download the latest generated SDK, or use `name@version` to request a specific SDK version.

```bash
fused-cli sdk security-sdk download
fused-cli sdk security-sdk@1.2.0 download
```

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--out` | `-o` | Output directory for the SDK | `"."` |

#### `sdk service <service-slug> add`
Add a service to an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Specific version to use for the service | `""` |

#### `sdk service <service-slug> remove`
Remove a service from an SDK configuration. Inherits global flags.

#### `sdk service <service-slug> add <operationId...>`
Add one or more operationIds to an existing service in an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive operation selection | `false` |
| `--apply` | | Apply changes after adding operation | `false` |
| `--download` | | Download SDK after apply (implies `--apply`) | `false` |

#### `sdk service <service-slug> remove <operationId...>`
Remove one or more operationIds from an existing service in an SDK configuration. Inherits global flags.

#### `sdk operation <service-slug> add [operationId...]`
Compatibility alias for adding operationIds to an existing service in an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive operation selection | `false` |
| `--apply` | | Apply changes after adding operation | `false` |
| `--download` | | Download SDK after apply (implies `--apply`) | `false` |

#### `sdk operation <service-slug> remove <operationId...>`
Compatibility alias for removing operationIds from an existing service in an SDK configuration. Inherits global flags.

#### `sdk token <sdk-id> generate <token-name>`
Generate a new SDK token for authenticating SDK requests to the Engine.

#### `sdk token <sdk-id> list`
List all active tokens for an SDK.

#### `sdk token <sdk-id> revoke <token-name>`
Revoke an SDK token.

#### `mcp plan`
Preview changes that will be made to your Fused environment for MCP server configurations (`.fused/mcps/*.yaml`).

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan receipt JSON instead of writing default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |

#### `mcp apply`
Apply a generated plan to deploy MCP server configurations to the Fused Engine runtime.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID for a single MCP config | `""` |
| `--receipt` | | Read a specific plan receipt for a single MCP config | `""` |

#### `mcp validate`
Validates MCP configuration files (`kind: mcp`).

#### `mcp list`
List deployed MCP servers from the Engine.


| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

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

#### `workspace service <service-slug> add`
Add a service to your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Default version to add | `""` |
| `--service-id` | | Optional service ID to store when editing a local config directly | `""` |

#### `workspace service <service-slug> remove`
Remove a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal when the generated plan action is applied | `false` |

#### `workspace service <service-slug> deprecate`
Deprecate a service in your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--at` | | Deprecation effective date in YYYY-MM-DD | `""` |
| `--reason` | | Reason for deprecation | `""` |

#### `workspace service <service-slug> versions`
List versions enabled in the workspace for a service slug. Supports provider-qualified slugs such as `@provider/slug`.

#### `workspace service <service-slug> operations`
List or search operationIds available for an enabled workspace service. Passing `--q` uses server-side endpoint search and supports pagination.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | | Search query for the operations action | `""` |
| `--version` | | Enabled workspace service version; omitted uses the latest enabled version | `""` |
| `--limit` | | Maximum rows to read; requires `--q` | `20` |
| `--offset` | | Rows to skip before reading; requires `--q` | `0` |

#### `workspace service <service-slug> version add <version>`
Add an allowed version to a workspace service. Inherits global flags.

#### `workspace service <service-slug> version remove <version>`
Remove a specific version of a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal | `false` |

#### `workspace service <service-slug> version deprecate <version>`
Deprecate a specific version of a service in your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--at` | | Deprecation effective date in YYYY-MM-DD | `""` |
| `--reason` | | Reason for deprecation | `""` |

#### `import plan`
Parse an auto-detected OpenAPI, AsyncAPI, Postman Collection, GraphQL SDL, or introspectable GraphQL endpoint, resolve the provider-version action, and diff it against the live service. Read-only apart from the plan record.

Usage: `fused-cli import plan [spec-path]` or `fused-cli import plan --url <http(s)-url>`

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--name` | | Service name (required) | `""` |
| `--slug` | | Account-scoped service slug to create or update (required) | `""` |
| `--url` | | Import from an online HTTP(S) source | `""` |
| `--version` | | Provider version when the source does not declare one | `""` |
| `--public` | | Mark a new service public | `false` |
| `--category` | | Category for a new service | `""` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |
| `--json` | | Print the raw plan response as JSON instead of a summary | `false` |

#### `import apply`
Commit the exact source reviewed by `import plan`. Service, provider version, contract rows, immutable internal revision, and plan completion are written atomically.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID (requires `--source-hash`) | `""` |
| `--source-hash` | | Source hash to pair with `--plan-id` | `""` |
| `--receipt` | | Read a specific plan receipt (default: most recent local receipt) | `""` |

#### `import docs`
Extract endpoints from a human-readable API documentation URL using the same agent-backed flow as the web UI. Every discovered endpoint is selected by default.

Usage: `fused-cli import docs --url <http(s)-docs-url> --name <service-name> --slug <service-slug> --version <provider-version>`

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--name` | | Service name (required) | `""` |
| `--slug` | | Account-scoped service slug to create (required) | `""` |
| `--url` | | Human-readable API documentation URL (required) | `""` |
| `--version` | | Provider version for the extracted contract (required) | `""` |
| `--review` | | Review discovered endpoints before extraction; all are selected by default | `false` |
| `--select` | | Endpoint to import as `METHOD:/path`; repeat for multiple endpoints | `[]` |
| `--timeout` | | Maximum time to wait for discovery and extraction | `20m0s` |
| `--no-workspace-add` | | Skip adding the extracted service to the current workspace | `false` |

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
apiVersion: fused/v1
kind: "sdk"
name: "my-sdk"
version: "1.0.0"
language: "typescript"
services:
  okta:
    version: "2026-07-09"
    operations:
      - "listLogEvents"
      - "getUser"
```

The `operations` values are OpenAPI `operationId`s for the selected service version. To browse available operations and add several at once:

```bash
fused-cli sdk service okta add -f .fused/sdks/my-sdk.yaml --version 2026-07-09
fused-cli workspace service okta operations --version 2026-07-09
fused-cli sdk service okta add listLogEvents getUser -f .fused/sdks/my-sdk.yaml
```

`sdk service <slug> add --version` creates or updates the service entry; the config becomes valid once that service has at least one operationId. Use `workspace service <slug> operations` to browse operationIds for services already enabled in the workspace.

### Defining a Workspace Configuration

You can also manage the services activated for your entire workspace via config.

Create `.fused/workspace.yaml`:
```yaml
apiVersion: fused/v1
kind: "workspace"
services:
  stripe:
    versions:
      - "2026-07-09"
      - "2026-08-01"
  okta:
    versions:
      - "1.0.0"
```
The service keys are Registry service slugs. Engine resolves those slugs to service IDs during workspace planning, so teams do not need to know UUIDs. If `versions` is omitted, the Engine resolves Registry's latest public service version during planning and records the exact service-version ID in the plan. Service authentication secrets and API keys are stored securely using `fused-cli secret set <service-slug>`. To edit this file directly from the CLI, use `fused-cli workspace service add <slug> --version <version> -f .fused/workspace.yaml`. To inspect the remote state, use `fused-cli workspace service <slug> versions` and `fused-cli workspace service <slug> operations`.

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
