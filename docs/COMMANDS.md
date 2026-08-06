# Fused CLI Command Reference

The full list of commands and flags for `fused-cli`. For installation, configuration, and a
walkthrough of common workflows, see the main [README](../README.md).

## Global Flags
All commands support the following global flags:

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--key` | | Engine credential (overrides saved login, `FUSED_API_KEY`, and `FUSED_LICENSE_KEY`) | `""` |
| `--engine-url` | | Fused Engine URL (overrides config & `FUSED_ENGINE_URL`) | `""` |
| `--file` | `-f` | Path to a Fused config file (disables `.fused/` discovery) | `""` |
| `--no-input` | | Fail instead of prompting; also enabled by `CI=true` | `false` |
| `--timeout` | | Maximum duration for an Engine request | `30s` |
| `--request-id` | | Attach an audit correlation ID to every Engine request | `""` |
| `--readme` | | Print the full CLI reference (this file plus the README) and exit | `false` |

All Engine requests have a finite timeout and are cancelled when the CLI
receives SIGINT or SIGTERM. `CI=true` and `FUSED_NO_UPDATE_CHECK=1` disable
release update checks. A command that would prompt fails with remediation when
`--no-input` or `CI=true` is active.

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
`fused-build-sdk` skill and execute the deterministic SDK workflow directly,
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

## `service versions <service-slug>`
List Registry versions visible to the current account for a service slug. Supports provider-qualified slugs such as `@provider/slug`.

## `service show <service-slug>`
Show the base URL, servers, and supported authentication methods for a service.

## `service operations <service-slug>`
List or search Registry operations for a service. Passing `--q` uses the server-side endpoint search path and supports pagination.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | | Search query for service operations | `""` |
| `--version` | | Service version for operations | `""` |
| `--limit` | | Maximum rows to read; requires `--q` | `20` |
| `--offset` | | Rows to skip before reading; requires `--q` | `0` |

## `service webhooks <service-slug>`
List Registry webhook definitions for a service.

## `secret set <service-slug>`
Set authentication credentials for a workspace service. Choose exactly one
input mode: `--interactive` for protected prompts or `--value-stdin` for
automation. Credential values are rejected in argv so they cannot leak through
shell history or process listings.
If the service supports multiple authentication methods, use `--type` to specify the method, or use `--interactive` to select from a menu.

> **`basic`/`mtls` credentials are ONE value, not two.** There is no
> `--username`/`--password`/`--cert`/`--key` flag and no separate `set` call
> per field. Send both fields as one `;`-delimited
> `key=value;key=value` value over stdin:
> ```shell
> printf '%s' 'username=x;password=y' | fused-cli secret set jira --type basic --value-stdin
> printf '%s' 'cert=...;key=...' | fused-cli secret set jira --type mtls --value-stdin
> ```
> Or pass `--interactive` to supply both fields via prompts.

> **Recommended Pattern:** Pipe API keys, tokens, and service credentials to `fused-cli secret set <service-slug> --value-stdin`. Add `--bucket <bucket-name>` for a bucket override. This stores secrets securely in Fused's encrypted vault.

> **Tip:** To see the available authentication methods (and their logical names for the `--type` flag) for a service, run `fused-cli service show <service-slug>`.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Force interactive mode to select the authentication method | `false` |
| `--value-stdin` | | Read the credential value from stdin | `false` |
| `--type` | | Specify the logical authentication method name (e.g., bearerAuth) | `""` |
| `--bucket` | | Bucket name or full UUID; omit to use the default bucket | `""` |
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

## `connect set <service-slug>`
Register or rotate a bucket's OAuth/OIDC app registration (`client_id`/`client_secret`/`redirect_uri`) for a service. This is an immediate admin action -- no workspace.yaml field, no plan/apply -- and it is the only way to register the app; a declarative `connect:` workspace.yaml block existed previously and was removed in favor of this single command. Every field is required the first time; afterward, omitting a field leaves it unchanged (a key present but blank is rejected as an attempt to blank out a credential).

There is no `--client-id`/`--client-secret`/`--redirect-uri` flag. Send the
whole registration as one `;`-delimited value over stdin; the literal key
names must be `client_id`, `client_secret`, and `redirect_uri`:
```shell
printf '%s' 'client_id=...;client_secret=...;redirect_uri=https://engine.example.com/workspace/connect/callback' | fused-cli connect set jira --bucket company-credentials --value-stdin
```
Or use `--interactive` to be prompted per field. Values in argv are rejected.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--bucket` | | Bucket name or full UUID to register this connect config against (required) | `""` |
| `--type` | | Disambiguate when a service declares both `oauth` and `oidc` | `""` |
| `--interactive` | `-i` | Prompt per field instead of parsing an inline value | `false` |
| `--value-stdin` | | Read registration fields from stdin | `false` |

## `connect get <service-slug>`
Read back a bucket's registered OAuth/OIDC app: `auth_type`, `enabled`, `redirect_uri` in plaintext, plus `has_client_id`/`has_client_secret` as booleans -- never the decrypted `client_id`/`client_secret`. This is the only way to check registration state on demand: `bucket services <bucket-name-or-id>` shows just a count, and neither `workspace.yaml` nor `workspace sync` reflect this at all. Fails with a clear error, not a raw 404, when nothing has been registered yet.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--bucket` | | Bucket name or full UUID to look up (required) | `""` |

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
deploys an MCP server.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan result JSON, including summary and notifications, instead of writing the default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |
| `--owner-team` | | Optional owning team slug; defaults to the authenticated person | `""` |

## `sdk apply` / `mcp apply`
Apply an SDK generation plan or deploy an MCP server plan.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--download` | | Download generated SDKs after `sdk apply`; unavailable for MCP | `false` |
| `--plan-id` | | Apply a specific remote plan ID | `""` |
| `--receipt` | | Read a specific plan receipt | `""` |

## `sdk sync`
Full-mirror a local SDK config from the exact Engine app version declared in that file. There is no implicit latest lookup or sync-time version upgrade.

Usage: `fused-cli sdk sync <sdk-name> -f .fused/sdks/<sdk-name>.yaml`

## `sdk validate` / `mcp validate`
Validate only the matching SDK or MCP configuration files. Inherits global flags.

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

## `sdk token list <sdk-name-or-id>`
List runtime tokens shared by every active or deprecated version of an SDK.

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

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--out` | `-o` | Output directory for the SDK | `"."` |

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
version of the SDK.

## `sdk token list <sdk-name-or-id>`
List active execution tokens for an SDK.

## `sdk token revoke <sdk-name-or-id> <token-name-or-id>`
Revoke an SDK execution token by its label or ID.

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
| `--interactive` | `-i` | Interactive service selection | `false` |

## `workspace has`
Check if a specific service is available in the workspace and output its enabled versions.
Usage: `fused-cli workspace has <service-name>`

## `workspace service add <service-slug>`
Add a service to your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Default version to add | `""` |
| `--service-id` | | Optional service ID to store when editing a local config directly | `""` |

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
List or search operationIds available for an enabled workspace service. Passing `--q` uses server-side endpoint search and supports pagination.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | | Search query for the operations action | `""` |
| `--version` | | Enabled workspace service version; omitted uses the latest enabled version | `""` |
| `--limit` | | Maximum rows to read; requires `--q` | `20` |
| `--offset` | | Rows to skip before reading; requires `--q` | `0` |

## `workspace service webhooks <service-slug>`
List the service's workspace webhook registrations without exposing signing
secrets.

## `workspace service connect <service-slug>`
Start an OAuth/OIDC connection session for an end user.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--bucket` | | Workspace bucket name or full UUID (required) | `""` |
| `--user-ref` | | Stable end-user reference (required) | `""` |
| `--sdk` | | Optional exact SDK `name@version` or app UUID for audit attribution | `""` |
| `--resource-input` | | Tenant input as `key=value`; repeatable | |
| `--scope` | | OAuth/OIDC scope; repeatable | |

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

## `import apply`
Commit the exact source reviewed by `import plan`. Service, provider version, contract rows, immutable internal revision, and plan completion are written atomically.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID (requires `--source-hash`) | `""` |
| `--source-hash` | | Source hash to pair with `--plan-id` | `""` |
| `--receipt` | | Read a specific plan receipt (default: most recent local receipt) | `""` |

## `import docs`
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
