# Fused CLI Command Reference

The full list of commands and flags for `fused-cli`. Start with the main
[README](../README.md), then use the focused guides for
[setup and operation](SETUP.md), [service imports](IMPORTING_SERVICES.md), and
[config as code](CONFIG_AS_CODE.md).

## Global Flags
All commands support the following global flags:

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--key` | | Fused credential (overrides saved login, `FUSED_API_KEY`, and `FUSED_LICENSE_KEY`) | `""` |
| `--engine-url` | | Fused URL (overrides config & `FUSED_ENGINE_URL`) | `""` |
| `--file` | `-f` | Path to a Fused config file (disables `.fused/` discovery) | `""` |
| `--no-input` | | Fail instead of prompting; also enabled by `CI=true` | `false` |
| `--timeout` | | Maximum duration for a Fused request; spec imports use `20m0s` unless explicitly set | `1m0s` |
| `--request-id` | | Attach an audit correlation ID to every Fused request | `""` |
| `--readme` | | Print the concise CLI onboarding README and exit | `false` |

All Fused requests have a finite timeout and are cancelled when the CLI
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
import and SDK apply, token generation, invocation, and activity also provide
stable JSON.

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
      "service_slug": "stripe",
      "bucket_name": "production",
      "command": "fused-cli connect 'stripe' --bucket 'production' --type 'api_key' --auth-name 'providerKey'"
    },
    "trace_id": "...",
    "http_status": 409,
    "command": "fused-cli app invoke support-sdk@1.0.0 listIssues"
  }
}
```

Fields unavailable for a particular failure are omitted. Fused errors retain
their safe structured fields through CLI wrapping; untrusted response bodies
are still excluded. Cobra usage failures use `invalid_arguments`; other local
CLI failures use `command_failed` with the original message and a help-oriented
remediation. OTEL records the stable error code and retryability without
recording remote messages or command input.

## `login`

Open the selected Fused's sign-in page and save a subject-scoped CLI
credential after browser approval. The page supports managed Fused Auth and an
existing Fused API key. The generated credential never passes through the
browser and is not printed.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--no-browser` | | Print the sign-in URL instead of opening a browser | `false` |

## `whoami`

Show the non-secret Fused identity used by the effective credential. The
command follows normal `--key` / saved config / environment resolution and
prints identity, account, workspace, credential source, authentication method,
and expiry when reported.

Pass `--json` for Fused URL, local credential source, and non-secret
identity response as structured fields.

## `logout`

Revoke a Fused-issued managed CLI login and remove its saved local
credential. Logout deliberately uses the saved Fused URL and saved credential,
not `--key` or environment overrides. A failed remote revocation preserves the
local login for retry; an already-inactive login is cleared locally. The saved
`engine-url` remains configured. Manually saved API keys are left unchanged
because they are not managed CLI logins.

## `config`
Manage your local CLI configuration (`set`, `get`, `list`, `reset`). Inherits global flags.

## `init <app-name>`

Create one App with SDK, MCP, and REST delivery by default. Explicit method
flags create only a typed SDK, a direct Fused REST app, or a Fused-hosted MCP
server. Top-level init composes the existing workspace and app plan/apply
functions; it does not introduce a new resource kind or receipt boundary.

```bash
# Create one App with all three delivery methods
fused-cli init customer-app \
  --service '@google/drive' \
  --operation '@google/drive=listFiles'

# Generate, apply, and download a typed SDK
fused-cli init google-workspace --sdk \
  --service '@google/drive' \
  --operation '@google/drive=listFiles'

# Apply the same unified app without generating a package
fused-cli init google-api --rest \
  --service '@google/drive' \
  --select-all '@google/drive'

# Deploy an Engine-hosted MCP server
fused-cli init support-agent --mcp \
  --description 'Help support teams manage issues' \
  --service jira \
  --select-all jira
```

The default writes one `kind: sdk` config with `generate: true` and an
`mcp` section. Apply creates one App family and one immutable version, then
downloads the typed package and reports the hosted MCP URLs. The issued
execution token works for generated SDK, MCP, and REST calls to that App.

`--service <service>[@<version>]` accepts comma-separated values or repeated
flags. `--operation <service>=<operationId>` and `--select-all <service>` are
repeatable. An omitted provider version resolves to
the latest enabled workspace version, or the latest public Registry version
when activation is needed. New SDK/MCP configs default to app version `1.0.0`;
generated SDKs default to TypeScript. Use `--language go` for a Go module or
`--language python` for Python. Go requires version 1.24 or later and supports
typed operations, SSE iteration, mixed response media, connected OAuth helpers,
durable webhook receivers, and hosted Unified App bindings. Generated Go packages
include a README with their local module identity and `go mod edit -replace`
installation instructions. `--bucket` only references an existing
bucket and is omitted by default—it never creates a bucket.

`--no-apply` writes and validates the same desired state, saves every plan
receipt whose dependencies are active, and stops before apply, generation, or
download. If a service is not active yet, the workspace plan receipt is saved
and app planning is deferred until workspace apply; otherwise the app plan
receipt is saved immediately. The command prints the exact remaining commands,
including `app apply --download` for generated SDKs, and tells users to re-plan
if a retained receipt becomes stale.

Omitting the method flag creates the combined App in terminals and automation.
If a
service has no operation flags, standalone MCP apps with imported capabilities
show a searchable combined picker with nothing preselected. Other services use
the endpoint selector, whose highlighted default is **All operations**;
choose the narrower path to search by operation ID, method, path, description,
or tag. In automation, `--no-input` or `CI=true` requires
explicit endpoint flags or, for standalone MCP apps, imported flags such as
`--mcp-tool` for every service. Non-interactive
MCP creation also requires `--description`.

Creation refuses to replace an existing path before any remote plan. Extension is additive and
idempotent through the top-level `extend` command described below.

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

For generated SDK delivery, including the default combined App,
`generation_contract_pin_unavailable` starts one bounded legacy-snapshot
repair. The CLI resolves every selected active
`service@version` before changing anything, visibly refreshes each exact Fused
snapshot, and retries the unchanged app plan once. `--no-input` follows the
same deterministic path without prompting. The CLI never substitutes a runtime
hash, selects a newer version, or bypasses the Registry-retained generation pin.
API mode, MCP mode, and unrelated failures do not trigger this repair.

If exact resolution or refresh fails, init does not retry the plan. If the one
retry fails, no app config or app receipt is created and the error reports any
snapshot refreshes that already completed. A repeated missing-pin response
directs you to Fused and Registry logs or another enabled version.
Changing credentials or operation selection does not repair a generation-pin
failure.

SDK mode writes `kind: sdk` with `generate: true`, applies it, and downloads the
package. API mode writes the same resource with `generate: false`, applies it,
and prints a central execution REST request template. MCP mode writes `kind:
mcp`, applies it, and reports the deployed Fused URL and token.

When service `auth` is omitted, Fused planning checks the selected bucket for
complete credentials compatible with the selected operations. A single ready
scheme is selected and shown in the plan summary; multiple ready schemes require
explicit `auth.type` and `auth.name`. If none is ready, planning retains provider
ordering and reports missing credentials. This applies to SDK, MCP, and Unified
App plans, including per-service bucket overrides. Explicit auth remains
authoritative, and apply uses the saved choice rather than selecting again.

If an SDK, MCP or combined App plan reports missing credentials, interactive
`init` names each service, auth scheme and resolved bucket before publication.
Choose **Set up credentials here** (the ordinary masked, confirmed secret
collector), **Open bucket setup in the browser**, **Recheck credentials and
continue**, **Proceed anyway**, or **Cancel app creation**. Browser setup opens
the selected Fused's bucket page without sending credentials in the URL;
bucket permissions still apply. Rechecking creates a fresh plan for the same
config. It does not invoke provider operations. Cancelling stops before app
apply or publishing the candidate app file; any already-applied workspace
activation or explicitly saved credentials remain and are not rolled back.

Readiness checks cover stored required credential material, not provider
validity or every connected user's OAuth token. Per-service bucket overrides
are preserved. `--no-input` and `CI=true` keep their non-interactive behavior:
warnings and exact setup commands are printed, and publication can continue
without secret writes or browser prompts. `--no-apply` does not enter the
new publication-choice menu.

App scaffolding adds only missing required bucket-backed
`server_variable` injections. Existing injections remain authoritative, and
workspace policy or native `x-fused-connect` routing is not duplicated. Use top-level `init` for creation and `extend` for additions. See the `fused-config` OpenAPI/Postman reference for the canonical
Sendbird binding and bucket-value setup.

## `describe <goal>`

Turn a natural-language goal into a reviewable Unified App by default.
`describe` shows the drafted TypeScript source and exact selected operations,
then sends that cart to Fused for compilation and deployment after approval.
Use `--kind sdk|mcp|rest` to request another output. Use `init` without a method
flag when you want one App with SDK, MCP, and REST delivery.

```bash
fused-cli describe 'Create a customer onboarding app using Stripe'
fused-cli describe --kind sdk 'Create a TypeScript SDK for Stripe customer lookup'
fused-cli describe --kind mcp 'Help support teams find and update Jira issues'
fused-cli describe --update billing-sdk 'Add Stripe invoice lookup'
```

`describe` requires an interactive terminal. It resolves exact Registry
services and operations, shows the proposal, and applies it only after
confirmation; `--no-input` and CI cannot approve a model-derived scope.
Operation selection uses Jev through Fused Registry, and goal parsing uses
Registry's configured language model. The CLI discloses those requests before
sending them. SDK and MCP proposals can attach provider webhook events; direct
REST apps have no event receiver.

Unified App creation needs 1–16 explicit operations. `describe` saves
`.fused/unified_app/<app>.tsx` and a YAML `source_path` reference. Edit that
TypeScript file and deploy a new version to update it. `app source extract` moves
inline YAML source into the editable file. `--update <app-name>` applies to
existing SDK, MCP, and REST configs and publishes an immutable successor when
the reviewed proposal changes it. Use `-f <path>` when a name is ambiguous.
`--name`, `--version`, `--language`, and `--bucket` constrain creation; updates
preserve omitted settings. A missing update target stops before creating
anything. See the [`buildUnifiedApp` guide](UNIFIED_APPS.md) for the
function contract and the [complete example](unified-apps/example.md) for
config and API calls.

## `workspace init`

Create an editable aggregate workspace skeleton at `.fused/workspace.yaml`, or
at the global `--file` path. Creation refuses to replace an existing file;
`--extend` adds service selections without duplicating existing entries. This
local authoring command does not activate or remove Fused services.

```bash
fused-cli workspace init
fused-cli workspace init --service stripe@v1,github@v3
fused-cli workspace init --extend --service slack
```

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--service` | | Service as `<service>[@<version>]`; comma-separated or repeatable | `[]` |
| `--extend` | | Merge services into an existing workspace config | `false` |
| `--json` | | Print the scaffold result as JSON | `false` |

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

`--service <service>[@<version>]`, `--operation
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

## `service list`

List enabled workspace services. Use `--search <query>` to filter. Add `--registry` with a non-empty search to read the Registry catalog instead. Registry results retain reusable provider-qualified slugs and require `catalogue.read`; they do not perform a workspace membership lookup. Both scopes support `--json`.

`service add <query-or-slug>` already checks the workspace and falls back to Registry discovery when needed, so a separate search is optional.

## `service version list --registry <service-slug>`
List Registry versions visible to the current account for a service slug. Supports provider-qualified slugs such as `@provider/slug`.
The command requests only version identity, status, and execution-contract
compatibility fields; it does not download documentation or execution policies.
Pass `--json` to return those bounded summary objects directly.

## `service edit <service-slug> --registry`

Edit the display name or description of a service owned by your account. Requires
Engine `service.manage` permission; Registry also enforces service ownership.
Workspace admin access alone does not let you edit another provider's service.

```shell
fused-cli service edit my-service --name "Customer API" --description "Customer lookup and updates."
fused-cli service edit my-service --description ""
```

Omitted fields stay unchanged. An empty description clears it. The slug, service
ID, and version contracts stay unchanged. Requires a Registry and Engine version
that supports `PATCH /integrations/{id}/details`.

## `service show --registry <service-slug>`
Show the description, base URL, servers, and supported authentication methods
for a service. Pass `--json` for stable machine-readable metadata, including
the reusable provider-qualified slug and complete non-secret auth contract.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print service metadata as JSON | `false` |

## `service operation list --registry <service-slug>`
List or search Registry operations for a service. Passing `--search` uses the server-side endpoint search path and supports pagination.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--search` | | Search query for service operations | `""` |
| `--version` | | Service version for operations | `""` |
| `--limit` | | Maximum rows to read; requires `--search` | `20` |
| `--offset` | | Rows to skip before reading; requires `--search` | `0` |
| `--json` | | Print operation summaries, descriptions, documentation, and security requirements as JSON | `false` |

## `service operation show --registry <service-slug> <operation-name>`

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

## `service webhook list --registry <service-slug>`
List Registry webhook definitions for a service.
Pass `--json` to return webhook definitions directly.

## `connect <service-name>`

Configure authentication for a service in a bucket. `--bucket` accepts a name or
UUID and defaults to `default`. The picker lists every matching method with
`Configured`, `Not configured`, `Expired`, or `Incomplete` status. Configured
methods have a checkmark and an edit label; select one to replace its values.
Status comes from metadata, and existing secret values are never displayed or
prefilled. Cancelling leaves credentials unchanged.

In a terminal, choose an authentication method and enter its fields. Secret
fields are masked. The picker also appears for a sole method or when assignments
are supplied; values never infer or filter the authentication type.
Use `--type` to select an authentication family and
`--auth-name` to select an exact scheme when needed. OAuth/OIDC input is the
application client ID and secret; starting end-user OAuth consent remains
`service connect`.

```shell
fused-cli connect stripe
fused-cli connect stripe --bucket production
fused-cli connect stripe "<auth-name>=$STRIPE_KEY" --type api_key --no-input --json
fused-cli connect gmail "client_id=$CLIENT_ID, client_secret=$CLIENT_SECRET" --type oauth --no-input
```

Supply credentials in one quoted `"name=value, name=value"` argument. Single-value schemes use
the exact auth scheme name; paired families use `client_id`/`client_secret`,
`username`/`password`, or `cert`/`key`. Quote shell variables as `"name=$VARIABLE"`;
the shell resolves them before invocation. Commas separate fields; spaces before
field names are optional. Values retain embedded `=` and `;`. Use interactive
setup for values containing literal commas. `--no-input`,
CI, and JSON output require explicit input when setup is needed and never open
prompts. With multiple matching methods, these modes require `--type` and/or
`--auth-name` to select exactly one method. Explicit assignments set or replace
the selected method. With no assignments, an already configured method returns
its status; missing credentials require input. `--expires-at` accepts an optional RFC3339 expiry. JSON contains only
bucket ID, auth family, scheme name, and `configured` or `saved` status.

## `bucket credential delete <service-slug> <key-name>`
Delete a workspace secret. Use `--bucket <bucket-name-or-id>` for an override secret.

## `bucket create <bucket-name>`
Create a new bucket for storing overrides and secrets.

## `bucket list`
List workspace buckets from Fused GraphQL page used by the UI.

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

## `bucket secret list <bucket-name-or-id>`
List secret metadata in a bucket without reading secret values.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket secret|env|cert set|get|remove`

Use `--bucket <name-or-id>` to select a bucket; omission selects `default`.
`set` accepts one or more `<name>[=<value>]` arguments. The first equals sign separates
the name from the value, preserving later equals signs and colons in URLs or tokens. If the value
is omitted, a terminal prompt collects it; secret prompts are masked and
certificate prompts support multiline PEM. Use separate arguments for multiple
entries. `get` and `remove` each accept one name without a value.

```shell
fused-cli bucket secret set webhook_signing
fused-cli bucket env set REGION=eu API_URL=https://api.example.com
fused-cli bucket env get REGION --bucket production
fused-cli bucket env remove REGION
fused-cli bucket cert set "root=$ROOT_CERT"
fused-cli bucket cert get root
fused-cli bucket cert remove root
fused-cli bucket secret set "webhook_signing=$WEBHOOK_SECRET" --no-input --exportable
fused-cli bucket secret get webhook_signing
fused-cli bucket secret remove webhook_signing
```

Values may be literal or supplied by shell environment expansion (`"name=$VARIABLE"`).
`--no-input`, CI, and JSON writes require explicit assignments.
Environment entries may contain an explicitly empty value (`NAME=`). Generic
secrets are encrypted, service-independent values. Environment entries are
non-secret bucket-wide values; service-specific values take precedence during
runtime lookup. Certificates are public X.509 PEM certificates or chains, stored
separately from same-name environment entries; private keys are not accepted.

Secret `set` additionally supports `--expires-at` and `--exportable`. Secret
`get` uses the audited reveal endpoint and requires both export opt-in and
`credentials.reveal`; it never changes that policy. `get` prints the exact value
without an added newline, or a JSON object with `name` and `value`. Mutations
return metadata only. A multi-entry set validates and collects the batch before
writing; a server failure stops the batch and reports how many entries were
confirmed saved, without replaying the failed write.

## `bucket connections <bucket-name-or-id>`
List connected users in a bucket. Filters are sent to Fused GraphQL.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--service` | | Service filter as `<service>[@<version>]`; comma-separated or repeatable | `[]` |
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

## `bucket value set <bucket-name-or-id> <service-slug> <location> <key-name> <value>`
Set a non-secret configuration value in a bucket. Location can be `env`, `body`, `query`, `header`, or `path`.

## `bucket value list <bucket-name-or-id>`
List a page of non-secret values stored in a bucket.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print the page as JSON | `false` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket value delete <bucket-name-or-id> <service-slug> <key-name>`
Delete a non-secret value from a bucket.

## Teams and access

Teams and people belong to the selected workspace. Use `workspace team` and
`workspace user`; these are the only supported identity-management paths.

Commands accept team slugs and user email addresses. Bucket names and workspace
service slugs are accepted at their respective resource boundaries. App access
mutations require the stable `APP_ID` displayed by `app list`; an exact ID avoids ambiguous names across app types. Kind-specific SDK/MCP commands can resolve names safely. List
operations are paginated by Fused; use `--limit` and `--offset` rather
than fetching an entire workspace into the CLI.

Provider connection and discovered-resource IDs remain opaque IDs because
providers do not guarantee a stable, workspace-unique human name for them.

### `workspace team list`
List active teams. Use `--search <text>` to search names and slugs and
`--include-archived` to include archived teams.

### `workspace team show <team-slug-or-id>`
Show a team and its workspace, service, bucket, SDK, and MCP bindings.

### `workspace team eligible-owners`
List active teams the current caller can select as the owner of a new SDK, MCP
server, or webhook. Supports `--search`, `--limit`, and `--offset`.

### `workspace team create <team-name>`
Create a team. Optional flags are `--slug` and `--description`.

### `workspace team update <team-slug-or-id>`
Update a team with one or more of `--name`, `--slug`, or `--description`.
Pass an empty `--description` value to clear it.

### `workspace team archive <team-slug-or-id>`
Archive a team after its bindings and owned apps have been removed or
transferred.

### `workspace team member list <team-slug-or-id>`
List team members. Supports `--limit` and `--offset`.

### `workspace team member add <team-slug-or-id> <email>`
Add a person to a team, creating their person record when needed. Use
`--role member` (default) or `--role manager`.

### `workspace team member remove <team-slug-or-id> <user-email-or-id>`
Remove a person from a team.

### `workspace team access workspace set <team-slug-or-id> <role>`
Set the team's workspace role. The canonical roles are `owner`, `admin`,
`builder`, and `viewer`.

### `workspace team access workspace clear <team-slug-or-id>`
Clear the team's workspace role.

### `workspace team access service grant|revoke <team-slug-or-id> <service-slug-or-id> <level>`
Grant or revoke `use` or `manage` access to a service.

### `workspace team access bucket grant|revoke <team-slug-or-id> <bucket-name-or-id> <level>`
Grant or revoke `use` or `manage` access to a bucket.

### `workspace team access app grant|revoke <team-slug-or-id> <app-id> <level>`
Grant or revoke `read`, `use`, or `manage` for an SDK or MCP server. The ID is
shown as `APP_ID`, and the grant applies to every version.

### `workspace team build-access <team-slug-or-id>`
List services or buckets available to both the current caller and the team.
Use `--resource service|bucket`, with optional `--search`, `--limit`, and
`--offset`.

## People and personal credentials

### `workspace user list`
List active people. Use `--search <text>` to search names and email addresses,
`--include-suspended` to include suspended people, and `--limit`/`--offset` for
pagination.

### `workspace user show <email-or-id>`
Show a person, their team memberships, and non-secret credential metadata.

### `workspace user create <email>`
Add a person without sending an email. `--name <display-name>` is required.

### `workspace user update <email-or-id>`
Update a person with `--email`, `--name`, or both.

### `workspace user suspend <email-or-id>` / `workspace user reactivate <email-or-id>`
Suspend a person and stop their credentials, or reactivate a suspended person.

### `workspace user credential issue <email-or-id>`
Issue a personal credential. `--name` defaults to `personal`. The raw key is
printed once and is never written to config, logs, or OTEL.

### `workspace user credential revoke <email-or-id> <credential-name-or-id>`
Revoke a personal credential.

## `app plan`

Plan the app type declared by the selected config: SDK, direct REST API (`kind: sdk`, `generate: false`), MCP, or Unified App. Use `-f <path>` to select a file. Without `-f`, discovery ignores workspace/webhook configs and requires one app type; mixed app types require explicit file selection. Local validation runs before any deployment request.

| Flag | Description |
|---|---|
| `--json` | Print a structured plan and permissions |
| `--receipt-out <path>` | Write a plan receipt |
| `--owner-team <slug>` | Select the owning team |
| `--interactive` | Require terminal credential prompting for SDK/API planning |

SDK/API planning can offer one secure credential repair and re-plan in a terminal. MCP planning reports readiness without prompting. Unified App planning extracts inline TypeScript into an editable source file before compilation.

## `app apply`

Apply the saved plan for the app type declared by the config. `--json` returns the type's deployment receipt, including any one-time execution token. Receipt and Engine identity checks run before mutation.

| Flag | Description |
|---|---|
| `--plan-id <id>` | Apply a particular plan |
| `--receipt <path>` | Read a saved plan receipt |
| `--json` | Print structured deployment output for every supported app type |
| `--download` | Wait for generation and download the package; generated SDKs only |

Generated SDKs queue package generation. Use `app show <name@version>` for build status, or `--download` to wait and fetch the package. Direct REST, MCP, and Unified Apps do not provide downloadable SDK packages.

## `app promote [app-name] --version <version>`

Switch new traffic to an existing Unified App version without recompiling or changing local files. Use an exact app name and a version label such as `1.0.0`; you do not need a UUID.

```sh
fused-cli app promote "Customer lookup" --version 1.0.0
fused-cli app promote -f .fused/unified_app/customer-app.yaml --version 1.0.0
```

| Argument | Description |
|----------|-------------|
| `[app-name]` | Exact app name; takes precedence over config selection and needs no local config |
| `-f <path>` | Read the app's top-level `name` from this Unified App config when no positional name is supplied |
| `--version <version>` | Required exact destination version label; never inferred from the config or latest version |
| `--json` | Print `name`, `version`, `app_family_id`, and `active_app_id` in the traffic receipt |

Without a positional name or `-f`, config discovery must find exactly one `kind: unified_app` declaration. Use `-f` when multiple apps are present. The config's `name` identifies the app; its `version` and linked TypeScript remain unchanged by promotion.

You need `app.unified_app.read` and `app.unified_app.manage`. The destination must be a retained version with a runnable bundle; a deleted version cannot be promoted. Historical results remain available after the switch.

Exact-version REST URLs do not redirect: update callers to the selected version's URL. The hosted MCP family URL follows the active version; clients with sessions on the previous version must reconnect. A competing traffic change causes a conflict. Inspect the current destination before retrying; the CLI does not automatically retry promotion.

## Unified App error diagnostics

Public execution errors contain stable codes. Detailed exceptions and captured bodies are available in the app's Requests inspector to actors granted `app.unified_app.diagnostics.read` on its family or workspace. Ordinary read/manage grants do not include this permission; Owner retains all permissions. Requests also needs `app.unified_app.read` and `audit.read`. The management endpoint is `GET /apps/{app_id}/executions/{execution_id}/diagnostics`; runtime execution tokens cannot use it. Evidence expires with the retained result, currently after 24 hours. New compiled bundles include TypeScript source maps.

## `app schedule`

Manage one-time and recurring runs with saved JSON input. Commands accept an
optional app name, or select one Unified App with `-f`. Requires app management
permission and an Engine build with scheduling support.

```sh
fused-cli app schedule create "Reports" --name "December report" --at '2026-12-01T09:00:00Z' --input-file ./input.json
fused-cli app schedule create "Reports" --name "Weekday report" --cron '0 9 * * 1-5' --timezone Europe/London --input-file ./input.json
fused-cli app schedule list "Reports"
fused-cli app schedule update "Reports" --id SCHEDULE_ID --input-file ./revised.json
fused-cli app schedule pause "Reports" --id SCHEDULE_ID
fused-cli app schedule resume "Reports" --id SCHEDULE_ID
fused-cli app schedule delete "Reports" --id SCHEDULE_ID
```

Create and update accept `--name`, `--at` (RFC3339 with an offset), `--cron`
(five fields), `--timezone` (IANA; defaults to UTC on creation), `--input`
(inline JSON), `--input-file` (file or `-` for stdin), and `--paused`.
`--at` and `--cron` are mutually exclusive, as are `--input` and `--input-file`.
Input defaults to `{}` on creation. Updates preserve omitted fields.

All commands support `--json`. JSON listing includes private saved input;
default output omits it. Prefer files or stdin for private input. Update, pause,
resume, and delete require `--id` and use the current revision to reject racing
changes. After an uncertain create response, list schedules before creating
another; the error retains the original schedule ID for reconciliation.

Schedules follow the app's active version and continue after the creator signs
out. Engine must remain running. Missed recurrence times produce one catch-up
run, while resuming skips paused occurrences. Runs never overlap for one
schedule, and failed executions are not automatically retried. Uncertain runs
pause for review. Recurring local times follow daylight-saving transitions:
nonexistent times are skipped and repeated times can run twice. Completed
one-time schedules need a new future `--at` to run again.

## `app bundle attach <app-version-id>`

You usually do not need this command. With `source_path`, Fused compiles the
TypeScript during `app plan`.

Use `bundle attach` only if you compiled the code yourself. Put the compiled
file's checksum in the config as `bundle_digest`, then run `app plan`
and `app apply`. The attach command uploads that JavaScript file and
the compiler's metadata file to the new App version. Pass the plan's
`source_hash` with `--source-hash`, the JavaScript file with `--bundle`, and the
metadata file with `--manifest`.

## `app source extract`

Create `.fused/unified_app/<app>.tsx` from a local Unified App's inline
`source: |`, and replace that block with a relative `source_path`. A linked
source in another location is copied into the canonical path. Existing files
with different contents are left untouched and reported as conflicts.

Usage: `fused-cli app source extract -f .fused/unified_app/<app>.yaml`. Omit `-f` to
sync all discovered local Unified App configs. `app plan` performs the
inline-source conversion automatically before sending the cart to Fused.

## `app sync`
Full-mirror a local SDK config from the exact Fused app version declared in that file. There is no implicit latest lookup or sync-time version upgrade.

Usage: `fused-cli app sync <sdk-name> -f .fused/sdks/<sdk-name>.yaml`

## `app validate`

Validate the selected app config offline. The config determines its type. Supports `--json`.

## `app list`

List each visible app once across SDK, REST API, MCP, and Unified Apps. `--type sdk|rest|mcp|unified-app` filters on the server before pagination. Omit `--type` to list all app types. `--limit` defaults to 20 and `--offset` to 0. `--json` returns `items` and `total`, stable family identity, kind, delivery mode, version count, latest/stable version metadata, and applicable transport URLs.

```sh
fused-cli app list
fused-cli app list --type rest --json
```

## `app versions list [app-name-or-id]`

List immutable versions of one app, or all visible versions when the selector is omitted. Supports `--limit`, `--offset`, and `--json`. Human output distinguishes stable app identity (`APP_ID`) from immutable `VERSION_ID`, and preserves applicable stable/pinned MCP transport URLs.

## `app show <app-name@version-or-version-id>`

Inspect an exact app version. The selected app determines its type; SDK/REST records include best-effort generation diagnostics. There is no implicit latest-version selection.

## `app service list <app-name@version-or-version-id>`

List services selected by an exact app version. Supports `--json`.

## `app operation list <mcp-name@version-or-version-id>`
List every operation ID callable through one exact MCP server version. The
Fused expands `select_all` from its immutable local service-contract snapshots.
Human output shows `OPERATION_ID` and `KIND` (`physical`); `--json`
returns the MCP and Version IDs plus exact physical service provenance. A bare
MCP name without `@version` is rejected so the result cannot float to another
version.

## `app bucket list <app-name-or-id>`
List credential buckets shared by all versions of an app.

## `app deactivate <app-name@version-or-version-id>`
Deactivate exactly one app version. Human-readable references must use
`name@version`; a version ID can identify the version directly. There is no
implicit latest version.

## `app download <sdk-name@version-or-version-id>`
Download one exact generated SDK version through Fused. Human-readable
references must use `name@version`; a version ID can identify the version
directly. There is no implicit latest version.

```bash
fused-cli app download security-sdk@1.2.0
```

The package is extracted to `<out>/fused-sdks/<sdk-name>`. When the generated
archive includes an Agent Skill, the CLI installs it centrally at
`<out>/fused-sdks/.agents/skills/<skill-name>/SKILL.md` so all downloaded SDK
skills share one discovery root.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--out` | `-o` | Output directory for the SDK | `"."` |
| `--json` | | Print SDK, Version ID, status, and output path as JSON | `false` |

## `app openapi <app-name@version-or-version-id>`

Export the OpenAPI 3.1 execution contract for an exact app version, including direct REST API apps created with `init --rest`. Engine determines whether the selected app supports export. The CLI resolves the immutable version with a management credential, then requests `GET /apps/{app_id}/openapi`.

The export describes `POST /v1/apps/{app_id}/executions` and runtime bearer authentication. It validates the returned version identity and writes the document atomically. Redirects are rejected and responses are bounded to 16 MiB.

| Flag | Description |
|---|---|
| `--operation <id>` | Export one exact operation |
| `--out <path>` | Choose the output file |
| `--format` (`yaml` or `json`) | Document format; defaults to YAML |
| `--json` | Print metadata only: `app`, version identity, `operation_count`, path and checksum (`sha256:<64 lowercase hex>`) |

```sh
fused-cli app openapi billing-api@1.2.0 --out billing.openapi.yaml
```

## `app invoke <app-name@version-or-version-id> <operation>`

Invoke one JSON operation through the Fused REST execution route configured by
the global `--engine-url` / `FUSED_ENGINE_URL` setting. The SDK execution token
comes from `FUSED_APP_TOKEN`, a variable named by `--token-env`, or stdin with
`--token-stdin`; management and provider credentials are never substituted.
Generated SDKs retain their broader gRPC transports; this CLI smoke-test surface
accepts only buffered JSON provider responses up to 1 MiB.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--params` | | One duplicate-free JSON object, `@file`, or `-` for stdin | `"{}"` |
| `--token-env` | | Environment variable containing the execution token | `"FUSED_APP_TOKEN"` |
| `--token-stdin` | | Read the execution token from stdin | `false` |
| `--environment` | | Provider environment selector; sugar for `--selector` | `""` |
| `--selector` | | Strict selector JSON object or `@file` | `""` |
| `--idempotency-key` | | Stable logical-request key; generated when omitted | `""` |
| `--json` | | Print Fused endpoint, physical results, status, and timing | `false` |

## `app activity <app-name@version-or-version-id>`

List canonical Fused execution receipts. Use `--all-versions` for the entire
SDK and `--status`, `--start`, or `--end` to narrow the page. Requires both
`app.sdk.read` and `audit.read`.

## `app service add <service-slug>`
Add a service to an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Specific version to use for the service | `""` |

## `app service remove <service-slug>`
Remove a service from an SDK configuration. Inherits global flags.

## `app operation add <service-slug> [operation-id...]`
Add OpenAPI operation IDs to an existing SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive operation selection | `false` |
| `--apply` | | Apply changes after adding operation | `false` |
| `--download` | | Download SDK after apply (implies `--apply`) | `false` |

## `app operation remove <service-slug> <operation-id...>`
Remove OpenAPI operation IDs from an existing SDK configuration. Inherits global flags.

## `app token generate <app-name-or-id> <token-name>`

Generate a family execution token for an SDK, REST API, MCP, or Unified App. `--expires-in 15m` sets its lifetime. `--allow <operation>` and repeatable `--fixed-binding '<service>,<auth>,<user>[,<resource>]'` are available where supported by the selected app. Engine validates the token policy. Omitted `--allow` uses the app type's default policy.

The one-time token and metadata are returned as JSON; keep the token private.

## `app token list <app-name-or-id>`

List token metadata for the selected app family, including expiry and scope.

## `app token revoke <app-name-or-id> <token-name>`

Revoke a named family execution token.

## `app webhook add <service-slug> [webhook-id...]`
Add webhooks to an existing SDK configuration. Use `--interactive` to select
webhooks when IDs are omitted.

## `app webhook remove <service-slug> <webhook-id...>`
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
Pull selected non-secret service configuration from Fused into local files. With no scope flag, sync refreshes only services already declared in `.fused/workspace.yaml` and `.fused/services/**/*.yaml`. A local declaration absent from Fused is retained and reported, never deleted.

```bash
fused-cli workspace sync
fused-cli workspace sync --file .fused/services/payments.yaml
fused-cli workspace sync --service stripe
fused-cli workspace sync --service stripe@v1,github@v3 --file .fused/services/payments.yaml
fused-cli workspace sync --all
```

`--service <service>[@<version>]` accepts comma-separated values or repeated flags. An omitted version pulls every active version; an exact version pulls only that active version while retaining unselected local versions. With `--file`, every selected service is created or updated in that one file; declarations already owned by another local file are transferred to the requested destination while preserving authored policy. Without `--file`, an undeclared service gets its own `.fused/services/<service-slug>.yaml` `type: services` document; colliding readable slugs receive a stable service-identity suffix. `--all` is the only full import. Sync is Fused-to-local only: it never changes Fused state, and local paths are never sent to Fused.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--file` | `-f` | Refresh only services declared in this file, or choose the destination with `--service`/`--all` | `""` |
| `--service` | | Active service as `<service>[@<version>]`; comma-separated or repeatable | `[]` |
| `--all` | | Explicitly import every active service | `false` |

## `workspace access list`
List buckets and SDK/MCP permission scopes shared for bounded workspace-wide use. The
Fused filters and paginates this collection; the CLI does not load the full
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

## `workspace access app grant <app-id>`
Grant bounded workspace-wide read/use of an SDK or MCP server. Use the
`APP_ID` shown by the list command. The grant applies to every
version. It does not grant lifecycle or token
management and does not replace the runtime token.

## `workspace access app revoke <app-id>`
Remove workspace-wide SDK or MCP use. Owner and explicit team bindings remain.

## `service list`
List services along with their enabled versions.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--search` | `-q` | Filter visible services by name or slug | `""` |
| `--interactive` | `-i` | Interactive service selection | `false` |
| `--json` | | Print visible services as JSON | `false` |

## `service refresh-missing-contracts`
Explicitly refresh a bounded batch of activated service version list --registry whose runtime
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
| `--limit` | | Maximum missing or unpinned activated service version list --registry to refresh | `100` |

## `service has`
Check if a specific service is available in the workspace and output its enabled versions.
Usage: `fused-cli service has <service-name>`

## `service add <service-query-or-slug> [service-query-or-slug...]`
Resolve enabled services first, then fall back to Registry search.
Without `--file`, the command immediately activates only the resolved services
through Fused's scoped additive boundary and does not read, create, or update
local workspace files. It prints each canonical service slug and a direct
Fused UI link backed by its stable service ID.

Find and add a service to the workspace. The command first checks the
access-filtered service list. If there is no exact name or slug match,
it uses the Registry's set-based reference resolver with the same lexical
ranking and provider-identity rules as `service list --registry`. In a terminal, an
ambiguous match opens service selection and every Registry fallback is confirmed
before activation or an explicitly requested file write. In CI or with `--no-input`, a unique or exact match
is added deterministically; ambiguous callers must supply an exact slug or
service ID.

Provider-qualified additions must use the complete `@provider/service-slug`
form. A leading `@` with a missing, blank, whitespace-containing, or nested
segment is rejected locally before discovery. The read-only `service list --registry`
command remains lexical and may accept incomplete `@` text as a search query.

A workspace lookup permission error is not treated as absence and never falls
through to Registry search. Registry fallback requires `catalogue.read`.
Passing `--file` explicitly opts into local config authoring: the command
atomically updates that existing `kind: workspace` or `type: services` file and
performs no activation by default. Review it with `workspace plan` and apply it
through the normal declarative workflow, or add `--apply` to author the file and
activate only these additions immediately. The command never implicitly targets
`.fused/workspace.yaml`. `service list --registry` remains available for explicit
read-only browsing, but is not a prerequisite.
If a later scoped activation fails, the error lists committed, failed, and
unattempted services and prints a stable code, failed phase, composite request
ID, whether the failed target may have committed, and exact ID-pinned recovery
commands. Re-running those commands is safe because the scoped Fused addition
is idempotent.

One `--version` value applies to every positional service reference. Omit it to
let Fused resolve each service's current public version, or run separate add
commands when the services need different explicit versions.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Version to enable; omitted resolves latest during plan or scoped activation | `""` |
| `--service-id` | | Exact Registry service UUID to activate and, with `--file`, persist | `""` |
| `--interactive` | `-i` | Explicitly require service selection and Registry confirmation (the terminal default) | `false` |
| `--apply` | | With `--file`, also activate only the services added by this command | `false` |

## `service remove <service-slug>`
Delete a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal when the generated plan action is applied | `false` |

## `service deprecate <service-slug>`
Deprecate a service in your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--at` | | Deprecation effective date in YYYY-MM-DD | `""` |
| `--reason` | | Reason for deprecation | `""` |

## `service version list <service-slug>`
List versions enabled in the workspace for a service slug. Supports provider-qualified slugs such as `@provider/slug`.

## `service operation list <service-slug>`
List or search operationIds available for an enabled service. Pagination is server-side with or without `--search`.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--search` | | Search query for the operations action | `""` |
| `--version` | | Enabled service version; omitted uses the latest enabled version | `""` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `webhook list --service <service-slug>`
List the service's workspace webhook registrations without exposing signing
secrets.

## `service connect <service-slug>`
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

## `service version add <service-slug> <version>`
Add an allowed version to a service. Inherits global flags.

## `service version remove <service-slug> <version>`
Delete a specific version of a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal | `false` |

## `service version deprecate <service-slug> <version>`
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

Registry publication may commit before Fused workspace activation fails. In
that case the command exits non-zero with the structured code
`import_workspace_activation_failed`, phase `workspace_activation`, commit state
`committed`, the request and operation IDs, and an exact pinned
file-free `service add ...` recovery command. The service is already
published; run the recovery command instead of replaying the import.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID (requires `--review-hash`) | `""` |
| `--review-hash` | | Combined Registry review hash to pair with `--plan-id` | `""` |
| `--receipt` | | Read a specific plan receipt (default: most recent local receipt) | `""` |
| `--json` | | Print the validated committed apply proof as JSON | `false` |

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
Fused's browser review when the draft is ready, and waits for that review
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

Plan output includes Fused's required permission checks. Human output
prints them under `Required permissions`; `--json` exposes them as
`required_permissions` for agents and CI policy checks.

## Imported service MCP catalogs

Imported capabilities are supported only by `kind: mcp`. The service version must
already be enabled in the workspace. Catalog import uses Engine's owner checks;
only the service owner with service-management and catalog-import permission can
discover or apply. Saved catalogs are private to the importing actor. Bearer
connections use an existing bucket secret; never pass the provider token to CLI.

```sh
fused-cli service mcp discover demo-crm --version 1.0.0 \
  --url https://mcp.deepwiki.com/mcp --json
# Review the preview, then use its exact id before it expires.
fused-cli service mcp apply demo-crm --version 1.0.0 \
  --draft-id <preview-id>
fused-cli service mcp show demo-crm --version 1.0.0 \
  --type tools --query structure
```

`discover` also previews refreshes and URL changes; it never replaces saved state.
For authenticated discovery, add both `--bucket <existing-bucket>` and
`--secret <generic-secret-name>`. Use that same bucket for the app's service.
`show --json` preserves complete input/output schemas and native definitions.
Types are `tools`, `prompts`, `resources`, and `resource_templates`; text and type
filters intersect and never change selection authority.

Use the same `init` and `extend` lifecycle as endpoint selection:

```sh
fused-cli init repository-docs --mcp \
  --description 'Browse public repository documentation through Demo CRM.' \
  --service demo-crm@1.0.0 --mcp-tool demo-crm=read_wiki_structure
fused-cli extend repository-docs --mcp-tool demo-crm=read_wiki_contents
```

These commands write the config, plan, and apply it. Extension preserves earlier
selections and infers the next minor version for a changed stable SemVer app;
repeating the same selection keeps the version. Use `-f <path>` for an explicit
config, `--no-apply` to save desired state and available receipts for review, or
`--no-token` to skip issuing an execution token. Existing service version list --registry are
inherited on extend, so there is no need to repeat `--service`.

In a terminal, omit selection flags to search and select endpoints, tools,
prompts, resources, and resource templates in one picker. No imported capability
is preselected. Services with no imported capabilities retain the endpoint picker.

For automation or precise selection, repeat any of:

- `--mcp-tool '<service>=<name>'`
- `--mcp-prompt '<service>=<name>'`
- `--mcp-resource '<service>=<URI>'`
- `--mcp-resource-template '<service>=<URI-template>'`

Values retain commas and equals signs. Physical `--operation` and `--select-all`
flags can be combined with these flags. `--mcp-all <service>` freezes all currently
saved capabilities into explicit lists; future catalog additions are excluded.
It cannot be combined with individual imported choices for that service.
Imported flags require standalone `--mcp` on init or an existing `kind: mcp`
config on extend; SDK, REST, and combined apps reject them.

`--mcp-revision '<service>=<approved-revision-id>'` guards against a catalog
changing after review. Extending an existing imported selection onto a different
catalog revision requires this flag. Retained names must still exist in the
reviewed revision; extension never silently removes earlier grants.

An imported-only app needs no physical operation selection:

```yaml
apiVersion: fused/v1
kind: mcp
name: repository-docs
version: 1.0.0
description: Browse public repository documentation through Demo CRM.
bucket: default
services:
  demo-crm:
    version: 1.0.0
    mcp:
      revision_id: <approved-revision-id>
      tools: [read_wiki_structure]
      # prompts: [exact-prompt-name]
      # resources: ["docs://guide"]
      # resource_templates: ["docs://{+path}"]
```

```sh
fused-cli app validate -f mcp.yaml
fused-cli app plan -f mcp.yaml
fused-cli app apply -f mcp.yaml
fused-cli app operation list repository-docs@1.0.0 --json
```

Engine checks exact revision membership and pins definitions in the immutable app
version. Refreshing a catalog does not change deployed apps. Imported tools use
Fused `search_docs` and `execute`; prompts and resources remain native MCP
capabilities. Restricted execution tokens use the exact capability identities
returned by `app operation list`. Provider connections and credentials remain in
Engine; CLI never executes provider tools during import or selection.
