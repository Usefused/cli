# Fused CLI

`fused-cli` is the config-as-code and operations CLI for the
[Fused](https://usefused.com) integration gateway. Use it to connect to an
Engine, import APIs, generate SDKs, and deploy MCP servers.

## Installation

### macOS and Linux

```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | bash
```

The script asks before installing to `/usr/local/bin`. To inspect it first:

```bash
curl -sSL -o install.sh https://raw.githubusercontent.com/Usefused/cli/main/install.sh
less install.sh
bash install.sh
```

For CI or a specific release:

```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | ASSUME_YES=1 bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | VERSION=v1.0.0 ASSUME_YES=1 bash
```

### Windows

```powershell
irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex
```

Install a specific release:

```powershell
$env:VERSION="v1.0.0"; irm https://raw.githubusercontent.com/Usefused/cli/main/install.ps1 | iex
```

The installer writes to `%LOCALAPPDATA%\Programs\fused-cli` and adds it to the
user `PATH`.

### Go or manual download

```bash
go install github.com/Usefused/cli@latest
```

Alternatively, download the archive for your platform from
[Releases](https://github.com/Usefused/cli/releases) and place `fused-cli` on
your `PATH`.

## Connect to an Engine

```bash
fused-cli --engine-url http://localhost:8081 login
fused-cli whoami
fused-cli workspace services list
```

Automation can use `--key`, `FUSED_API_KEY`, or `FUSED_LICENSE_KEY` instead of
browser login. See [CLI setup and operation](docs/SETUP.md) for configuration
precedence, headless login, credentials, teams, and automation-safe execution.

## Install skills for your coding agent

The CLI ships task-specific skills that teach coding agents how to discover
services, inspect authentication, prepare reviewable SDK/MCP config, and use
plan/apply safely.

```bash
# See the bundled skills.
fused-cli skill list

# Install all skills for an agent.
fused-cli skill install --for codex

# Or install one skill only.
fused-cli skill install --for codex --skill fused-sdk
```

Supported targets are `codex`, `claude`, `antigravity`, `cursor`, and
`windsurf`. Use `--scope project` for repository-local installation or
`--scope user` where the target supports it:

```bash
fused-cli skill install --for codex --scope project
```

Installation writes the files into the selected agent's normal skill or rules
directory. Release installs use the versioned skill snapshot shipped beside the
binary, so this also works offline. The agent discovers and loads the relevant
skill when a task matches; the command does not inject new instructions into an
already-running context.

## Import an API

Plan the import first, review it, then apply the exact plan:

```bash
fused-cli import plan ./openapi.json \
  --name "Internal Billing API" \
  --slug billing-api

fused-cli import apply
```

For a remote specification:

```bash
fused-cli import plan \
  --url https://developer.example.com/openapi.json \
  --name "Example API" \
  --slug example-api

fused-cli import apply
```

Fused detects the supported source format. See
[Importing services](docs/IMPORTING_SERVICES.md) for documentation discovery,
GraphQL, AsyncAPI, Postman, WSDL, Google Discovery, overlays, strict mode, and
diagnostics.

## Generate an SDK from a goal

`sdk prompt` is the user-invoked Fused agent. Describe the business capability;
it discovers services and operations, then opens an interactive cart for review.

```bash
fused-cli sdk prompt \
  --name onboarding-sdk \
  --version 1.0.0 \
  --description "When a new employee joins, create an onboarding ticket in Jira, provision GitHub access, and send a Slack welcome message"
```

If the goal is already being handled by a coding agent, install and use the
`fused-sdk` skill instead. The coding agent should perform the deterministic
workflow directly rather than start a second agent through `sdk prompt`.

## More documentation

- [CLI setup and operation](docs/SETUP.md) — authentication, automation,
  credentials, buckets, teams, and access.
- [Importing services](docs/IMPORTING_SERVICES.md) — supported sources,
  diagnostics, overlays, and execution compatibility.
- [Config as code](docs/CONFIG_AS_CODE.md) — workspace/SDK YAML, plan/apply,
  sync, and execution policies.
- [Command reference](docs/COMMANDS.md) — every command and flag.

Run `fused-cli --help` for command discovery. `fused-cli --readme` prints this
onboarding guide without appending the command reference.
