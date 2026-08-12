# Fused CLI

The `fused-cli` is the official command-line interface for [Fused](https://usefused.com). It manages a Fused Engine from your terminal: connect the CLI to an Engine, import API services into the Registry, apply workspace configuration, manage buckets and secrets, generate SDK packages, and deploy MCP servers.

Use it as the config-as-code and operations CLI for the Fused integration harness. SDK and MCP generation are supported workflows, not the whole product.

## Installation

### Option 1: One-Line Install Script (Recommended)

#### macOS / Linux

```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | bash
```

Because this pipes a remote script straight into `bash`, we recommend inspecting it first:

```bash
curl -sSL -o install.sh https://raw.githubusercontent.com/Usefused/cli/main/install.sh
less install.sh   # review before you run it
bash install.sh
```

Either way, the script prompts for confirmation before it downloads or installs anything (it uses `sudo` to write to `/usr/local/bin`). For non-interactive installs (CI, Dockerfiles), skip the prompt with `-y`/`--yes` or `ASSUME_YES=1`:

```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | ASSUME_YES=1 bash
```

Install a specific version:

```bash
curl -sSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | VERSION=v1.0.0 ASSUME_YES=1 bash
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

The `fused-cli` requires an **Engine credential** and an **Engine URL** to connect to the Fused data plane. A small workspace can use its `FUSED_LICENSE_KEY` as the bootstrap Owner credential; workspaces that add people can sign in through their Engine and receive an individually attributable CLI credential. If you do not have an Engine running yet, install or run one from the [Fused Engine releases](https://github.com/Usefused/engine/releases).

```bash
# Open the Engine login page and choose managed sign-in or an existing API Key
fused-cli --engine-url "http://localhost:8081" login
```

The CLI generates the resulting `fsk_` key locally; the Engine stores only its
hash and binds it to the subject authenticated in the browser. Use
`--no-browser` to print the approval URL for an interactive remote/headless
session. Automation should keep using `--key`, `FUSED_API_KEY`, or
`FUSED_LICENSE_KEY`; browser login is rejected with `--no-input` or `CI=true`.

Inspect the effective credential without revealing it, then revoke a saved
managed CLI login when finished:

```bash
fused-cli whoami
fused-cli logout
```

`whoami` follows the normal credential resolution order and prints the Engine,
subject, account, workspace, authentication source, and expiry. `logout`
always targets the saved login and its saved Engine, even when flags or
environment variables are present. It removes only the saved credential and
expiry metadata, preserving `engine-url`. If `FUSED_API_KEY` or
`FUSED_LICENSE_KEY` remains set, the CLI warns that subsequent commands can
still authenticate through the environment.

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
2. **Saved login/config credential**: created by `fused-cli login` or `fused-cli config set api-key`
3. **Personal/service credential fallback**: `FUSED_API_KEY`
4. **Bootstrap Owner credential fallback**: `FUSED_LICENSE_KEY`

The saved login deliberately wins over ambient environment variables so an
individually attributable user credential remains active after login.

### Automation-safe execution

Engine requests time out after one minute by default. Override this per run
with `--timeout`, and pass `--request-id` to attach a safe audit correlation ID
to every Engine request. SIGINT and SIGTERM cancel outstanding requests.

Use `--no-input` in scripts and agent runs to fail instead of prompting.
Read-only inspection commands also accept `--json`; paginated results include
`items`, `total`, `limit`, and `offset` so agents do not need to parse tables.
On failure, commands using `--json` write one structured error object to stderr
and exit non-zero; Engine error codes, remediation, retryability, and trace IDs
are preserved when available.
`CI=true` enables the same non-interactive behavior and disables release update
checks; `FUSED_NO_UPDATE_CHECK=1` disables only the update check. Interactive
options such as `import docs --review` must be replaced by their explicit flag
forms when non-interactive execution is active.

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
printf '%s' "$GITHUB_TOKEN" | fused-cli secret set github --value-stdin

# Manage people, teams, and team-scoped access
fused-cli team list
fused-cli user list
fused-cli team eligible-owners

# Share a company bucket for bounded use by every workspace member and team
fused-cli workspace access bucket grant company-credentials
```

For organisation workspaces, `team` manages membership and workspace, service,
bucket, and runtime access; `user` manages people and their personal
credentials. SDKs, MCP servers, and webhook registrations belong to the
authenticated person by default. Use `plan --owner-team <team-slug>` only when
that SDK, MCP server, or webhook should be managed by a team. `workspace access`
is separate from ownership: it grants bounded use of selected buckets or
permission-scoped resources to the whole workspace without
granting secret, configuration, or token management. Solo workspaces need no
RBAC setup.

### Generate an SDK (`sdk prompt`)

The `sdk prompt` command uses Fused intent AI to turn a business use case into
a typed Business Capability SDK. Describe the workflow your team wants to ship,
and Fused maps the right services and endpoints into a scoped generated package.

This command is the user-invoked, Fused-agent entry point. When the business
goal is instead given to a coding agent, install/use the `fused-sdk` skill;
that agent performs the workflow directly and must not call `sdk prompt` or
start a second agent.

If your query requires services you haven't added to your workspace yet, the Copilot will automatically discover the latest stable versions from the Global Registry and safely append them to your local `.fused/workspace.yaml`.

```bash
# Generate a standard SDK around a business capability
fused-cli sdk prompt --name onboarding-sdk --version 1.0.0 -d "When a new employee joins, use Jira to create an onboarding ticket, use GitHub to provision repository access, and use Slack to send a welcome message"
```

Fused parses the use case, finds the required services, and opens an interactive
Cart UI where you can review, add, or refine the SDK before generating it.
MCP servers use their own `mcp plan` and `mcp apply` deployment flow.

### Import a Provider API (`import`)

Use `import plan` / `import apply` when the source is already a supported API specification (OpenAPI 3, Swagger 2, Google Discovery, AsyncAPI, Postman Collection, WSDL, or GraphQL SDL/introspectable endpoint) -- the non-interactive path for teams adding their own internal service to Fused, e.g. from a CI step. The Registry auto-detects the source format; no `--format` flag is required, and the plan summary reports the authoritative detected format.

```bash
# Plan endpoint-only import from a local file. Slug is required for create/update.
fused-cli import plan ./openapi.json --name "Internal Billing API" --slug billing-api --target endpoints
fused-cli import apply

# Import an online source with --url; format is auto-detected. Versionless
# sources (GraphQL SDL, versionless Postman collections) need --version.
fused-cli import plan --url https://developer.example.com/asyncapi.yaml --name "Events API" --slug events-api

# Google Discovery documents use the same reviewed plan/apply path.
fused-cli import plan --url https://www.googleapis.com/discovery/v1/apis/drive/v3/rest --name "Google Drive" --slug google-drive

# Apply provider-specific corrections from a local overlay. The CLI sends the
# file unchanged; Registry validates and canonicalizes it with the source.
fused-cli import plan ./openapi.json --overlay ./billing.overlay.yaml --name "Billing API" --slug billing-api

# Human-readable docs URL: the agent discovers endpoints and, by default,
# imports every one it finds. --review or --select narrows that down.
fused-cli import docs --url https://docs.example.com/api --name "Docs API" --slug docs-api --version 1.0
```

`--target` accepts `all`, `endpoints`, or `webhooks` and defaults to `endpoints`.
The selected target is stored in the plan, so `import apply` commits exactly
the contract scope that was reviewed.

`import plan` reports structured diagnostics when provider metadata cannot be
represented exactly. Use `--json` to consume the diagnostic fields unchanged,
or `--strict` to reject a plan containing warning or error diagnostics. Info
diagnostics do not fail strict mode.

Loss-aware diagnostics include the detected source format/version, a bounded
source pointer, service scope, disposition (`captured`, `diagnosed`, or
`strict_error`), required execution capability when applicable, and provenance.
Treat diagnostic codes and disposition as the stable automation contract;
message text remains for people.

Every imported service version also carries an execution-contract envelope:
`contract_version` identifies the wire shape and `required_capabilities` names
behaviour an Engine must understand to execute it correctly. The CLI transports
these fields without choosing compatibility. Registry publication and Engine
snapshot materialization fail closed when the target Engine cannot execute the
declared version or any required capability; additive documentation fields do
not require a capability.

`--overlay` accepts a local file only. The CLI does not parse, normalize, or
merge it: Registry owns canonicalization and returns a combined `review_hash`.
The receipt records that opaque review identity, so `import apply` commits the
exact source and overlay that were reviewed without rereading either file. For
direct apply, pass both `--plan-id` and `--review-hash`; do not use the
informational source or overlay hashes as the apply guard.

`--slug` is required and is resolved within the caller's Registry account. An existing match updates that service; an unknown slug creates one. The provider-declared version is authoritative -- importing the same version creates a new internal revision, importing a different version creates that provider version -- and if a generated SDK or workspace uses the version being changed, `import plan` reports that usage without blocking apply. When apply runs, Engine also best-effort registers the service in its sole workspace; a workspace failure there is logged and traced without affecting the successful Registry apply response, so use the explicit `workspace service add` flow if that automatic registration fails.

## Command Reference

Every command and flag -- including `team`, `user`, `service`, `secret`, `bucket`, `connect`, `value`, `sdk`, `mcp`, `webhook`, `workspace`, `import`, and the global `plan`/`apply`/`validate` -- is documented in [`docs/COMMANDS.md`](docs/COMMANDS.md). `fused-cli --readme` prints that reference together with this file as one combined document.

## Config-as-Code (GitOps)

Fused supports managing your SDKs and Workspace service whitelists via declarative YAML configurations stored in a `.fused/` folder in your repository.

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
fused-cli sdk service add okta -f .fused/sdks/my-sdk.yaml --version 2026-07-09
fused-cli workspace service operations okta --version 2026-07-09
fused-cli sdk operation add okta listLogEvents getUser -f .fused/sdks/my-sdk.yaml
```

`sdk service add <slug> --version` creates or updates the service entry; the config becomes valid once that service has at least one operationId.

### Defining a Workspace Configuration

You can also manage the services activated for your entire workspace via config.

Create `.fused/workspace.yaml`:
```yaml
apiVersion: fused/v1
kind: "workspace"
services:
  stripe:
    versions:
      - version: "2026-07-09"
      - version: "2026-08-01"
  okta:
    versions:
      - version: "1.0.0"
```
The service keys are Registry service slugs. Engine resolves those slugs to service IDs during workspace planning, so teams do not need to know UUIDs. If `versions` is omitted, the Engine resolves Registry's latest public service version during planning and records the exact service-version ID in the plan. `versions` is a list of objects rather than bare version strings: each entry carries its own resolved `service_version_id` plus any per-version `public`/`execution_policy`/`connection_profiles` override, so that data doesn't need a separate sibling list keyed by a repeated version string. Service authentication secrets and API keys are stored securely using `fused-cli secret set <service-slug>`; a bucket's OAuth/OIDC app registration is a separate, immediate admin action via `fused-cli connect set <service-slug>` -- see [`docs/COMMANDS.md`](docs/COMMANDS.md) -- neither is a workspace.yaml field.

Pagination uses the versioned strategy contract under `execution_policy`:

```yaml
services:
  google-drive:
    execution_policy:
      pagination:
        version: 3
        request:
          - state: cursor
            target: {location: query, name: pageToken}
            value_type: string
            apply: all
        response:
          items: {path: "$.files"}
          values:
            - name: next_cursor
              source: {location: body, path: "$.nextPageToken", value_type: string}
        continuation:
          - {kind: token, state: cursor, response_value: next_cursor}
        termination:
          stop_on_empty_items: true
          stop_on_missing_values: [next_cursor]
          repeated_value: error
        limits: {max_pages: 100, max_items: 10000, max_bytes: 16777216, max_duration_ms: 120000}
```

The policy always affects this Engine locally. `execution_policy.public: true`
also publishes it for other consumers when the caller owns the service. A
version entry's policy overrides the service-level local default for that
version, while endpoint pagination imported into the provider contract remains
more specific. SDK and MCP configs do not duplicate pagination settings. The
composable v3 policy can combine token, offset, page, RFC Link, next-URL,
conditional item paths, derived cursors, and GraphQL page templates without
provider-specific client code. Engine validates termination, repeated values,
URL origins and hard limits, then streams each successful provider page as a
separate result chunk.

Quota, concurrency, and retry policies use the same `execution_policy`
ownership boundary. Version 3 policies declare simultaneous dimensions,
explicit bucket-identity inputs and enforcement modes, response-driven state,
and bounded replay predicates. The CLI transports them unchanged. Generated
SDKs make one Engine request and contain no provider limiter, semaphore, retry
loop, jitter, or backoff; Engine coordinates those decisions across pages and
processes.

Structured webhook signatures, post-auth discovery, media-upload workflows,
and multi-spec catalogues follow that boundary too. CLI preserves reviewed
recipes, bucket-secret references, ordered upload steps, origin/status guards,
and reject-on-collision catalog metadata without executing them. Generated SDK
and MCP callers make one logical Engine request; they never receive signing
references or implement verification, discovery, resumable upload, or catalogue
composition locally.

OpenAPI 3.2 operations keep the exact `QUERY` or custom HTTP method token,
named servers, whole-query parameter content, OAuth device-flow metadata, and
sequential/positional media encodings. Tag summary, parent, and kind remain
documentation-only. Generated callers forward one declared query-string value
in their single Engine request; they do not percent-encode
it, frame streams, or interpret multipart positions themselves. Registry
resolves reusable media types before projection, while unsupported `$self`,
XML node execution, external URI security names, and discriminator fallback
semantics remain gated instead of being guessed. Device Authorization metadata
may survive source analysis, but public admission rejects that flow until its
connection and Engine execution vertical is implemented.

### Syncing Local Config From Remote State

UI actions and service imports can update the Engine or Registry before your local YAML knows about the change. Use sync when you want local config-as-code to mirror the current remote truth:

```bash
fused-cli workspace sync -f .fused/workspace.yaml
fused-cli sdk sync my-sdk -f .fused/sdks/my-sdk.yaml
```

`workspace sync` mirrors the Engine's active workspace services. `sdk sync`
mirrors the most recently generated SDK with the given name, including its
selected services, resolved versions, and operation names. Once you're satisfied
with a plan (`fused-cli sdk plan` / `fused-cli workspace plan`), apply it the
same way shown under Common Workflows above.

Plan output includes the Engine's complete change summary. A saved plan receipt is bound to both the exact config content and the normalized Engine URL. Apply preflights every selected config before its first remote mutation and rejects receipts that are stale, unbound, or belong to another Engine; re-run plan against the intended Engine instead of bypassing this check.

CLI-managed config files, plan receipts, generated SDK config, and installed skill files are replaced atomically. Existing file permissions are preserved, and structured config/receipt content is validated before it replaces the previous file.

Run `fused-cli --help` for more information on available commands and flags.
