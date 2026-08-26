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
A top-level `unified_operations` map uses the same graph, mapping, dependency,
rollback, and output authoring contract as an SDK; read
`fused-unified-operations`. MCP sets no `language`, so plan skips only generated
TypeScript/Python symbol checks, then runs the same Engine compiler after
physical selections are pinned. Apply persists the same immutable private v3
definitions and hashes, while the credential-free descriptor remains in the
applied plan. Do not create an MCP-specific graph shape or authorization scope.
At session start, Engine adds each fully token-authorized descriptor to the
existing `search_docs` catalogue under its exact authored name. The server still
exposes only `search_docs` and `execute`; an exact physical/Unified name collision
fails closed. Discovery returns public schemas and graph names only, never
private mappings, internal UUIDs, selectors, or values.

Treat a non-empty `search_docs` query as a concise capability intent, such as
`send email attachment`, rather than forwarding the conversation. Intent search
returns the three best matches by default and accepts at most five, including
enough callable detail for an immediate `execute` when that detail fits. Prefer
a complete Unified Operation when it covers the whole goal. If no returned
operation safely supports the request, retry at most once with more specific
service and action terms; never guess an operation ID.

An empty or whitespace-only query is the browsing fallback: it returns a bounded,
schema-free catalogue with `total` and `truncated` metadata. An exact
`operationId` remains the deterministic detail lookup and takes precedence over
query text. Every `search_docs` result is bounded to 64 KiB of UTF-8 JSON. Check
`schema_status.complete`, then compare `included_sections` with
`available_sections` before writing a call: never infer fields omitted from an
outline or truncated result. When a chosen operation is incomplete, retrieve
only its missing physical `parameters`, `request`, or `response:<status>`
section, or its Unified `input`, `targets`, or `output` section. Use
`schemaPath` JSON Pointer retrieval only when a smaller nested schema is needed.
Do not pre-emptively load every response schema because the actual provider
response arrives through `execute`.

Agent-triggered discovery is auditable without exposing user content. Telemetry
may record mode, bounded catalogue counts, whether detail was requested,
response bytes, duration, outcome, and actor type, but never the raw query,
operation ID, returned schema, schema pointer, private mapping, or
credential-bearing value.

Inside an `execute` script, call a discovered Unified operation with
`await call(operationId, {input, targets, selectors?, pagination?, idempotencyKey?})`.
Use TypeScript camelCase selector and pagination fields, keep `targets`
dependency-closed, and omit `idempotencyKey` only when a new SDK-equivalent UUID
is appropriate. Engine reauthenticates the session and sends every selected
forward and active rollback through the canonical Unified coordinator; catalogue
visibility never grants execution scope.
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
`--select-all`. For a service-bearing config without `--bucket`, init lists
read-visible buckets once, writes the visible bucket named `default` or the
first visible candidate, and fails without writing when none is visible. An
explicit `--bucket` remains authoritative. Init never creates a bucket, and
plan/apply remains the authoritative `bucket.use` check.

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

Engine applies non-configurable ceilings before MCP data crosses a process or
transport boundary: 256 KiB request/call payloads, 64 KiB documentation,
512 KiB individual schemas, 1 MiB physical/final execution results, bounded schema
depth/node counts. Sessions have a five-minute maximum inactivity window, not a
fixed lifetime: accepted client traffic and pending calls keep them alive, and
completion refreshes the idle window. Server keepalives alone do not count as
activity. Token expiry/revocation, app deactivation, and independent per-call
timeouts still apply. Treat
`MCP_DOCUMENTATION_OUTPUT_LIMIT_EXCEEDED`,
`MCP_EXECUTE_RESULT_LIMIT_EXCEEDED`, `mcp_call_payload_too_large`, and
`mcp_call_result_too_large` as terminal for that invocation. Narrow
`search_docs` with a query or exact `operationId`; reduce execution input or
the returned projection before retrying. Keep the same token and MCP version;
these failures do not require republishing or creating another server.

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
