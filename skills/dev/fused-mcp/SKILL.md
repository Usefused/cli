---
name: fused-mcp
description: "Use when the user wants to deploy or manage an Engine-hosted MCP server using fused-cli mcp and a kind: mcp config. Trigger on 'MCP server', 'fused-cli mcp', or 'kind: mcp' files. An MCP service cannot select webhooks; for generated SDK packages read fused-sdk instead."
---

# MCP server config

If the user starts with a business goal and no existing config, first follow
the `fused-cli` skill's `reference/build-sdk-or-mcp.md` workflow. Use this skill
for the MCP-specific config and lifecycle once Engine setup, service discovery,
workspace activation, and credential requirements are understood.

`kind: mcp`, managed by `fused-cli mcp ...`, declares an Engine-hosted runtime.
It never builds, archives, or downloads a code package. Its config shares service
selection fields with SDK configs so validation remains consistent where the
runtime capabilities overlap.
The shared Engine app-selection projection is exactly `schema_version: 3`.
That is response metadata, not an `mcp.yaml` field. Do not substitute another
field name or interpret a missing, older, or future version.

Run `bucket list`, treat its results as visible through `bucket.read` rather
than proven usable, and choose visible `default` or another visible candidate.
Let MCP plan/apply check `bucket.use` on that exact candidate. On denial, stop
and report it; never create a fallback. Follow `fused-bucket` for the narrow
conditions that permit creation.

```yaml
apiVersion: fused/v1
kind: mcp
name: customer-support
version: "1.0.0"
bucket: default
services:
  <service-slug>:
    version: "v1"
    operations: ["getIssue", "createIssue"]   # or select_all: true
    auth:    { type: "oauth" }                # see fused-config
    connect: { scopes: ["read:jira-work"] }   # see fused-config
    injections:                               # optional dynamic variable injection
      - location: body
        name: from
        value: ${bucket.env.FROM_EMAIL}       # Supports ${bucket.env.*}, ${bucket.values.*} (identical alias), and ${bucket.secrets.*}
```

`injections[].value` tags always resolve against *this MCP server's* `bucket:` -- there's no way to name a different bucket here, unlike `kind: webhook`'s `${bucket.<name>.secret.<key>}` (see `fused-webhook`). A value can also merge a tag with surrounding text (e.g. `"Bearer ${bucket.secrets.API_KEY}"`), which a webhook's secret field cannot. Writing a webhook-style named-bucket reference here (e.g. `${bucket.prod.secrets.API_KEY}`) is rejected at dispatch time with an explicit error naming the unsupported reference, rather than one of the three ambient forms above.

`select_all: true` is the alternative to listing `operations` explicitly --
exactly one of the two is required (see `fused-sdk`; the validation and
sync-freezing behavior described there is the same struct shared with SDK).
A service's `webhooks`/`webhooks_select_all` are rejected outright on an MCP
config (both CLI-side and Engine-side) -- this predates `webhook_attachment`
and hasn't been revisited since (see the doc comment on
`validateAppServices` in `cli/internal/configfile/parser.go` if you need
to check whether that's changed). Setting top-level `webhook_attachment`
alone, with no service selecting webhooks, is accepted but has no effect.
Treat webhook delivery as an SDK-only surface today (see `fused-webhook`).

## Identity, versions, and authentication

One MCP name has one stable MCP ID shared by all its versions. Each explicit
`version` also has an immutable Version ID. Multiple versions may run together
and there is no implicit latest/default. Name punctuation, including colons,
is part of canonical identity, so never recover a name by splitting a config
key.

Applying identical canonical content to the same version is a no-op. Changing
its operation/auth/injection scope returns `app_version_immutable`; publish a
new version. The runtime URL contains the exact opaque `app_id`, which is the
authoritative version identity.

MCP execution tokens are shared across versions. The token emitted once by the
first apply allows every selected operation and does not expire, preserving the
simple default. For an agent, generate a stricter MCP token with an exact
operation allowlist and, when appropriate, a short lifetime. Anything absent
from `--allow` is denied. A token can only narrow a version's own operation
scope; it cannot grant an operation the MCP version does not expose.

Choose the token's connected-user binding at issuance. A dynamic token accepts
Fused selectors from an MCP client that can set connection headers. A fixed
token resolves each declared service/auth/user/resource tuple against the MCP
server's bucket at issuance and cannot be overridden by caller headers. Use a
fixed token for clients that accept only `Authorization`, or whenever the token
must be limited to one customer's connected account. Different services on one
token may intentionally use different end-user references.

The MCP token only authenticates the client to Engine. Provider credentials
remain in the MCP's selected Engine bucket and are never sent by the MCP
client. Engine-to-Registry authentication uses the License Key and is unrelated
to both. Registry stores no MCP runtime/config/package data.

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli mcp <subcommand> --help` to confirm before relying on one. The
`fused-cli` skill documents init's batched server-variable enrichment.

```shell
fused-cli mcp init <name> [--service '<service>=<version>'] [--operation '<service>=<operationId>']
fused-cli mcp init <name> --extend --service '<service>=<version>' --select-all '<service>'
fused-cli mcp plan
fused-cli mcp apply
fused-cli mcp validate
fused-cli mcp list
fused-cli mcp deactivate <mcp-name@version-or-version-id>
fused-cli mcp token generate <mcp-name-or-id> <token-name> --allow <operation-id> --expires-in 15m
fused-cli mcp token generate <mcp-name-or-id> <token-name> --expires-in 1h \
  --fixed-binding '<service-slug>,<auth-name>,<end-user-ref>[,<resource-uuid>]'
fused-cli mcp token generate <mcp-name-or-id> <token-name> \
  --fixed-binding 'jira,JiraOAuth,jira-customer-a' \
  --fixed-binding '@google/gmail,GoogleOAuth,google-customer-a'
fused-cli mcp token list <mcp-name-or-id>
fused-cli mcp token revoke <mcp-name-or-id> <token-name>
```

Use `init` to create `.fused/mcps/<name>.yaml` without overwriting an existing
file. Repeat it with `--extend` to add selections; the result is `extended` or
idempotent `unchanged`, and conflicts stop before writing. An empty skeleton
is intentionally incomplete until each service lists operations or uses
`--select-all`. `--bucket` references an existing usable bucket and never
creates one.

`mcp apply` doesn't just validate config -- it stands up (or updates) a
persistent, named Engine-hosted server with its own URL, which stays live
until explicitly deactivated. `mcp list` shows each server's name, version, ID,
active state, recommended Streamable HTTP URL, and legacy SSE URL. Give new
clients the Streamable HTTP URL. Use the SSE URL only when a client explicitly
requires the older transport (see below).
Deactivating one is
an irreversible hard deactivation of that exact version: Engine writes a
tombstone, stops its runtime, and will not allow that MCP/version to be
recreated. Sibling versions and shared tokens remain. Deactivation is immediate and
not gated behind the same SDK-config blocker `fused-workspace` describes for
removing a workspace service. The CLI currently exposes no MCP deprecate or
undeprecate command; do not invent one.

The first successful MCP apply may return `execution_token` once. An idempotent
apply does not reveal it again. Store it immediately. After an Engine database
reset, Registry cannot restore the MCP or its token; reapply the exact config
and securely distribute the newly issued token.

`mcp token generate` also reveals the plaintext once. Omit `--expires-in` for
no expiry; omit `--allow` for the full-access `*` default. Repeat `--allow` or
pass a comma-separated list for multiple exact operation IDs. Applications
that need dynamic agent sessions can use Engine's equivalent app-token API;
the caller still needs `app.tokens.manage`. Repeat `--fixed-binding` for each
service/auth tuple that token may use; each tuple starts with the public service
slug (bare `service` or provider-qualified `@provider/service`), never its
internal UUID. Every repeat independently selects its auth name, end-user
reference, and optional resource UUID. The CLI rejects malformed optional resource UUIDs;
Engine resolves every slug and rejects duplicate or unavailable service/auth
bindings atomically. `mcp token list` includes
active/expired/revoked status, binding mode, and aggregate execution/session
counts from retained history; it never reveals token plaintext or hashes.

## Permissions and team access

A new MCP plan requires `app.create`, `service.read`, and `bucket.read`.
Planning an update requires `app.manage` plus the dependency reads. Apply
requires `app.create` for a new server or `app.manage` for an existing
one, together with `service.consume` for every selected service and `bucket.use`
for its bucket. `mcp list` requires `app.read`, removal requires
`app.manage`, and any execution-token management surface requires
`app.tokens.manage`.

For team ownership, preflight the owner and dependencies before planning:

```shell
fused-cli team eligible-owners
fused-cli team build-access <team> --resource service
fused-cli team build-access <team> --resource bucket
fused-cli mcp plan --owner-team <team>
```

An authorised administrator can grant only the missing scope:

```shell
fused-cli team access service grant <team> <service> use
fused-cli team access bucket grant <team> <bucket> use
fused-cli team access app grant <team-slug-or-id> <mcp-id> read|use|manage
```

Use workspace-wide bucket/app grants only when that audience is intended.
On denial, stop the blocked action, preserve the config and plan, and tell the
user the missing permission and resource. Never self-grant, switch credentials,
broaden scope, or retry with guessed authority. Do not run access commands
unless explicitly requested and authorised. Read the `fused-cli` skill's
`reference/access-management.md` for the full matrix.

The owning person or team retains management even when the MCP server is offered
to the whole company. `fused-cli workspace access app grant <mcp-id>`
adds bounded workspace-wide use across every MCP version
without granting token or lifecycle management;
the MCP execution token below is still required at runtime. A platform-owned
bucket can be made selectable by every eligible owning team with `workspace
access bucket grant <bucket-name>` while its secrets remain platform-managed.

## Calling the running MCP

Use the recommended Streamable HTTP URL returned by `mcp apply` or `mcp list`:

```text
https://<engine-host>/mcp/<version-id>
```

POST a JSON-RPC `initialize` request with `Authorization: Bearer <token>`.
Engine returns `Mcp-Session-Id`; send that opaque header on subsequent POST,
GET, and DELETE requests. Send the negotiated `MCP-Protocol-Version` when the
client supports it. DELETE terminates only that session. Revoke access with
`fused-cli mcp token revoke`; do not treat session deletion as token revocation.
The same output also exposes the legacy SSE URL:

```text
https://<engine-host>/mcp/<version-id>/sse
```

It addresses the same immutable MCP version and uses the same execution token,
but it is transitional compatibility and must not be recommended for new
clients. Human output labels Streamable HTTP as recommended and SSE as legacy;
JSON output uses `default_transport` and the typed `transport_urls` object.

Pagination is inherited automatically from the selected endpoint and its
effective service-version policy. Do not add pagination fields to MCP config or
tool schemas. Engine performs the provider requests and streams each successful
page as a separate execution chunk.

Neither the tool schema nor its `call()` function accepts provider tokens,
API keys, auth scheme names, Fused user selectors, or server-routing variables
as parameters. A `server_variable` injection resolves the effective base URL
inside Engine; only imported endpoint path/query/header/cookie/body inputs
belong in the tool schema. For a
dynamic token, configure selectors once on the MCP connection rather than on
each tool call:

```text
Authorization: Bearer <MCP execution token>
X-Fused-End-User-Ref: user_123
X-Fused-Resource-ID: <optional Fused connection-resource UUID>
```

For a fixed token, omit both selector headers. Engine uses only the opaque
connection/resource IDs resolved when the token was issued. Do not add a second
temporary-token format or an MCP-client OAuth flow: SDK and MCP deliberately use
the same execution-token contract, while provider OAuth remains inside
the Engine bucket.

Provider arguments come from the imported canonical request schema. Map-valued
objects retain their `additional_properties` value schema, so validate each map
entry rather than treating the object as untyped. Pass resource-name path values
normally: when an imported contract marks slash-preserving expansion, Engine
preserves embedded `/` separators and still escapes unsafe segment characters.
Do not pre-encode the value in the MCP call.

`X-Fused-End-User-Ref` is required only for connected OAuth/OIDC calls.
`X-Fused-Resource-ID` is required when that connection has multiple
provider sites/shops/portals/accounts and no default is set (see
`fused-bucket` for setting a default resource). Engine middleware resolves
the bucket credential and applies connection-resource bindings before
dispatch -- the agent driving the MCP only ever picks operations and
provider arguments.
