# Fused CLI Command Reference

The full list of commands and flags for `fused-cli`. For installation, configuration, and a
walkthrough of common workflows, see the main [README](../README.md).

## Global Flags
All commands support the following global flags:

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--key` | | API key (overrides config & `FUSED_API_KEY`) | `""` |
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

## `sdk prompt`
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

## `config`
Manage your local CLI configuration (`set`, `get`, `list`, `reset`). Inherits global flags.

## `service <service-slug> versions`
List Registry versions visible to the current account for a service slug. Supports provider-qualified slugs such as `@provider/slug`.

## `service <service-slug> show`
Show the base URL, servers, and supported authentication methods for a service.

## `service <service-slug> operations`
List or search Registry operations for a service. Passing `--q` uses the server-side endpoint search path and supports pagination.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | | Search query for service operations | `""` |
| `--version` | | Service version for operations | `""` |
| `--limit` | | Maximum rows to read; requires `--q` | `20` |
| `--offset` | | Rows to skip before reading; requires `--q` | `0` |

## `service <service-slug> webhooks`
List Registry webhook definitions for a service.

## `secret <service-slug> set [value]`
Set an authentication secret for a workspace service.
If the service supports multiple authentication methods, use `--type` to specify the method, or use the `--interactive` flag to select from a menu.

> **`basic`/`mtls` credentials are ONE value, not two.** There is no
> `--username`/`--password`/`--cert`/`--key` flag and no separate `set` call
> per field. Pack both fields into the single positional `<value>` as a
> `;`-delimited `key=value;key=value` string, quoted so the shell doesn't
> split on the `;`:
> ```shell
> fused-cli secret jira set 'username=x;password=y' --type basic --bucket <bucket>
> fused-cli secret jira set 'cert=...;key=...' --type mtls --bucket <bucket>
> ```
> Or omit the value and pass `--interactive` to supply both fields via prompts instead.

> **Recommended Pattern:** Store API keys, tokens, and service credentials directly using `fused-cli secret set <service-slug> [value] --bucket <bucket>`. This stores secrets securely in Fused's encrypted vault.

> **Tip:** To see the available authentication methods (and their logical names for the `--type` flag) for a service, run `fused-cli service show <service-slug>`.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Force interactive mode to select the authentication method | `false` |
| `--type` | | Specify the logical authentication method name (e.g., bearerAuth) | `""` |
| `--bucket` | | Set secret as an override for a specific Bucket | `""` |
| `--expires-at` | | RFC3339 expiry timestamp (e.g. 2026-12-31T23:59:59Z) | `""` |

## `secret list`
List secret metadata in a specific bucket. Secret values are never read back.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--list-bucket-id` | | Bucket name or ID to inspect | `""` |

## `secret <service-slug> remove <key-name>`
Remove a workspace secret.

## `bucket <name> create`
Create a new bucket for storing overrides and secrets.

## `bucket list`
List workspace buckets from the Engine GraphQL page used by the UI.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket <name-or-id> show`
Show bucket counts and metadata.

## `bucket <name-or-id> services`
List services represented in a bucket, including secret, value, Connect config, and connected-user counts.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket <name-or-id> secrets`
List secret metadata in a bucket without reading secret values.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket <name-or-id> values`
List non-secret values in a bucket.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket <name-or-id> connections`
List connected users in a bucket. Filters are sent to Engine GraphQL.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--service` | | Service slug to filter connections | `""` |
| `--user` | | End-user reference to filter connections | `""` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket <name-or-id> sdks`
List SDK or MCP scopes linked to a bucket.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `bucket <name> remove`
Remove a workspace bucket.

## `connect <service-slug> set [value]`
Register or rotate a bucket's OAuth/OIDC app registration (`client_id`/`client_secret`/`redirect_uri`) for a service. This is an immediate admin action -- no workspace.yaml field, no plan/apply -- and it is the only way to register the app; a declarative `connect:` workspace.yaml block existed previously and was removed in favor of this single command. Every field is required the first time; afterward, omitting a field leaves it unchanged (a key present but blank is rejected as an attempt to blank out a credential).

There is no `--client-id`/`--client-secret`/`--redirect-uri` flag. The single
positional `<value>` is the whole registration as ONE `;`-delimited
`key=value;key=value;key=value` string, quoted so the shell doesn't split on
the `;` -- the literal key names must be `client_id`, `client_secret`, and
`redirect_uri`:
```shell
fused-cli connect jira set 'client_id=...;client_secret=...;redirect_uri=https://engine.example.com/workspace/connect/callback' --bucket <name>
```
Or omit the value and use `--interactive` to be prompted per field.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--bucket` | | Bucket (name or ID) to register this connect config against (required) | `""` |
| `--type` | | Disambiguate when a service declares both `oauth` and `oidc` | `""` |
| `--interactive` | `-i` | Prompt per field instead of parsing an inline value | `false` |

## `connect <service-slug> get`
Read back a bucket's registered OAuth/OIDC app: `auth_type`, `enabled`, `redirect_uri` in plaintext, plus `has_client_id`/`has_client_secret` as booleans -- never the decrypted `client_id`/`client_secret`. This is the only way to check registration state on demand: `bucket <name-or-id> services` shows just a count, and neither `workspace.yaml` nor `workspace sync` reflect this at all. Fails with a clear error, not a raw 404, when nothing has been registered yet.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--bucket` | | Bucket (name or ID) to look up (required) | `""` |

## `value <bucket-id> set <service-slug> <location> <key-name> <value>`
Set a non-secret configuration value in a bucket. Location can be `env`, `body`, `query`, `header`, or `path`.

## `value <bucket-id> list`
List all non-secret values stored in a bucket.

## `value <bucket-id> remove <service-slug> <key-name>`
Remove a non-secret value from a bucket.

## `sdk plan`
Preview changes that will be made to your Fused environment for SDKs.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan result JSON, including summary and notifications, instead of writing the default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |

## `sdk apply`
Apply a generated plan to deploy SDK changes.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--download` | | Download generated SDK after apply | `false` |
| `--plan-id` | | Apply a specific remote plan ID for a single SDK config | `""` |
| `--receipt` | | Read a specific plan receipt for a single SDK config | `""` |

## `sdk sync`
Full-mirror a local SDK config from the most recently generated remote SDK with the given name.

Usage: `fused-cli sdk sync <name> -f .fused/sdks/<name>.yaml`

## `sdk validate`
Validates an SDK configuration file. Inherits global flags.

## `sdk list`
List generated SDK records.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--target` | | Target type to list (`sdk` or `mcp`) | `"sdk"` |
| `--language` | | Target language to filter by | `""` |
| `--latest-only` | | Only show the latest SDK per name | `true` |
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

## `sdk <sdk-name[@version]> show`
Show the generated SDK ID, version, and sandbox URL.

## `sdk <sdk-name[@version]> services`
List services selected by a generated SDK.

## `sdk <sdk-name[@version]> buckets`
List credential buckets linked to a generated SDK scope.

## `sdk <sdk-name[@version]> tokens`
List tokens for the generated SDK.

## `sdk <sdk-name> download`
Download generated SDK artifacts from Registry records through the Engine. Pass an SDK name to download the latest generated SDK, or use `name@version` to request a specific SDK version.

```bash
fused-cli sdk security-sdk download
fused-cli sdk security-sdk@1.2.0 download
```

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--out` | `-o` | Output directory for the SDK | `"."` |

## `sdk service <service-slug> add`
Add a service to an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Specific version to use for the service | `""` |

## `sdk service <service-slug> remove`
Remove a service from an SDK configuration. Inherits global flags.

## `sdk service <service-slug> add <operationId...>`
Add one or more operationIds to an existing service in an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive operation selection | `false` |
| `--apply` | | Apply changes after adding operation | `false` |
| `--download` | | Download SDK after apply (implies `--apply`) | `false` |

## `sdk service <service-slug> remove <operationId...>`
Remove one or more operationIds from an existing service in an SDK configuration. Inherits global flags.

## `sdk operation <service-slug> add [operationId...]`
Compatibility alias for adding operationIds to an existing service in an SDK configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive operation selection | `false` |
| `--apply` | | Apply changes after adding operation | `false` |
| `--download` | | Download SDK after apply (implies `--apply`) | `false` |

## `sdk operation <service-slug> remove <operationId...>`
Compatibility alias for removing operationIds from an existing service in an SDK configuration. Inherits global flags.

## `sdk token <sdk-id> generate <token-name>`
Generate a new SDK token for authenticating SDK requests to the Engine.

## `sdk token <sdk-id> list`
List all active tokens for an SDK.

## `sdk token <sdk-id> revoke <token-name>`
Revoke an SDK token.

## `mcp plan`
Preview changes that will be made to your Fused environment for MCP server configurations (`.fused/mcps/*.yaml`).

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--json` | | Print plan result JSON, including summary and notifications, instead of writing the default receipt | `false` |
| `--receipt-out` | | Write the plan receipt to a specific path | `""` |

## `mcp apply`
Apply a generated plan to deploy MCP server configurations to the Fused Engine runtime.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--plan-id` | | Apply a specific remote plan ID for a single MCP config | `""` |
| `--receipt` | | Read a specific plan receipt for a single MCP config | `""` |

## `mcp validate`
Validates MCP configuration files (`kind: mcp`).

## `mcp list`
List deployed MCP servers from the Engine.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--limit` | | Maximum rows to read | `20` |
| `--offset` | | Rows to skip before reading | `0` |

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

## `workspace services list`
List workspace services along with their enabled versions.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--interactive` | `-i` | Interactive service selection | `false` |

## `workspace has`
Check if a specific service is available in the workspace and output its enabled versions.
Usage: `fused-cli workspace has <service_name>`

## `workspace service <service-slug> add`
Add a service to your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--version` | | Default version to add | `""` |
| `--service-id` | | Optional service ID to store when editing a local config directly | `""` |

## `workspace service <service-slug> remove`
Remove a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal when the generated plan action is applied | `false` |

## `workspace service <service-slug> deprecate`
Deprecate a service in your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--at` | | Deprecation effective date in YYYY-MM-DD | `""` |
| `--reason` | | Reason for deprecation | `""` |

## `workspace service <service-slug> versions`
List versions enabled in the workspace for a service slug. Supports provider-qualified slugs such as `@provider/slug`.

## `workspace service <service-slug> operations`
List or search operationIds available for an enabled workspace service. Passing `--q` uses server-side endpoint search and supports pagination.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--q` | | Search query for the operations action | `""` |
| `--version` | | Enabled workspace service version; omitted uses the latest enabled version | `""` |
| `--limit` | | Maximum rows to read; requires `--q` | `20` |
| `--offset` | | Rows to skip before reading; requires `--q` | `0` |

## `workspace service <service-slug> version add <version>`
Add an allowed version to a workspace service. Inherits global flags.

## `workspace service <service-slug> version remove <version>`
Remove a specific version of a service from your Workspace configuration.

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--force` | | Force removal | `false` |

## `workspace service <service-slug> version deprecate <version>`
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

**`apply`**

| Argument | Short | Description | Default |
|----------|-------|-------------|---------|
| `--download` | | Download generated SDKs after apply | `false` |
| `--plan-id` | | Apply a specific remote plan ID for a single config | `""` |
| `--receipt` | | Read a specific plan receipt for a single config | `""` |
