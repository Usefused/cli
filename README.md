# <img src=".github/readme/cli.svg" width="30" height="30" alt=""> Fused CLI

**Fused - Connect Your APIs Once, Use Everywhere**

Bring internal, partner, and third-party APIs into Fused. Use the CLI to turn those connections into tools your apps and AI agents can share, with control over who can use them.

[Install](#install) · [Build a customer brief](#get-started) · [Coding agent setup](#use-with-your-coding-agent) · [Command reference](docs/COMMANDS.md)

## Install

**macOS and Linux**

```bash
curl -fsSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | bash
```

**Windows PowerShell**

```powershell
irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex
```

Run `fused-cli --version` to check your installation. You can also download a [release binary](https://github.com/Usefused/cli/releases) or review the [shell installer](install.sh) and [PowerShell installer](install.ps1).

<a id="connect-to-an-engine"></a>

## Get started

Build a customer brief that finds an account in HubSpot and returns its recent Stripe invoices. The CLI drafts the integration in your project. Your Engine holds the credentials and runs the published app.

### 1. Sign in to your Engine

Use your team’s Engine URL or `http://localhost:8081` for a local deployment. Login opens your browser. Check the active account with `whoami`.

```bash
fused-cli --engine-url https://your-engine.example.com login
fused-cli whoami
```

### 2. Find and connect your services

```bash
fused-cli service list --registry --search hubspot
fused-cli service list --registry --search stripe
```

Run `fused-cli service list` to see which services your workspace has enabled. Once HubSpot and Stripe are available, connect the accounts this app will use:

```bash
fused-cli connect hubspot
fused-cli connect stripe
```

Choose an authentication method at the prompt. By default, Fused stores credentials in the `default` bucket. Use `--bucket` to select a different one. See [credentials and buckets](docs/SETUP.md#credentials-and-buckets) for named secrets, certificates, and unattended setup.

### 3. Describe the result

```bash
fused-cli describe "Find a customer in HubSpot and return their recent Stripe invoices" \
  --name customer-brief
```

Review the generated TypeScript and configuration before confirming deployment. You can call the resulting Unified App through its API or add it to an SDK or MCP server. The same workflow works with [your own APIs](docs/IMPORTING_SERVICES.md).

### 4. Inspect the published app

```bash
fused-cli app list
fused-cli app versions list customer-brief
```

Filter `app list` with `--type sdk`, `rest`, `mcp`, or `unified-app`. To publish a change, edit the source and configuration, select a new version, then review it with `app plan` before running `app apply`. See [configuration and deployment](docs/CONFIG_AS_CODE.md).

## App types

| Use case | Interface |
| --- | --- |
| A workflow that several apps or teams can reuse | [Unified App](docs/UNIFIED_APPS.md) with defined inputs and outputs |
| An application that calls selected API operations | [Typed SDK](https://usefused.com/docs/app/build-an-sdk) |
| Tools for an AI agent | [Hosted MCP server](https://usefused.com/docs/mcp/deploy-a-server) |
| An integration callable over HTTP | [REST API](https://usefused.com/docs/app/testing/over-rest) |
| A reaction to a provider event | [Verified webhook](https://usefused.com/docs/mcp/receive-events) delivered to a Unified App, SDK, or MCP server |

## Use service operations directly

To give a support app two existing operations, select them by name:

```bash
fused-cli init support-app --service 'jira=issueUpdate;slack=chatPostMessage'
```

- **Choose interfaces.** Review the configuration to create SDK, MCP, and REST access for the same app. Add `--sdk`, `--mcp`, or `--rest` for a single interface. Use `extend` to add services later.
- **Choose operations.** Separate operations with commas and services with semicolons. Quote the selector so the shell passes it intact.
- **Select whole services.** A bare selector such as `--service 'jira;slack'` selects all operations for both services, including with `--no-input`.

## Use with your coding agent

Install the Fused skills in your project:

```bash
fused-cli skill install --for codex
```

Supported agents include Codex, Claude Code, Cursor, Windsurf, and Antigravity. Run `fused-cli skill install --help` for installation options.

## Delete or deprecate a Registry service

`service remove` removes a service from your workspace. Registry deletion affects every version and consumer of a service you own. Inspect its lifecycle before deleting it:

```bash
fused-cli service registry lifecycle <service> --json
fused-cli service registry delete <service> --yes
```

To deprecate now and delete later, schedule a date. You can change the deadline by repeating the command or cancel before deletion:

```bash
fused-cli service registry deprecate <service> --delete-at 2027-01-01T00:00:00Z --yes
fused-cli service registry cancel-deletion <service>
```

Registry mutations require ownership and Engine `service.manage` permission. Commands accept an owned slug, `@provider/slug`, or UUID. After deletion, use the UUID to inspect lifecycle history. Consumers reconcile through their Engines when online. The CLI, Engine, and Registry must support these endpoints. The separate `service deprecate --at` command is advisory.

## Documentation and help

| Guide | Use it to |
| --- | --- |
| [Setup](docs/SETUP.md) | Configure sign-in, team access, credentials, or CI. |
| [API imports](docs/IMPORTING_SERVICES.md) | Import specifications or discover APIs from documentation. |
| [Configuration and deployment](docs/CONFIG_AS_CODE.md) | Review, version, and publish changes. |
| [Unified Apps](docs/UNIFIED_APPS.md) | Write and deploy hosted TypeScript workflows. |
| [Command reference](docs/COMMANDS.md) | Look up commands, flags, and automation options. |

Use `fused-cli --help` for commands, `fused-cli <command> --help` for flags, or `fused-cli --readme` to read this guide in your terminal.
