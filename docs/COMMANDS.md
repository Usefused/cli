# Fused CLI Command Reference

The full list of commands and flags for `fused-cli`. Start with the main
[README](../README.md), then use the focused guides for
[setup and operation](SETUP.md), [service imports](IMPORTING_SERVICES.md), and
[config as code](CONFIG_AS_CODE.md).

## Global Flags
All commands support the following global flags:

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--key` | | Engine credential (overrides saved login, `FUSED_API_KEY`, and `FUSED_LICENSE_KEY`) | `""` |
| `--engine-url` | | Fused Engine URL (overrides config & `FUSED_ENGINE_URL`) | `""` |
| `--file` | `-f` | Path to a Fused config file (disables `.fused/` discovery) | `""` |
| `--no-input` | | Fail instead of prompting; also enabled by `CI=true` | `false` |
| `--timeout` | | Maximum duration for an Engine request; spec imports use `20m0s` unless explicitly set | `1m0s` |
| `--request-id` | | Attach an audit correlation ID to every Engine request | `""` |
| `--readme` | | Print the concise CLI onboarding README and exit | `false` |

All Engine requests have a finite timeout and are cancelled when the CLI
receives SIGINT or SIGTERM. `CI=true` and `FUSED_NO_UPDATE_CHECK=1` disable
release update checks. A command that would prompt fails with remediation when
`--no-input` or `CI=true` is active.

## Structured output for agents

Read-only inspection commands accept `--json` without changing their default
human-readable output. This includes service and workspace discovery,
SDK/MCP inspection and token metadata, validation, bucket/secret/value/connect
metadata, identity, access, team, user, config, and skill listings.

Commands backed by an API page return:

```json
{"items": [], "total": 0, "limit": 20, "offset": 0}
```

Exact-object and non-paged reads return the object or array directly. Sensitive
reads keep the same safe projection as human output: secret values, decrypted
connect credentials, and execution-token values are never returned by list
commands. Plan commands retain their existing `--json` plan-result contract;
SDK apply, token generation, invocation, and activity also provide stable JSON.

When a command using `--json` fails, stdout remains reserved for successful
output and stderr receives one JSON object before the CLI exits non-zero:

```json
{
  "ok": false,
  "error": {
    "code": "bucket_credentials_missing",
    "message": "Provider credentials are not configured.",
    "category": "validation",
    "retryable": false,
    "remediation": "Run the supplied credential command before repeating this call.",
    "details": {
      "bucket_id": "11111111-1111-4111-8111-111111111111",
      "service_id": "22222222-2222-4222-8222-222222222222",
      "command": "fused-cli secret set 22222222-2222-4222-8222-222222222222 --bucket 11111111-1111-4111-8111-111111111111 --type api_key --auth-name providerKey"
    },
    "trace_id": "...",
    "http_status": 409,
    "command": "fused-cli sdk invoke support-sdk@1.0.0 listIssues"
  }
}
```

Fields unavailable for a particular failure are omitted. Engine errors retain
their safe structured fields through CLI wrapping; untrusted response bodies
are still excluded. Cobra usage failures use `invalid_arguments`; other local
CLI failures use `command_failed` with the original message and a help-oriented
remediation. OTEL records the stable error code and retryability without
recording remote messages or command input.

## `login`

Open the selected Engine's sign-in page and save a subject-scoped CLI
credential after browser approval. The page supports managed Fused Auth and an
existing Engine API key. The generated credential never passes through the
browser and is not printed.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--no-browser` | | Print the sign-in URL instead of opening a browser | `false` |

## `whoami`

Show the non-secret Engine identity used by the effective credential. The
command follows normal `--key` / saved config / environment resolution and
prints identity, account, workspace, credential source, authentication method,
and expiry when reported.

Pass `--json` for the Engine URL, local credential source, and non-secret
identity response as structured fields.

## `logout`

Revoke an Engine-issued managed CLI login and remove its saved local
credential. Logout deliberately uses the saved Engine URL and saved credential,
not `--key` or environment overrides. A failed remote revocation preserves the
local login for retry; an already-inactive login is cleared locally. The saved
`engine-url` remains configured. Manually saved API keys are left unchanged
because they are not managed CLI logins.

## `sdk prompt`
Generate a brand new SDK using Fused intent AI. Automatically discovers and adds missing services to your workspace.

This is the user-invoked Fused-agent path. Coding agents should use the
`fused-sdk` skill and execute the deterministic SDK workflow directly,
without invoking `sdk prompt` and starting a second agent.

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

## `config`
Manage your local CLI configuration (`set`, `get`, `list`, `reset`). Inherits global flags.

## `init <app-name>`

Create and start a typed SDK, a direct Engine API app, or an Engine-hosted MCP
server. Top-level init composes the existing workspace and app plan/apply
functions; it does not introduce a new resource kind or receipt boundary.

```bash
# Generate, apply, and download a typed SDK
fused-cli init google-workspace --sdk \
  --service '@google/drive' \
  --operation '@google/drive=listFiles'

# Apply the same execution app without generating a package
fused-cli init google-api --api \
  --service '@google/drive' \
  --select-all '@google/drive'

# Deploy an Engine-hosted MCP server
fused-cli init support-agent --mcp \
  --description 'Help support teams manage issues' \
  --service jira \
  --select-all jira
```

`--service <key>[=<version>]`, `--operation <service>=<operationId>`, and
`--select-all <service>` are repeatable. An omitted provider version resolves to
the latest enabled workspace version, or the latest public Registry version
when activation is needed. New SDK/MCP configs default to app version `1.0.0`;
generated SDKs default to TypeScript. `--bucket` only references an existing
bucket and is omitted by default—it never creates a bucket.

`--no-apply` writes and validates the same desired state, saves every plan
receipt whose dependencies are active, and stops before apply, generation, or
download. If a service is not active yet, the workspace plan receipt is saved
and app planning is deferred until workspace apply; otherwise the app plan
receipt is saved immediately. The command prints the exact remaining commands,
including `sdk apply --download` for generated SDKs, and tells users to re-plan
if a retained receipt becomes stale.

In a terminal, omit the mode flag to choose `--sdk`, `--api`, or `--mcp`. If a
service has no operation flags, the highlighted default is **All operations**;
choose the narrower path to search by operation ID, method, path, description,
or tag. In automation, `--no-input` or `CI=true` requires one explicit mode and
an explicit `--operation` or `--select-all` for every service. Non-interactive
MCP creation also requires `--description`.

Creation refuses to replace an existing path before any remote plan. Extension is additive and
idempotent through the top-level `extend` command described below. The retained
`init --extend` flag is a compatibility alias for existing scripts.

If an exact version is not enabled, a terminal presents one combined
confirmation; governance remains split into a workspace plan receipt and an app
plan receipt. SDK/API planning securely remediates only typed missing
credentials in the exact selected bucket and retries once. Top-level init has
human apply output and does not expose `--json`; use the explicit plan/apply
commands for structured automation.

Without `--no-apply`, init builds and validates the app candidate in memory, then plans it before
atomically creating the YAML file. If that app plan fails, no app config or app
receipt is created. A workspace activation completed earlier in the composed
flow remains applied under its separate receipt.

For generated SDK mode, `generation_contract_pin_unavailable` starts one
bounded legacy-snapshot repair. The CLI resolves every selected active
`service@version` before changing anything, visibly refreshes each exact Engine
snapshot, and retries the unchanged app plan once. `--no-input` follows the
same deterministic path without prompting. The CLI never substitutes a runtime
hash, selects a newer version, or bypasses the Registry-retained generation pin.
API mode, MCP mode, and unrelated failures do not trigger this repair.

If exact resolution or refresh fails, init does not retry the plan. If the one
retry fails, no app config or app receipt is created and the error reports any
snapshot refreshes that already completed. A repeated missing-pin response
directs you to the Engine and Registry logs or another enabled version.
Changing credentials or operation selection does not repair a generation-pin
failure.

SDK mode writes `kind: sdk` with `generate: true`, applies it, and downloads the
package. API mode writes the same resource with `generate: false`, applies it,
and prints a central execution REST request template. MCP mode writes `kind:
mcp`, applies it, and reports the deployed Engine URL and token.

App scaffolding adds only missing required bucket-backed
`server_variable` injections. Existing injections remain authoritative, and
workspace policy or native `x-fused-connect` routing is not duplicated. The
older `sdk init` and `mcp init` scaffold commands remain callable but hidden for
compatibility; `workspace init` remains available for an editable workspace
skeleton. See the `fused-config` OpenAPI/Postman reference for the canonical
Sendbird binding and bucket-value setup.

## `extend <app-name>`

Add services or operations to an existing SDK, direct API app, or MCP server:

```bash
fused-cli extend support-sdk \
  --service slack \
  --select-all slack
```

The command finds the existing `.fused/sdks/<name>.yaml` or
`.fused/mcps/<name>.yaml` file and infers its mode from the validated document.
An SDK config with `generate: false` remains a direct API app. If both SDK and
MCP files use the same name, pass `-f <exact-file>` rather than letting the CLI
guess.

Extension updates that same YAML file. It preserves existing selections,
reports `unchanged` for an idempotent merge, and rejects conflicting identity,
routing, or service-version input before writing. A real change to a stable SemVer
version infers its next minor successor; `1.0.0` becomes `1.1.0`. Terminal
confirmation shows that identity, and `--no-input` or `CI=true` uses the same
deterministic inference. An idempotent repeat keeps the current version. Pass
`--version` to override inference; prerelease and non-SemVer versions require it.

`--service <key>[=<version>]`, `--operation
<service>=<operationId>`, and `--select-all <service>` retain the same meanings
as init. Omit operation flags in a terminal to accept all operations or use the
searchable selector. The command reuses the same activation, plan, apply, SDK
download, API next-step, and MCP deployment functions as initial creation.

## `skill`

List, inspect, or install the Fused skills bundled with this CLI. Installed
skills teach supported coding agents the relevant discovery, configuration,
credential, and plan/apply workflows.

### `skill list`

List every bundled skill. Pass `--json` for structured name and summary fields.

### `skill print [skill-file]`

Print `SKILL.md` or one referenced skill file. `--skill` defaults to
`fused-cli`; pass `--json` for structured skill, file, and content fields.

### `skill install --for <agent>`

Install all bundled skills into the selected agent's normal skill/rules
directory. Supported agents are `codex`, `claude`, `antigravity`, `cursor`, and
`windsurf`.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--for` | | Target agent or app (required) | `""` |
| `--skill` | | Install one named skill instead of all skills | `""` |
| `--scope` | | `user` or `project`; Cursor and Windsurf support project only | target default |
| `--path` | | Exact destination; requires `--skill` | `""` |

The command writes files only; it does not inject a skill into an already
running agent context. Binary release archives include `skills/<version>/`, and
the official installers preserve it beside the executable. `skill install`
reads that immutable local snapshot first, avoiding network drift; GitHub and
embedded content remain fallbacks for older and `go install` installations.

## `service search`
Search workspace and Registry services together for read-only browsing. Pass a
non-empty `--q <provider-or-product>` and optionally `--json`. Results already
enabled in the workspace are listed first with `workspace_status: enabled`;
Registry-only results use `workspace_status: available_to_add`. The CLI uses
Registry's existing bounded search, then one Engine lookup limited to that
query/result set—it does not load the full workspace. This combined view
requires both `catalogue.read` and `service.read`.

You do not need to run this before adding a service: `workspace service add
<query-or-slug>` performs the same workspace-first resolution and Registry
fallback itself.

## `service versions <service-slug>`
List Registry versions visible to the current account for a service slug. Supports provider-qualified slugs such as `@provider/slug`.
The command requests only version identity, status, and execution-contract
compatibility fields; it does not download documentation or execution policies.
Pass `--json` to return those bounded summary objects directly.

## `service show <service-slug>`
Show the description, base URL, servers, and supported authentication methods
for a service. Pass `--json` for stable machine-readable metadata, including
the reusable provider-qualified slug and complete non-secret auth contract.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print service metadata as JSON | `false` |

## `service operations <service-slug>`
List or search Registry operations for a service. Passing `--q` uses the server-side endpoint search path and supports pagination.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | | Search query for service operations | `""` |
| `--version` | | Service version for operations | `""` |
| `--limit` | | Maximum rows to read; requires `--q` | `20` |
| `--offset` | | Rows to skip before reading; requires `--q` | `0` |
| `--json` | | Print operation summaries, descriptions, documentation, and security requirements as JSON | `false` |

## `service operation show <service-slug> <operation-name>`

Show one operation grounded in an exact service version. Parameters,
description, documentation, protocol, and ordered security requirements are
returned by default. Request and response contracts are opt-in so large schemas
do not unnecessarily fill an agent's context.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Exact service version (required) | `""` |
| `--json` | | Print operation detail as JSON | `false` |
| `--include-request` | | Include the request content contract | `false` |
| `--include-responses` | | Include response contracts | `false` |

## `service webhooks <service-slug>`
List Registry webhook definitions for a service.
Pass `--json` to return webhook definitions directly.

## `secret set <service-slug>`
Set authentication credentials for a workspace service. In a terminal, the
command prompts for a supported authentication method and securely collects its
value by default. Use `--value-stdin` for automation; it is required with
`--no-input` or `CI=true`. Credential values are rejected in argv so they cannot
leak through shell history or process listings.
If the service supports multiple authentication families, use `--type` to
specify the family. If that family has multiple named schemes, also pass the
exact `--auth-name`; the CLI never silently chooses the first same-family
scheme. Interactive mode selects the exact scheme directly.

> **Paired credentials are ONE structured value, not two commands.** There is no
> `--username`/`--password`/`--cert`/`--key` flag and no separate `set` call
> per field. Send both fields as one `;`-delimited
> `key=value;key=value` value over stdin:
> ```shell
> printf '%s' 'username=x;password=y' | fused-cli secret set jira --type basic --value-stdin
> printf '%s' 'cert=...;key=...' | fused-cli secret set jira --type mtls --value-stdin
> printf '%s' 'client_id=...;client_secret=...' | fused-cli secret set gmail --bucket default --type oauth --auth-name oauth2 --value-stdin
> ```
> Without `--value-stdin`, a terminal supplies both fields through protected prompts.

OAuth/OIDC application credentials are encrypted bucket secrets, separate from
each connected user's access, refresh, and ID tokens. The Engine derives the
callback from its canonical public URL; `redirect_uri` is never accepted as
credential input.

> **Recommended Pattern:** Pipe API keys, tokens, and service credentials to `fused-cli secret set <service-slug> --value-stdin`. OAuth/OIDC application pairs must include the exact `--bucket <bucket-name>`; other schemes may use it to override the default. This stores secrets securely in Fused's encrypted vault.

> **Tip:** To see available authentication families and their exact scheme names, run `fused-cli service show <service-slug>`.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Explicitly require authentication prompts (the terminal default) | `false` |
| `--value-stdin` | | Read the credential value from stdin | `false` |
| `--type` | | Authentication family (for example `bearer`, `api_key`, or `oauth`) | `""` |
| `--auth-name` | | Exact Registry auth scheme name; required when the selected family has multiple schemes | `""` |
| `--bucket` | | Bucket name or full UUID; required for OAuth/OIDC application credentials | `""` |
| `--expires-at` | | RFC3339 expiry timestamp (e.g. 2026-12-31T23:59:59Z) | `""` |

## `secret list`
List secret metadata in a specific bucket. Secret values are never read back.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--bucket` | | Bucket name or full UUID to inspect (required) | `""` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `secret delete <service-slug> <key-name>`
Delete a workspace secret. Use `--bucket <bucket-name-or-id>` for an override secret.

## `bucket create <bucket-name>`
Create a new bucket for storing overrides and secrets.

## `bucket list`
List workspace buckets from the Engine GraphQL page used by the UI.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket show <bucket-name-or-id>`
Show bucket counts and metadata.

## `bucket services <bucket-name-or-id>`
List services represented in a bucket, including secret, value, Connect config, and connected-user counts.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket secrets <bucket-name-or-id>`
List secret metadata in a bucket without reading secret values.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket values <bucket-name-or-id>`
List non-secret values in a bucket.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket connections <bucket-name-or-id>`
List connected users in a bucket. Filters are sent to Engine GraphQL.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--service` | | Service slug to filter connections | `""` |
| `--user` | | End-user reference to filter connections | `""` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket sdks <bucket-name-or-id>`
List SDKs linked to a bucket. Each relationship applies to every version of
the SDK.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket delete <bucket-name>`
Delete a workspace bucket.

## `value set <bucket-name-or-id> <service-slug> <location> <key-name> <value>`
Set a non-secret configuration value in a bucket. Location can be `env`, `body`, `query`, `header`, or `path`.

## `value list <bucket-name-or-id>`
List all non-secret values stored in a bucket.

## `value delete <bucket-name-or-id> <service-slug> <key-name>`
Delete a non-secret value from a bucket.

## Teams and access

Commands accept team slugs and user email addresses. Bucket names and workspace
service slugs are accepted at their respective resource boundaries. App access
mutations require the `SDK_ID` or `MCP_ID` displayed by list commands because
the generic `app` command cannot infer SDK versus MCP from a potentially shared
name. Kind-specific SDK/MCP commands can resolve names safely. List
operations are paginated by the Engine; use `--limit` and `--offset` rather
than fetching an entire workspace into the CLI.

Provider connection and discovered-resource IDs remain opaque IDs because
providers do not guarantee a stable, workspace-unique human name for them.

### `team list`
List active teams. Use `--search <text>` to search names and slugs and
`--include-archived` to include archived teams.

### `team show <team-slug-or-id>`
Show a team and its workspace, service, bucket, SDK, and MCP bindings.

### `team eligible-owners`
List active teams the current caller can select as the owner of a new SDK, MCP
server, or webhook. Supports `--search`, `--limit`, and `--offset`.

### `team create <team-name>`
Create a team. Optional flags are `--slug` and `--description`.

### `team update <team-slug-or-id>`
Update a team with one or more of `--name`, `--slug`, or `--description`.
Pass an empty `--description` value to clear it.

### `team archive <team-slug-or-id>`
Archive a team after its bindings and owned apps have been removed or
transferred.

### `team member list <team-slug-or-id>`
List team members. Supports `--limit` and `--offset`.

### `team member add <team-slug-or-id> <email>`
Add a person to a team, creating their person record when needed. Use
`--role member` (default) or `--role manager`.

### `team member remove <team-slug-or-id> <user-email-or-id>`
Remove a person from a team.

### `team access workspace set <team-slug-or-id> <role>`
Set the team's workspace role. The canonical roles are `owner`, `admin`,
`builder`, and `viewer`.

### `team access workspace clear <team-slug-or-id>`
Clear the team's workspace role.

### `team access service grant|revoke <team-slug-or-id> <service-slug-or-id> <level>`
Grant or revoke `use` or `manage` access to a service.

### `team access bucket grant|revoke <team-slug-or-id> <bucket-name-or-id> <level>`
Grant or revoke `use` or `manage` access to a bucket.

### `team access app grant|revoke <team-slug-or-id> <sdk-or-mcp-id> <level>`
Grant or revoke `read`, `use`, or `manage` for an SDK or MCP server. The ID is
shown as `SDK_ID` or `MCP_ID`, and the grant applies to every version.

### `team build-access <team-slug-or-id>`
List services or buckets available to both the current caller and the team.
Use `--resource service|bucket`, with optional `--search`, `--limit`, and
`--offset`.

## People and personal credentials

### `user list`
List active people. Use `--search <text>` to search names and email addresses,
`--include-suspended` to include suspended people, and `--limit`/`--offset` for
pagination.

### `user show <email-or-id>`
Show a person, their team memberships, and non-secret credential metadata.

### `user create <email>`
Add a person without sending an email. `--name <display-name>` is required.

### `user update <email-or-id>`
Update a person with `--email`, `--name`, or both.

### `user suspend <email-or-id>` / `user reactivate <email-or-id>`
Suspend a person and stop their credentials, or reactivate a suspended person.

### `user credential issue <email-or-id>`
Issue a personal credential. `--name` defaults to `personal`. The raw key is
printed once and is never written to config, logs, or OTEL.

### `user credential revoke <email-or-id> <credential-name-or-id>`
Revoke a personal credential.

## `sdk plan` / `mcp plan`
Preview SDK package or MCP server changes. Each command reads only its matching
config kind, so an MCP plan never generates SDK code and an SDK plan never
deploys an MCP server. Plan runs the same local/offline validation as
`validate` before its first Engine request.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan result JSON, including summary and notifications; the default receipt is still written | `false` |
| `--interactive` | `-i` | Explicitly require SDK credential prompting when needed (the terminal default); incompatible with `--json`, CI, and `--no-input` | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |
| `--owner-team` | | Optional owning team slug; defaults to the authenticated person | `""` |

Terminal SDK planning may receive successful `credential_readiness` metadata,
offer one secure write to the exact YAML-resolved bucket, and retry once.
Declining preserves the valid plan. It never creates or substitutes a bucket.
Static auth is stored through the ordinary secret path; OAuth/OIDC prompts for
the bucket's client registration rather than an end-user provider token. MCP
planning reports the same non-blocking readiness but does not prompt.

## `sdk apply` / `mcp apply`
Apply an SDK generation plan or deploy an MCP server plan.

Generated-SDK apply returns after Engine durably queues the non-runnable build;
package generation continues in Engine's background finalizer. Use `sdk show
<name@version-or-version-id>` to inspect `generation_status`. `--download`
waits through Engine-local Version ID status reads before downloading, without
replaying apply or depending on a Registry event stream.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--download` | | Download generated SDKs after `sdk apply`; unavailable for MCP | `false` |
| `--json` | | Print SDK apply, generation, and download outcomes as JSON; unavailable for MCP | `false` |
| `--plan-id` | | Apply a specific remote plan ID | `""` |
| `--receipt` | | Read a specific plan receipt | `""` |

## `sdk sync`
Full-mirror a local SDK config from the exact Engine app version declared in that file. There is no implicit latest lookup or sync-time version upgrade.

Usage: `fused-cli sdk sync <sdk-name> -f .fused/sdks/<sdk-name>.yaml`

## `sdk validate` / `mcp validate`
Validate only the matching SDK or MCP configuration files without an Engine
request. This remains useful for offline checks; `plan` already performs this
validation first. Inherits global flags.

## `sdk list` / `mcp list`
List generated SDKs or deployed MCP servers. The kind is fixed by the command.
`SDK_ID` or `MCP_ID` remains stable across versions; `VERSION_ID` identifies
one exact immutable version.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `sdk show <sdk-name@version-or-version-id>`
Show one exact SDK version from the Engine.

## `sdk services <sdk-name@version-or-version-id>`
List services selected by one exact SDK version.

## `sdk buckets <sdk-name-or-id>`
List credential buckets shared by all versions of an SDK.

## `mcp deactivate <mcp-name@version-or-version-id>`
Deactivate exactly one MCP server version. Human-readable references must use
`name@version`; a version ID can identify the version directly. There is no
implicit latest version.

## `sdk download <sdk-name@version-or-version-id>`
Download one exact generated SDK version through the Engine. Human-readable
references must use `name@version`; a version ID can identify the version
directly. There is no implicit latest version.

```bash
fused-cli sdk download security-sdk@1.2.0
```

The package is extracted to `<out>/fused-sdks/<sdk-name>`. When the generated
archive includes an Agent Skill, the CLI installs it centrally at
`<out>/fused-sdks/.agents/skills/<skill-name>/SKILL.md` so all downloaded SDK
skills share one discovery root.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--out` | `-o` | Output directory for the SDK | `"."` |
| `--json` | | Print SDK, Version ID, status, and output path as JSON | `false` |

## `sdk openapi <sdk-name@version-or-version-id>`

Export an OpenAPI 3.1 document for one exact immutable **generated SDK**
version. Use `api openapi` for an app created with `init --api` or any SDK
configuration whose `generate` field is `false`. The CLI
resolves the Version ID with its ordinary control credential and `app.read`,
then calls `GET /apps/{app_id}/openapi`; an SDK execution token does not
authorize this export. The document describes the real
`POST /v1/apps/{app_id}/executions` route, pins that path to the resolved
Version ID, and declares the SDK execution token as the runtime Bearer
credential.

The command always atomically writes a file and never writes the document to
stdout. YAML is the default; JSON is available for consumers that require it.
The generated server URL is the configured Engine URL. Redirects are rejected,
and the CLI accepts at most 16 MiB from the Engine. Before replacing the file,
the CLI verifies that the document identifies the resolved Version ID and
contains the actual app-execution POST path with the matching `app_id` enum and
a request-branch count consistent with the declared operation count.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--operation` | | Export one exact physical or Unified operation name; surrounding whitespace is rejected and the value is case-sensitive, non-empty, and at most 512 bytes | `""` |
| `--out` | `-o` | Atomic output file path | `<safe-sdk-name>-<version>.openapi.<format>`; Version ID input uses `<version-id>.openapi.<format>` |
| `--format` | | Output format: `yaml` or `json` | `"yaml"` |
| `--json` | | Print export metadata only (SDK, version, Version ID, operation, `operation_count`, format, path, bytes, `sha256:<64 lowercase hex>`, server URL, and status) | `false` |

## `api openapi <api-name@version-or-version-id>`

Export the OpenAPI 3.1 execution contract for one exact immutable direct REST
API version created with `init --api` or represented by an SDK configuration
with `generate: false`.

```bash
fused-cli api openapi billing-api@1.2.0 --out billing-api.openapi.yaml
```

The export has the same immutable-version resolution, `app.read` control
credential, operation filtering, atomic file writing, size bound, and document
validation described for `sdk openapi`. It describes the same Engine execution
route and runtime Bearer token contract. Its generated filename and `--json`
metadata identify the resource as an API instead of an SDK.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--operation` | | Export one exact physical or Unified operation name | `""` |
| `--out` | `-o` | Atomic output file path | `<safe-api-name>-<version>.openapi.<format>`; Version ID input uses `<version-id>.openapi.<format>` |
| `--format` | | Output format: `yaml` or `json` | `"yaml"` |
| `--json` | | Print export metadata only, using the `api` field for the resource name | `false` |

## `sdk invoke <sdk-name@version-or-version-id> <operation>`

Invoke one JSON operation through the Engine REST execution route configured by
the global `--engine-url` / `FUSED_ENGINE_URL` setting. The SDK execution token
comes from `FUSED_SDK_TOKEN`, a variable named by `--token-env`, or stdin with
`--token-stdin`; management and provider credentials are never substituted.
Generated SDKs retain their broader gRPC transports; this CLI smoke-test surface
accepts only buffered JSON provider responses up to 1 MiB each and bounds a
Unified aggregate response at 17 MiB.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--params` | | One duplicate-free JSON value, `@file`, or `-` for stdin; physical operations require an object | `"{}"` |
| `--token-env` | | Environment variable containing the execution token | `"FUSED_SDK_TOKEN"` |
| `--token-stdin` | | Read the execution token from stdin | `false` |
| `--environment` | | Physical provider environment selector; sugar for `--selector` | `""` |
| `--target` | | Required for Unified: explicit unique target; repeat 1–16 times. Omit for physical operations. | `[]` |
| `--selector` | | Strict physical selector JSON object or `@file` | `""` |
| `--selectors` | | Strict Unified service-selector map or `@file` | `""` |
| `--idempotency-key` | | Stable logical-request key; generated when omitted | `""` |
| `--json` | | Print Engine endpoint, inferred kind, results, rollbacks, and timing | `false` |

## `sdk activity <sdk-name@version-or-version-id>`

List canonical Engine execution receipts. Use `--all-versions` for the entire
SDK and `--status`, `--start`, or `--end` to narrow the page. Requires both
`app.read` and `audit.read`.

## `sdk service add <service-slug>`
Add a service to an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Specific version to use for the service | `""` |

## `sdk service remove <service-slug>`
Remove a service from an SDK configuration. Inherits global flags.

## `sdk operation add <service-slug> [operation-id...]`
Add OpenAPI operation IDs to an existing SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive operation selection | `false` |
| `--apply` | | Apply changes after adding operation | `false` |
| `--download` | | Download SDK after apply (implies `--apply`) | `false` |

## `sdk operation remove <service-slug> <operation-id...>`
Remove OpenAPI operation IDs from an existing SDK configuration. Inherits global flags.

## `sdk token generate <sdk-name-or-id> <token-name>`
Generate a named execution token that works across every active or deprecated
version of the SDK. Use `--expires-in 4h` to grant temporary full-SDK execution
access for testing; omit it for a non-expiring service token. Pass `--json` to
receive the one-time token, absolute expiry, and metadata as a structured object.

## `sdk token list <sdk-name-or-id>`
List execution-token metadata for an SDK, including expired tokens that may
still need to be inspected or revoked.

## `sdk token revoke <sdk-name-or-id> <token-name>`
Revoke an SDK execution token by name.

## `mcp token generate <mcp-name-or-id> <token-name>`
Generate a named MCP execution token. It defaults to full access (`--allow "*"`)
with no expiry. Repeat `--allow <operation-id>` (or pass a comma-separated list)
to narrow the token, and use `--expires-in 15m` to make it short-lived.

## `mcp token list <mcp-name-or-id>`
List MCP execution-token metadata, including its operation allowlist, expiry,
last use, and creation time.

## `mcp token revoke <mcp-name-or-id> <token-name>`
Revoke an MCP execution token by name.

## `sdk webhook add <service-slug> [webhook-id...]`
Add webhooks to an existing SDK configuration. Use `--interactive` to select
webhooks when IDs are omitted.

## `sdk webhook remove <service-slug> <webhook-id...>`
Remove webhooks from an existing SDK configuration.

## `webhook plan`
Preview changes for webhook registration configurations.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan result JSON, including summary and notifications, instead of writing the default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |
| `--owner-team` | | Optional owning team slug; defaults to the authenticated person | `""` |

## `webhook apply`
Apply a generated plan for webhook registration configurations.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID for a single webhook config | `""` |
| `--receipt` | | Read a specific plan receipt for a single webhook config | `""` |

## `webhook validate`
Validate webhook registration configuration files (`kind: webhook`).

## `workspace plan`
Preview changes that will be made to your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan result JSON, including summary and notifications, instead of writing the default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |

## `workspace apply`
Apply a generated plan to activate Workspace changes.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID | `""` |
| `--receipt` | | Read a specific plan receipt | `""` |

## `workspace sync`
Full-mirror the local workspace config from the Engine's current activation state. Engine state wins: services activated remotely are added or updated locally, and local services no longer activated remotely are removed.

Usage: `fused-cli workspace sync -f .fused/workspace.yaml`

## `workspace access list`
List buckets and SDK/MCP permission scopes shared for bounded workspace-wide use. The
Engine filters and paginates this collection; the CLI does not load the full
workspace and filter it locally.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--resource` | | Filter by `bucket` or `app` | `""` |
| `--limit` | | Maximum rows to return | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `workspace access bucket grant <bucket-name-or-id>`
Grant every authenticated workspace member and eligible app-owning team
bounded use of a bucket. Ownership and secret, value, connection, and bucket
management remain unchanged.

## `workspace access bucket revoke <bucket-name-or-id>`
Remove workspace-wide bucket use. Owner and explicit team bindings remain.

## `workspace access app grant <sdk-or-mcp-id>`
Grant bounded workspace-wide read/use of an SDK or MCP server. Use the
`SDK_ID` or `MCP_ID` shown by the list command. The grant applies to every
version. It does not grant lifecycle or token
management and does not replace the runtime token.

## `workspace access app revoke <sdk-or-mcp-id>`
Remove workspace-wide SDK or MCP use. Owner and explicit team bindings remain.

## `workspace services list`
List workspace services along with their enabled versions.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | `-q` | Filter visible workspace services by name or slug | `""` |
| `--interactive` | `-i` | Interactive service selection | `false` |
| `--json` | | Print visible workspace services as JSON | `false` |

## `workspace services refresh-missing-contracts`
Explicitly refresh a bounded batch of activated service versions whose runtime
contract snapshot is missing or does not retain a valid generation pin. Each
refresh reacquires the exact immutable contract from Registry; it does not
derive a pin locally or move a service to another version. The command reports
the number found, refreshed, and failed.

This is an advanced workspace-maintenance command. It runs only when invoked
and processes at most `--limit` candidates per call. Generated `init --sdk`
uses a narrower recovery path that refreshes only the versions selected by that
SDK candidate.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum missing or unpinned activated service versions to refresh | `100` |

## `workspace has`
Check if a specific service is available in the workspace and output its enabled versions.
Usage: `fused-cli workspace has <service-name>`

## `workspace service add <service-query-or-slug> [service-query-or-slug...]`
Resolve enabled workspace services first, then fall back to Registry search.
After atomically updating the local config, the command prints each canonical
service slug and a direct Engine UI link backed by its stable service ID. Review
the changes with `workspace plan` before applying them, or pass `--apply` to
activate only these additions through Engine's scoped service boundary.

Find and add a service to workspace configuration. The command first checks the
access-filtered workspace service list. If there is no exact name or slug match,
it uses the Registry's set-based reference resolver with the same lexical
ranking and provider-identity rules as `service search`. In a terminal, an
ambiguous match opens service selection and every Registry fallback is confirmed
before the file is written. In CI or with `--no-input`, a unique or exact match
is added deterministically; ambiguous callers must supply an exact slug or
service ID.

Provider-qualified additions must use the complete `@provider/service-slug`
form. A leading `@` with a missing, blank, whitespace-containing, or nested
segment is rejected locally before discovery. The read-only `service search`
command remains lexical and may accept incomplete `@` text as a search query.

A workspace lookup permission error is not treated as absence and never falls
through to Registry search. Registry fallback requires `catalogue.read`.
Without `--apply`, the command only authors the local YAML; it neither activates
the service nor proves the caller has activation permission. Run `workspace
plan`, review the resolved identity and permission checks, and then run
`workspace apply` to apply the full declarative workspace. With `--apply`, the
command instead composes the existing scoped additive activation once for each
resolved service and cannot remove unrelated active services. `service search`
remains available for explicit read-only browsing, but is not a prerequisite.
If a later scoped activation fails, the error lists committed, failed, and
unattempted services and prints a stable code, failed phase, composite request
ID, whether the failed target may have committed, and exact ID-pinned recovery
commands. Re-running those commands is safe because the scoped Engine addition
is idempotent.

One `--version` value applies to every positional service reference. Omit it to
let Engine resolve each service's current public version, or run separate add
commands when the services need different explicit versions.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Version to enable; omitted resolves latest during plan or scoped activation | `""` |
| `--service-id` | | Registry service UUID to store in workspace config | `""` |
| `--interactive` | `-i` | Explicitly require service selection and Registry confirmation (the terminal default) | `false` |
| `--apply` | | Activate only the services added by this command | `false` |

## `workspace service delete <service-slug>`
Delete a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal when the generated plan action is applied | `false` |

## `workspace service deprecate <service-slug>`
Deprecate a service in your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--at` | | Deprecation effective date in YYYY-MM-DD | `""` |
| `--reason` | | Reason for deprecation | `""` |

## `workspace service versions <service-slug>`
List versions enabled in the workspace for a service slug. Supports provider-qualified slugs such as `@provider/slug`.

## `workspace service operations <service-slug>`
List or search operationIds available for an enabled workspace service. Pagination is server-side with or without `--q`.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | | Search query for the operations action | `""` |
| `--version` | | Enabled workspace service version; omitted uses the latest enabled version | `""` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `workspace service webhooks <service-slug>`
List the service's workspace webhook registrations without exposing signing
secrets.

## `workspace service connect <service-slug>`
Start an OAuth/OIDC connection session for an end user.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--bucket` | | Workspace bucket name or full UUID (required) | `""` |
| `--user-ref` | | Stable end-user reference (required) | `""` |
| `--type` | | OAuth/OIDC family; pass with `--auth-name` to select an exact scheme | `""` |
| `--auth-name` | | Exact OAuth/OIDC scheme; pass with `--type` | `""` |
| `--auth-ref` | | Optional source application registration in the exact form `${bucket.auth.<service>.<auth-name>}` | `""` |
| `--resource-input` | | Tenant input as `key=value`; repeatable | |
| `--scope` | | OAuth/OIDC scope; repeatable | |

Standalone consent resolves a reused application registration only from the
explicit `--auth-ref`. This initialization/debug command has no SDK or MCP
identity selector. Generated runtimes instead use the
`services.<target>.auth.ref` pinned in their app configuration.

## `workspace service version add <service-slug> <version>`
Add an allowed version to a workspace service. Inherits global flags.

## `workspace service version delete <service-slug> <version>`
Delete a specific version of a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal | `false` |

## `workspace service version deprecate <service-slug> <version>`
Deprecate a specific version of a service in your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--at` | | Deprecation effective date in YYYY-MM-DD | `""` |
| `--reason` | | Reason for deprecation | `""` |

## `import plan`
Parse an auto-detected OpenAPI 3, Swagger 2, Google Discovery, AsyncAPI, Postman Collection, WSDL, GraphQL SDL, or introspectable GraphQL endpoint, resolve the provider-version action, and diff it against the live service. Read-only apart from the plan record.

Usage: `fused-cli import plan [spec-path]` or `fused-cli import plan --url <http(s)-url>`

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--name` | | Service name (required) | `""` |
| `--slug` | | Account-scoped service slug to create or update (required) | `""` |
| `--url` | | Import from an online HTTP(S) source | `""` |
| `--version` | | Source provider version fallback when the specification does not declare one | `""` |
| `--destination-version` | | Existing provider version to augment; valid only with `--target webhooks` | `""` |
| `--target` | | Contract content to import: `all`, `endpoints`, or `webhooks` | `"endpoints"` |
| `--public` | | Mark a new service public | `false` |
| `--category` | | Category for a new service | `""` |
| `--overlay` | | Local overlay file sent unchanged for Registry-owned canonicalization | `""` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |
| `--json` | | Print the raw plan response as JSON instead of a summary | `false` |
| `--strict` | | Reject warning or error import diagnostics before a plan is persisted | `false` |

## `import apply`
Commit the exact source and optional overlay reviewed by `import plan` or
`import discover`. With no flags, this command reads
`.fused/.state/import.plan.json`; pass `--receipt <path>` when discovery or
planning wrote a different receipt. The receipt's combined review hash
authorizes the reviewed result; source and overlay hashes are informational.
Service, provider version, contract rows, immutable internal revision, and plan
completion are written atomically.

Import plan/apply requests use a 20-minute timeout unless the global `--timeout`
flag is explicitly set. A timeout leaves the apply outcome unknown; run the
exact `fused-cli import status <operation-id>` command reported by the error.
Reapplying the exact plan ID and review hash is idempotent and returns the
stored committed result instead of mutating Registry again.

Registry publication may commit before Engine workspace activation fails. In
that case the command exits non-zero with the structured code
`import_workspace_activation_failed`, phase `workspace_activation`, commit state
`committed`, the request and operation IDs, and an exact pinned
`workspace service add ... --apply` recovery command. The service is already
published; run the recovery command instead of replaying the import.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID (requires `--review-hash`) | `""` |
| `--review-hash` | | Combined Registry review hash to pair with `--plan-id` | `""` |
| `--receipt` | | Read a specific plan receipt (default: most recent local receipt) | `""` |

## `import status <operation-id>`
Read the durable outcome of an import apply without retrying its mutation. The
immutable plan ID is also the operation ID. Human output reports status, phase,
commit state, and a compact service/version result after a complete commit;
`--json` exposes the complete raw status projection. A pending row conservatively
reports an unknown commit state because another apply may still hold its lock;
run the returned status command again instead of replaying apply. Terminal
failed or incomplete historical results include a stable code and a planning
recovery command rather than directing callers into a status loop.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print the raw operation status as JSON | `false` |

## `import discover`
Resolve a machine-readable specification or crawl provider documentation, review exact operations and optional Fused enrichment, then produce the ordinary import-plan receipt. This command never applies the plan.

Usage: `fused-cli import discover (--url <http(s)-provider-url> --name <service-name> --slug <service-slug> | --session <session-id>) [--all | --select METHOD:/path]`

In an interactive terminal, the command shows the operation selector, opens
the Engine's browser review when the draft is ready, and waits for that review
to finish. `--no-browser` prints the review URL and waits without opening it.
Global `--no-input` uses only flags and typed actions: pass `--all` or repeat
`--select`, then repeat `--accept-proposal` or pass `--reject-enrichment`.
Non-interactive execution does not open or wait for browser review.

On `plan_ready`, discovery atomically writes
`.fused/.state/import.plan.json`, replacing the previous receipt at that path.
Use `--receipt-out` to retain the plan elsewhere. Applying is always a separate
`fused-cli import apply` invocation.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--name` | | Service name (required unless resuming) | `""` |
| `--slug` | | Account-scoped service slug to create or update (required unless resuming) | `""` |
| `--url` | | Provider specification or documentation URL (required unless resuming) | `""` |
| `--session` | | Resume this authoritative discovery session; cannot change start identity or limits | `""` |
| `--version` | | Provider version when it cannot be discovered | `""` |
| `--source-mode` | | Source admission strategy: `auto`, `spec`, or `docs` | `"auto"` |
| `--workers` | | Requested extraction workers; Registry clamps the value | `0` |
| `--max-pages` | | Requested documentation page ceiling; Registry clamps the value | `0` |
| `--max-depth` | | Requested documentation crawl depth; Registry clamps the value | `0` |
| `--all` | | Explicitly select every discovered operation | `false` |
| `--select` | | Exact operation as `METHOD:/path`; repeat for several | `[]` |
| `--accept-proposal` | | Accept one exact enrichment proposal ID; repeat for several | `[]` |
| `--reject-enrichment` | | Reject every optional enrichment proposal | `false` |
| `--overlay` | | Local JSON Fused overlay submitted for reviewed validation | `""` |
| `--receipt-out` | | Write the resulting import-plan receipt to a specific path | `""` |
| `--no-browser` | | Print the browser review URL and wait instead of opening it | `false` |
| `--json` | | Print the final plan-ready snapshot as JSON | `false` |
| `--timeout` | | Maximum discovery session duration | `20m0s` |

## Global `plan` / `apply` / `validate`
The CLI also supports top-level `plan`, `apply`, and `validate` commands to process all configurations (both SDKs and workspaces).

**`validate`**
Validates the syntax and references for all Fused configurations in the target directory or file. Inherits global flags.

**`plan`**

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan result JSON, including summary and notifications, instead of writing the default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |
| `--owner-team` | | Optional owning team slug for selected SDK, MCP, and webhook configs; defaults to the authenticated person | `""` |

**`apply`**

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--download` | | Download generated SDKs after apply | `false` |
| `--plan-id` | | Apply a specific remote plan ID for a single config | `""` |
| `--receipt` | | Read a specific plan receipt for a single config | `""` |

Plan output includes the Engine's required permission checks. Human output
prints them under `Required permissions`; `--json` exposes them as
`required_permissions` for agents and CI policy checks.
