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

Read `fused-sdk` or `fused-mcp` for the selected config shape. Read
`fused-bucket` when credentials or OAuth are required, and `fused-config` for
auth, connect scopes, connection profiles, or execution policy.

## 3. Search the workspace first

Always look for a suitable already-enabled service before querying the
Registry:

```shell
fused-cli workspace services list --q "<provider or product>"
# Use this additional exact check when the Registry service name is known:
fused-cli workspace has "<exact service name>"
```

Inspect the list's service names, slugs, and enabled versions against the
user's requested provider and capability. If a suitable service is enabled,
reuse its returned slug and stay within workspace commands:

```shell
fused-cli workspace service versions <slug>
fused-cli workspace service operations <slug> --version <version> --q "<capability>"
```

Workspace discovery requires `service.read` and is access-filtered. Do not
query the Registry or try to add the service merely because the user did not
know its slug; use the workspace search to resolve that first. If it returns no
match, say "no suitable visible workspace service" rather than claiming the
service is not enabled, because the caller may lack access to see it.

Only when the workspace has no suitable service, search the Registry:

```shell
fused-cli service search --q "<provider, product, or capability>"
fused-cli service search --q "<query>" --json
```

The returned `slug` is reusable in later commands. Owned services use a bare
slug; public services owned by another provider use `@provider/slug`. If several
results could satisfy the request, compare names/providers and ask the user
before selecting a materially different service.

Registry service search currently returns at most 20 ranked matches and has no
pagination flag. Refine `--q` when the intended provider is not in that set;
do not assume later pages exist.

Registry discovery requires `catalogue.read`. A search result proves that the
service is visible to the caller; it does not prove that the caller may activate
or consume it.

Then inspect available versions and search operations using the user's own
business terms:

```shell
fused-cli service versions <slug>
fused-cli service operations <slug> --version <version> --q "<capability>"
```

Prefer a stable public version unless the user requested another version. Keep
the exact operation IDs returned by the CLI. Use `select_all: true` only when
the user genuinely wants the full surface and accepts future scope growth.

`workspace has` matches the exact service name, not its slug.

## 4. Activate the service in the workspace

Perform this step only for a service found through the Registry fallback. Skip
activation when workspace search already found a suitable enabled service.

Create or update a `kind: workspace` file under `.fused/`, following the
`fused-workspace` skill. Include the chosen service and version. Preview and
apply the workspace change before building an SDK or MCP that depends on it:

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

Inspect current `--help` before using editor commands such as `workspace service
add`; they operate on an existing config file and may require `-f`.

## 5. Prepare the bucket and authentication

Determine whether each selected operation uses API key, bearer/basic auth,
OAuth/OIDC, mTLS, or no authentication. Reuse an appropriate existing bucket or
create one according to `fused-bucket`. Add secret material through stdin or an
interactive prompt, never a command argument. For OAuth/OIDC, configure the app
and start the connect flow; pause for the user's browser consent when required.

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

Do not claim the integration is ready while required bucket values, secrets,
connect scopes, or user connections are unresolved.

## 6. Author and apply the selected output

For an SDK, create `.fused/sdks/<name>.yaml` using `fused-sdk`, then run:

```shell
fused-cli sdk validate
fused-cli sdk plan
fused-cli sdk apply --download
# Or download an already-generated version:
fused-cli sdk download <name>@<version>
```

For MCP, create `.fused/mcps/<name>.yaml` using `fused-mcp`, then run:

```shell
fused-cli mcp validate
fused-cli mcp plan
fused-cli mcp apply
fused-cli mcp list
```

Review plan warnings and required permissions. Respect production warnings and
any approval/owner-team requirements; do not bypass them. After apply, confirm
the created version is active.

SDK/MCP creation and use have separate permissions from workspace activation.
A new plan requires `artifact.create`, `service.read`, and `bucket.read`; an
existing artifact plan requires `artifact.manage` plus the dependency reads.
Apply requires `artifact.create` for a new resource or `artifact.manage` for an
existing one, together with `service.consume` and `bucket.use` for every
selected dependency. Use the human-readable denial or `required_permissions`
in JSON plan output to identify the exact resource. An authorised administrator
can use `team access service ... use`, `team access bucket ... use`, or `team
access artifact ... manage` as appropriate. Do not execute those grants
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
