# Build a ready SDK or MCP from a business goal

Use this workflow when the user describes an integration outcome but has not
provided exact Registry slugs, operation IDs, or existing Fused config. Finish
with something they can use: a downloaded SDK package or a deployed MCP server
with its connection details.

## 1. Establish the live CLI and Engine context

Treat the installed CLI's help as authoritative. Inspect only the branches you
will use:

```shell
fused-cli --version
fused-cli config list
fused-cli service --help
fused-cli workspace --help
fused-cli sdk --help       # SDK target
fused-cli mcp --help       # MCP target
```

If the Engine URL or credential is missing, follow the `fused-cli` skill's
first-time setup. Confirm connectivity before authoring config. Never place a
credential in a command argument, transcript, config file committed to source
control, or generated SDK/MCP config.

## 2. Choose the output

- Choose an SDK when the user needs a typed package embedded in application
  code. Confirm language, name, and version, then deliver the downloaded output.
- Choose MCP when the user needs an Engine-hosted tool server for an agent or
  MCP client. Confirm server name and version, then deliver the server URL and
  explain how to obtain/use its token.
- Ask the user only when this choice, a provider ambiguity, credential ownership,
  or a destructive/production apply would materially change the result.

Read `fused-sdk` or `fused-mcp` for the selected config shape. For one generated
SDK method spanning multiple services, also read `fused-unified-operations`.
Read `fused-bucket` when credentials or OAuth are required, and `fused-config`
for auth, connect scopes, connection profiles, or execution policy.

## 3. Find the service once

Use the combined search when you need to browse candidates:

```shell
fused-cli service search --q "<provider, product, or capability>" --json
```

The command searches Registry and performs one bounded Engine lookup. Read
`workspace_status`: reuse `enabled`; treat `available_to_add` as a candidate
that still needs workspace activation. Discovery requires `catalogue.read` and
`service.read`, and results remain access-filtered. A missing result means "no
suitable visible service," not proof that no service exists.

Use `workspace services list --json` only when the user needs the complete
enabled set. Use `workspace has "<exact service name>"` only as an additional
exact-name check; it does not match a slug. As the selection is refined, use
`--json` so agents do not parse human-readable tables.

For an enabled result, inspect its workspace contract:

```shell
fused-cli workspace service versions <slug>
fused-cli workspace service operations <slug> --version <version> --q "<capability>"
```

The returned `slug` is reusable in later commands. Owned services use a bare
slug; public services owned by another provider use `@provider/slug`. If several
results could satisfy the request, compare names/providers and ask the user
before selecting a materially different service.

Registry service search currently returns at most 20 ranked matches and has no
pagination flag. Refine `--q` when the intended provider is not in that set;
do not assume later pages exist.

A search result proves only visibility; it does not prove that the caller may
activate or consume the service.

Then inspect available versions and search operations using the user's own
business terms:

```shell
fused-cli service versions <slug>
fused-cli service show <slug> --json
fused-cli service operations <slug> --version <version> --q "<capability>" --json
```

Prefer a stable public version unless the user requested another version. Keep
the exact operation IDs returned by the CLI. Search results intentionally omit
large request/response schemas. Inspect only the operations that could satisfy
the goal:

```shell
fused-cli service operation show <slug> <operation-name> --version <version> --json
fused-cli service operation show <slug> <operation-name> --version <version> --json --include-request --include-responses
```

Start without body flags, then request only the contract needed to author or
verify the SDK usage. Descriptions and provider-declared security requirements
are authoritative; do not infer parameters or payload fields from an operation
name. Use `select_all: true` only when the user genuinely wants the full surface
and accepts future scope growth.

`workspace has` matches the exact service name, not its slug.

## 4. Add and activate only when needed

Skip this step when the selected search result is already `enabled`. For an
`available_to_add` result, use the existing workspace config and let one command
resolve the workspace first, fall back to Registry, and merge the selected
service without erasing its existing settings:

```shell
fused-cli workspace service add <query-or-slug> [query-or-slug...] [--version <version>] [--apply] -f <workspace-config-path>
```

A unique or exact result is added non-interactively. Use `--interactive` only
when a person is present to choose among ambiguous Registry results and confirm
the write. The command edits local intent; it does not activate the service.
Preview and apply that workspace change before building an SDK or MCP that
depends on it:

```shell
fused-cli validate -f <workspace-config-path>
fused-cli workspace plan
fused-cli workspace apply
```

Activation is permission-gated. Planning a changed service requires
`workspace.read` and `service.manage` for that service. Applying the plan also
requires `workspace.update` and `service.manage`. The built-in Admin and Owner
workspace roles provide those permissions; Builder and Viewer do not. If plan
or apply returns a permission denial while adding the service, stop immediately
and tell the user that the service was found but could not be added. Include the
named missing permission/resource and leave the SDK/MCP outcome explicitly
incomplete. Do not self-grant, silently switch credentials, or keep retrying. An
access administrator can, for example, give the user's team an appropriate
workspace role with `team access workspace set`, or grant scoped service access
with `team access service grant`; follow `reference/access-management.md` and
current command help. A service-specific grant alone does not supply
`workspace.update`.

Use the narrow command only as remediation for an authorised administrator;
do not run it automatically:

```shell
fused-cli team access service grant <team> <service> manage
fused-cli team access workspace set <team> admin
```

The editor operates on an existing config file and requires `-f`.

## 5. Prepare the bucket and authentication

Determine whether each selected operation uses API key, bearer/basic auth,
OAuth/OIDC, mTLS, or no authentication. Run `fused-cli bucket list`; its result
contains only buckets visible through `bucket.read` and does not prove
`bucket.use`. Choose visible `default` or another visible candidate, then run
plan/apply/connect with that exact candidate. On `bucket.use` denial, stop and
report it; never create a fallback. Create only when the user explicitly asks or
states an enterprise, tenant, or environment isolation requirement and the
caller has workspace `bucket.manage`. Creation does not grant `bucket.use`;
never self-grant. Follow `fused-bucket` for the complete policy. Add secret
material through stdin or an interactive prompt, never a command argument. For
OAuth/OIDC, configure the app and start the connect flow; pause for the user's
browser consent when required.

If the output will be team-owned, verify that team before planning. The team
must appear in `eligible-owners` and have build access to every selected service
and bucket:

```shell
fused-cli team eligible-owners
fused-cli team build-access <team> --resource service
fused-cli team build-access <team> --resource bucket
fused-cli sdk plan --owner-team <team>       # or mcp/webhook plan
```

If the requested team is absent from `eligible-owners` or `build-access` shows
a missing dependency, stop before plan. Tell the user which team and resource
failed the preflight; do not silently switch to personal ownership or choose a
different team.

An authorised administrator can fill a missing dependency with the narrowest
scope. These are remediation commands, not permission to change access on the
user's behalf:

```shell
fused-cli team access service grant <team> <service> use
fused-cli team access bucket grant <team> <bucket> use
```

An app may be published while credential readiness remains unresolved. Describe
it as ready for integration development, but make clear that an affected
provider call will stop before dispatch until its bucket value, secret, client
registration, connect scope, or user connection is configured.

## 6. Author and apply the selected output

For an SDK, use top-level init so service activation, operation selection,
optional credential setup, plan/apply, and package download share one guided
flow while retaining separate receipts:

```shell
fused-cli init <name> --sdk --service '<service>[=<version>]'
```

If this generated SDK plan returns the typed
`generation_contract_pin_unavailable` condition, init visibly refreshes only
the exact selected active `service@version` snapshots and retries the same plan
once. The path is deterministic with `--no-input`. Do not replace the retained
pin, change versions, broaden the refresh to the workspace, or retry again if
the refresh or second plan fails. API and MCP init do not use this recovery.

Use `--api` instead when the user wants the central execution REST API without
a generated package. It creates the same `kind: sdk` app with `generate: false`
and prints a request template; API is not a separate resource kind.

For MCP, use the same top-level lifecycle. Before scaffolding, have the LLM write a concise one-to-three
sentence summary of the user-facing work enabled by the selected services.
Pass it with `--description`; do not enumerate operation IDs or describe the
`search_docs`/`execute` mechanics because the prose is advertised as the MCP
server's identity before tool discovery. Then run:

```shell
fused-cli init <name> --mcp --description '<LLM-authored capability summary>' --service '<service>[=<version>]' [...selection flags]
fused-cli mcp list
```

Review plan warnings and required permissions. Respect production warnings and
any approval/owner-team requirements; do not bypass them. After apply, confirm
the created version is active.

SDK/MCP creation and use have separate permissions from workspace activation.
A new plan requires `app.create`, `service.read`, and `bucket.read`; an
existing app plan requires `app.manage` plus the dependency reads.
Apply requires `app.create` for a new resource or `app.manage` for an
existing one, together with `service.consume` and `bucket.use` for every
selected dependency. Use the human-readable denial or `required_permissions`
in JSON plan output to identify the exact resource. An authorised administrator
can use `team access service ... use`, `team access bucket ... use`, or `team
access app ... manage` as appropriate. Do not execute those grants
automatically or broaden a team to Admin merely to consume an already-enabled
service.

On any denial, stop the blocked action, preserve the config and plan, and tell
the user the missing permission and resource. Never self-grant, switch
credentials, broaden scope, or retry with guessed authority. See
`reference/access-management.md` for the full permission matrix and access
commands.

## Completion contract

Report the exact services, versions, operation IDs, bucket, and config paths
used. For SDK, provide the downloaded package path plus the shortest relevant
usage/auth next step. For MCP, provide the deployed server URL and token/client
connection next step. Clearly identify any remaining user action, especially
OAuth consent or secret entry.
