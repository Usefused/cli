# Fused CLI

The command-line interface to [Fused](https://usefused.com)—the simplification
of integration for organizations, engineers, and agents.

Use `fused-cli` to work with your Fused deployment from a terminal or coding
agent. Discover and import services, create and deploy apps, manage credentials
and access, and inspect execution activity. Services can be internal APIs or
third-party APIs.

The CLI connects to your **Fused Engine**, where your integrations run.
App configuration and TypeScript source live in your project; the Engine
manages execution, credentials, and permissions.

## App types

| Task | App or interface |
| --- | --- |
| Share integration logic across applications and agents | A Unified App with defined inputs and outputs, available through API, SDK, or MCP |
| Call internal or third-party services from your application | A typed SDK with methods for your selected operations |
| Give an AI agent access to your services | A hosted MCP server with your selected tools |
| Call integrations over HTTP | A REST API hosted by your Engine |
| React to service events | Verified webhooks that can trigger a Unified App or reach applications and agents |

Teams can discover available services, import their own APIs, and control who
can use each integration and credential bucket.

## Install

### macOS and Linux

```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | bash
```

### Windows

```powershell
irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex
```

You can review the [macOS/Linux installer](install.sh) or
[Windows installer](install.ps1) before running it, or download a binary from
[Releases](https://github.com/Usefused/cli/releases).

Check your installation:

```bash
fused-cli --version
```

<a id="connect-to-an-engine"></a>

## Get started

### 1. Connect to your Engine

You need access to a running Fused Engine. Use the URL provided by your team
or the address of your own deployment. Replace the example URL below:

```bash
fused-cli --engine-url https://your-engine.example.com login
fused-cli whoami
```

`login` opens your browser to sign in. `whoami` confirms the Engine and account
you are using. For a local Engine, the usual URL is `http://localhost:8081`.
See [connection and authentication setup](docs/SETUP.md) for other login methods.

### 2. Find a service

Search the Fused service catalogue for the API you want to use:

```bash
fused-cli service list --registry --search hubspot
fused-cli service list --registry --search stripe
```

To see services already enabled in your workspace, including imported internal
APIs, run `fused-cli service list`.

If your API is not in the catalogue, you can
[import an API specification or discover it from its documentation](docs/IMPORTING_SERVICES.md).

### 3. Describe your integration

With your services available and their credentials configured, describe the
result you want. For example, if your workspace uses HubSpot and Stripe:

```bash
fused-cli describe "Find a customer in HubSpot and return their recent Stripe invoices" \
  --name customer-brief
```

Fused finds the relevant operations and drafts a Unified App. You review its
TypeScript source and configuration before confirming deployment. The deployed
app can be called through its API or added to an SDK or MCP server. The same
workflow can use your imported internal services.

Provider credentials are stored in a **bucket**, a named collection of
credentials used by your apps. Run `fused-cli connect <service-name>` to choose
an authentication method and enter its fields. The picker highlights configured
methods; select one to edit it, or choose a missing method to set it up.
`--bucket` defaults
to `default`. Automation can use one quoted `"name=value, name=value"` argument with `--type`, `--auth-name`, and
`--no-input`; values may come from shell environment variables.

Named bucket entries use `fused-cli bucket secret|env|cert set <name>[=<value>]`,
`get <name>`, and `remove <name>`. Omit the value to enter it interactively,
or pass an assignment such as `"token=$TOKEN"`. Certificates contain public PEM only. See [credentials and buckets](docs/SETUP.md#credentials-and-buckets)
to configure access to the services your integration needs.

### 4. Manage your app

List your apps and their published versions:

```bash
fused-cli app list
fused-cli app list --type mcp
fused-cli app versions list customer-brief
```

All app management uses `fused-cli app`. Filter the list with `--type sdk`,
`--type rest`, `--type mcp`, or `--type unified-app`; omit the filter to see all
types. There are no standalone `sdk`, `rest`, `api`, `mcp`, or `unified-app`
command groups.

To update a Unified App, edit its TypeScript source and configuration, and
choose a new version. Use `app plan` to preview the changes, then `app apply`
to publish them.
See [configuration and deployment](docs/CONFIG_AS_CODE.md) for the full workflow.

## Use service operations directly

You can also select individual service operations for an SDK, REST API, or
MCP server. For example, create an app using Linear:

```bash
fused-cli init support-app --service linear
```

The interactive setup lets you select operations and review the configuration.
It creates a typed SDK, an MCP endpoint, and REST access for the same app.
Add `--sdk`, `--mcp`, or `--rest` when you need only one of these interfaces.
Use `extend` to add services to an existing app.

For workflows you want to author yourself, see
[writing a Unified App in TypeScript](docs/UNIFIED_APPS.md).

## Use with your coding agent

Install Fused skills so your coding agent can help discover services,
configure integrations, and deploy apps from your project:

```bash
fused-cli skill install --for codex
```

Supported agents include Codex, Claude Code, Cursor, Windsurf, and Antigravity.
Run `fused-cli skill install --help` for agent names and installation options.

## Documentation and help

- [Setup](docs/SETUP.md): sign-in, credentials, team access, and CI authentication.
- [Import APIs](docs/IMPORTING_SERVICES.md): add services from specifications or documentation.
- [Configuration and deployment](docs/CONFIG_AS_CODE.md): review, version, and publish changes.
- [Unified Apps](docs/UNIFIED_APPS.md): write and deploy hosted TypeScript workflows.
- [Command reference](docs/COMMANDS.md): commands, flags, and automation options.

Run `fused-cli --help` to see available commands, or add `--help` to any command
for details. Run `fused-cli --readme` to read this guide in your terminal.
