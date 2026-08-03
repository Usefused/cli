---
name: fused-cli
description: "Use this skill when the user wants to set up or use fused-cli itself, or starts with a business goal and wants Fused to produce a ready SDK or MCP server: installing it, authenticating against a Fused Engine, discovering Registry services when the slug is unknown, understanding global flags, managing workspace teams or people, assigning RBAC or workspace-wide resource access, issuing personal credentials, selecting an owner team, importing API specs or docs URLs, or choosing a domain skill. Also use it when no Engine is running yet. Trigger on 'fused-cli', 'Fused CLI setup', 'build an SDK', 'create an MCP', 'integration workflow', 'find a service', 'engine-url', 'api-key', 'team access', 'workspace access', 'workspace role', 'add user', 'personal credential', 'owner-team', 'required permissions', 'import docs', 'start the engine', 'FUSED_LICENSE_KEY', or connection failures. Read the relevant domain skill once the task concerns a specific config shape."
---

# fused-cli

`fused-cli` is the config-as-code interface to a Fused Engine: YAML/JSON files
under `.fused/` (or `-f <path>`) declare desired state, `plan` previews the
diff against the Engine, `apply` pushes it.

## Standing up an Engine (before you have one to connect to)

Everything below this section assumes an Engine is already running somewhere
reachable. If `fused-cli` is failing with a connection error, or the user
hasn't stood up an Engine yet at all, that's a different problem -- here's
how to actually start one.

`FUSED_LICENSE_KEY` is issued by Fused -- get it from the dashboard/account
team at [usefused.com](https://usefused.com) if you don't have one.

```shell
# Native binary
curl -sL https://github.com/Usefused/engine/releases/latest/download/fused-engine_$(uname -s)_$(uname -m).tar.gz | tar -xz
mv fused-engine /usr/local/bin/

export FUSED_LICENSE_KEY="<license key from the Fused dashboard>"
fused-engine start
```

Or via Docker (`fused-alpine` variant, headless -- no bundled UI):

```shell
docker pull ghcr.io/usefused/engine:latest
docker run -e FUSED_LICENSE_KEY="<license key>" -p 8081:8081 -p 50051:50051 ghcr.io/usefused/engine:latest
```

Defaults: HTTP API + dashboard on `:8081`, SDK gRPC on `:50051`. Override via
`--port`/`--grpc-port`/`--webhook-port` flags, environment variables, or
`engine.yaml` (`--config` flag, default path `engine.yaml`) -- flags win over
env vars win over the config file. The Engine will refuse to boot without a
valid `FUSED_LICENSE_KEY`.

Once it's up, point `fused-cli` at it (see "First-time setup" below), and
check `curl http://<engine-host>:8081/health` if you need to confirm it's
actually reachable.

## First-time setup

Every command that talks to an Engine needs two things. The Engine URL resolves
as flag -> environment -> local config -> error. The credential resolves as
flag -> `FUSED_API_KEY` -> `FUSED_LICENSE_KEY` -> local config -> error.

- Engine URL: `--engine-url` flag, `FUSED_ENGINE_URL` env var, or
  `fused-cli config set engine-url <url>`
- Credential: `--key` flag, `FUSED_API_KEY` personal/service credential,
  `FUSED_LICENSE_KEY` bootstrap Owner credential, or `fused-cli config set
  api-key <key>`. `FUSED_API_KEY` deliberately wins when both variables exist.

```shell
fused-cli config set engine-url https://engine.example.com
fused-cli config set api-key <key>
fused-cli config get engine-url
fused-cli config list
fused-cli config reset      # deletes the local config file entirely
```

The local config file only stores `engine-url` and `api-key` -- no other key
is valid.

## Global flags

- `--key` / `--engine-url` -- override config/env for one invocation
- `-f, --file <path>` -- point at a specific config file, disabling `.fused/`
  directory discovery
- `--no-input` -- fail with remediation rather than opening a prompt;
  `CI=true` enables this automatically
- `--timeout <duration>` -- bound Engine requests (default `30s`)
- `--request-id <id>` -- attach a non-secret audit correlation ID to every
  Engine request
- `--readme` -- print the full CLI reference and exit
- `--version`

SIGINT/SIGTERM cancel outstanding Engine requests. `CI=true` also disables the
release update check; set `FUSED_NO_UPDATE_CHECK=1` when only the update check
should be disabled. In non-interactive mode, replace prompt-oriented options
with explicit inputs (for example, use `import docs --select METHOD:/path`
instead of `--review`).

## Command surface drifts faster than these skills -- verify with `--help`

Every command list in this skill and its seven domain skills
(`fused-workspace`, `fused-sdk`, `fused-mcp`, `fused-webhook`, `fused-bucket`,
`fused-config`, `fused-notifications`) reflects the subcommands/flags that existed when that file was last
updated -- not a live source of truth. Before running any subcommand you
haven't just confirmed, run `fused-cli <command> --help` (e.g. `fused-cli
workspace service --help`, `fused-cli sdk token --help`) to see its actual
current flags and any subcommands these files don't list yet. `fused-cli
--readme` dumps the full reference for every command at once, useful when
you need the whole surface rather than one subcommand. Treat a command list
in any of these skills as a starting point for what's likely available, not
the final word on exact syntax.

## Build an SDK or MCP from a business goal

When the user describes what they want to accomplish rather than supplying an
existing config, read [reference/build-sdk-or-mcp.md](reference/build-sdk-or-mcp.md)
and run its workspace-first discovery workflow. It covers setup, reuse of
enabled workspace services, Registry fallback, workspace activation, operation
selection, credentials, config authoring, and the final ready-to-use handoff.
Do not guess service slugs or operation IDs.

## Permissions and team access

Use `team` and `user` for RBAC, and `workspace access` when a specific bucket
or SDK/MCP permission scope should be usable by everyone in the local workspace.
The access commands still call that shared Engine resource `artifact`; this is
an internal RBAC label, not a claim that MCP produces a downloadable artifact. These are
immediate access-management commands, not config-file fields. Read
[reference/access-management.md](reference/access-management.md) before
changing membership, roles, resource access, personal credentials, or resource
ownership. Prefer human names/slugs for normal commands, and use the displayed
full UUID when the same `name@version` exists as both an SDK and MCP server. Use `team
eligible-owners` before planning a new SDK, MCP server, or webhook.

Permission-sensitive CLI flows must follow the denial protocol in that
reference: stop the blocked action, preserve drafts and plans, tell the user
the missing permission and resource, and name the narrowest relevant access
command for an authorised administrator. Never self-grant, switch credentials,
broaden a role, or retry with guessed authority. Reading access state requires
`access.read`; changing it requires `access.manage`, and assigning Owner also
requires `account.manage`.

## Every config file shares this shape

```yaml
apiVersion: fused/v1
kind: workspace   # or: sdk, mcp, webhook
```

`fused-cli plan` / `fused-cli apply` (no subcommand) operate across every
kind found under the target directory at once. `<kind> plan|apply` scopes to
one kind.

## How plan/apply staleness is caught

`plan` prints the Engine's complete plan summary and, with `--json`, includes
that summary and any notifications alongside the receipt fields. It hashes
the config file's content locally and sends that hash (not just a plan ID) to
the Engine, then writes a local receipt at
`.fused/.state/<config-key>.plan.json` (`config_key`, `plan_id`, that same
`source_hash`, and the normalized `engine_url`).

`apply` with no `--plan-id` preflights every selected config before applying
the first one. It rejects a receipt when its config hash changed, when it has
no `engine_url`, or when it targets a different Engine. Re-run `plan` against
the intended Engine to replace an invalid receipt; there is no legacy bypass
for an unbound or cross-Engine receipt. The all-config preflight prevents a
bad later receipt from being discovered only after an earlier config was
already applied. Passing an explicit `--plan-id` uses the current config hash
and active Engine directly, so use it only when the plan ID was captured from
that exact config and target. Each successfully applied resource also emits a
secret-safe OTEL audit event; a partial multi-config apply therefore retains
evidence for the resources that changed before a later failure. CLI-managed
config and receipt writes use validated same-directory atomic replacement and
preserve an existing file's permission mode.

## Importing a provider API

Registry search and import are separate permissions: `service search --q`
requires `catalogue.read`; creating or updating a service with `import plan`,
`import apply`, or `import docs` requires `catalogue.import`; reading an import
session requires `catalogue.read`. A visible Registry result does not grant
`service.manage`, `service.consume`, or workspace activation. If import is
denied, preserve the source and plan, report the missing permission/resource,
and follow [reference/access-management.md](reference/access-management.md);
do not try another credential or grant access automatically.

Use `import plan` / `import apply` when the source is already a machine-readable
specification. This path is reviewed and receipt-backed: `plan` parses/diffs,
then `apply` commits the exact planned source.

```shell
fused-cli import plan ./openapi.yaml --name "Billing API" --slug billing-api
fused-cli import apply

fused-cli import plan --url https://developer.example.com/asyncapi.yaml \
  --name "Events API" --slug events-api
fused-cli import apply
```

Supported spec inputs are OpenAPI, AsyncAPI, Postman Collection, WSDL, GraphQL
SDL, and introspectable GraphQL endpoints. `--url` is still a spec URL here:
Registry first tries a bounded `GET`, then GraphQL introspection if the GET
response is not recognized as a spec. A normal HTML docs page belongs to
`import docs`, not `import plan --url`.

Use `import docs` when the source is a human-readable docs page and the agent
must discover endpoint candidates. By default it selects every discovered
endpoint, then extracts schemas and commits the service. Selection is an
advanced correction/filter:

```shell
# Default: import every discovered endpoint.
fused-cli import docs --url https://docs.example.com/api \
  --name "Docs API" --slug docs-api --version 1.0

# Review interactively; all endpoints start selected.
fused-cli import docs --url https://docs.example.com/api \
  --name "Docs API" --slug docs-api --version 1.0 --review

# CI/partial mode. Omitted endpoints are not treated as deletions.
fused-cli import docs --url https://docs.example.com/api \
  --name "Docs API" --slug docs-api --version 1.0 --select GET:/users
```

Why the default is all endpoints: docs extraction is meant to produce a coherent
service contract. Partial selections are useful when the extractor found noise
or the team intentionally wants a small slice, but omitted endpoints should not
be interpreted as evidence that the provider removed them. `import docs` adds
the completed service to the current workspace unless `--no-workspace-add` is
passed.

## End-to-end: wiring up an OAuth service from zero

Enable the service first:

```yaml
# .fused/workspace.yaml
apiVersion: fused/v1
kind: workspace
services:
  jira:
    versions:
      - version: "2026-07-01"
```

```shell
fused-cli workspace plan
fused-cli workspace apply
```

Then register the OAuth app under the bucket that will hold the resulting
user tokens -- `auth`/`connect` live under `buckets.<bucket>.service_config.
<slug>`, not under the service itself (see `fused-bucket`). Registering the
app is a separate, immediate admin action, not a workspace.yaml field:

```shell
printf '%s' 'client_id=...;client_secret=...;redirect_uri=https://engine.example.com/workspace/connect/callback' | \
  fused-cli connect set jira --bucket company-credentials --value-stdin
```

(Omit the value and add `-i` to be prompted per field instead, or see
`fused-bucket` for how to rotate just one field later without resupplying
the others.) This is the only way to register the app -- there is no
workspace.yaml `connect:` field; it was removed in favor of this single
imperative command, since two ways to declare the same registration (one
requiring every field on every apply, one supporting partial updates) was
duplicated decision-making with no upside.

```shell
fused-cli connect get jira --bucket company-credentials
```

Checks what's actually registered (`auth_type`/`enabled`/`redirect_uri` plus
`has_client_id`/`has_client_secret` -- never the decrypted values) -- useful
since neither `workspace.yaml` nor `workspace sync` reflect this at all, not
even read-only.

The `profile` block is omitted above because Registry has exactly one public
match for this version/auth_type -- see `fused-config` for when you'd need to
add one explicitly.

Start one user's OAuth session (see `fused-bucket`):

```shell
fused-cli workspace service connect jira --bucket company-credentials \
  --user-ref user_123 --scope read:jira-work --scope write:jira-work --scope offline_access
```

If that user has more than one Jira site, confirm/set which one is default
(see `fused-bucket`):

```shell
fused-cli connection resources list <connection-id>
fused-cli connection resources set-default <connection-id> <resource-id>
```

Generate an SDK against that same bucket (see `fused-sdk`):

```yaml
# .fused/sdks/jira-sdk.yaml
apiVersion: fused/v1
kind: sdk
name: jira-sdk
version: "1.0.0"
language: typescript
bucket: customer-accounts
services:
  jira:
    version: "2026-07-01"
    operations: [getIssue, createIssue]
    auth:    { type: "oauth" }
    connect: { scopes: ["read:jira-work", "write:jira-work"] }
```

```shell
fused-cli sdk plan
fused-cli sdk apply --download
```

Call it -- only Fused selectors ever cross the wire, never a raw provider
token or site URL:

```ts
const resources = await sdk.auth.listConnectionResources({ connectionId });
await sdk.Jira.issues.getIssue({
  issueIdOrKey: "OPS-1",
  fused: { endUserRef: "user_123", authType: "oauth", resourceId: resources[0].id },
});
```

Changing the file to `kind: mcp`, then running `fused-cli mcp plan` and
`fused-cli mcp apply` (see `fused-mcp`), deploys an Engine-hosted MCP server
against the same bucket rather than generating a package. The
workspace/bucket/connect steps above do not change.

## Which skill to read next

| Skill | Covers |
|---|---|
| `fused-workspace` | The service allowlist: enabling services/versions, execution policy, deprecations |
| `fused-sdk` | Generating a typed SDK package from selected operations/webhooks |
| `fused-mcp` | Generating an Engine-hosted MCP server from selected operations (MCP cannot select webhooks) |
| `fused-webhook` | Registering inbound webhook ingress (`kind: webhook`) and attaching it to an SDK via `webhook_attachment` so that SDK receives delivery |
| `fused-bucket` | Credential containers: secrets, static values, registering a service's OAuth/OIDC app, starting an OAuth connect session, managing a connected user's resources |
| `fused-config` | Cross-cutting config owned by no single concept above: execution policy (rate limits/retries/pagination/outbound webhook verification, local-workspace-effect vs. Registry-publish), connection profiles (auth + dynamic request routing), and the OpenAPI/Postman `x-fused-connect` equivalent |
| `fused-notifications` | Reading, not authoring: what a `plan`/`apply` notification block means, `registry_*` vs. `workspace_*` types, severity, and how/where one gets marked read or dismissed (UI only, not `fused-cli`) |

Read only the skill(s) relevant to the task at hand -- don't load all seven
for a single-domain question.
