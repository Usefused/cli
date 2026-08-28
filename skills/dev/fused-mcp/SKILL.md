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
description: >-
  Help support teams find customer context, manage issues, and coordinate
  follow-up work through the connected services.
bucket: default
services:
  <service-slug>:
    version: "v1"
    operations: ["getIssue", "createIssue"]   # or select_all: true
    auth:                                     # see fused-config
      type: "oauth"
      name: "<target-auth-name>"
      ref: "${bucket.auth.<source-service>.<source-auth-name>}"
    connect: { scopes: ["read:jira-work"] }   # see fused-config
    injections:                               # optional dynamic variable injection
      - location: body
        name: from
        value: ${bucket.env.FROM_EMAIL}       # Supports ${bucket.env.*}, ${bucket.values.*} (identical alias), and ${bucket.secrets.*}
```

Author the top-level `description` with the LLM before planning. Summarize the
user-facing work this MCP server enables in one to three concise sentences,
using the business goal and selected services as evidence. Make the concrete
capabilities obvious enough that an MCP host can choose this server before it
lists tools. Do not include operation IDs, schema fields, `search_docs`,
`execute`, setup instructions, credentials, or claims beyond the selected
services. Pass the same prose to `mcp init --description` when scaffolding; it
is immutable version metadata and is returned as MCP `serverInfo.description`.
Every runnable MCP version requires complete authored server metadata; never
substitute generic compatibility prose when it is absent.

Keep OAuth/OIDC selection to the target `auth.type`/`auth.name`, an optional
complete-pair `auth.ref`, and sibling service-specific `connect.scopes`. The ref
resolves the source service/auth name in this MCP server's selected bucket; the
source need not be selected by the MCP server, but it must be an enabled
workspace service with that named pair stored in the bucket. MCP readiness,
consent, callback exchange, execution, and managed refresh use the same Engine
application-credential resolver as SDKs; MCP hosting does not add another
credential store or decision path.

For standalone CLI consent, pass the complete reference explicitly with
`workspace service connect --auth-ref`. That command has no `--mcp` selector
and does not load an MCP app to infer credential routing.

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

Every physical operation returned by `search_docs` may include a `pagination`
object scoped only to the operation ID in that same result. A ranked query result
for an operation that may support Engine pagination reports `supported: true`
and `exact_lookup_required: true`. Its `usage` offers only an exact `operationId`
lookup for full guidance or the safe two-argument `call(operationId, params)`;
it does not mention or authorize a numeric bound or third argument. The ranked
object also omits `caller_bound_supported` and `engine_max_pages`. Resolve exact
detail before adding a physical pagination option, but not solely to make the
safe two-argument call. Exact operation detail exposes `supported`,
`caller_bound_supported`, optional `engine_max_pages`, and the authoritative
`usage` instruction. A ranked query result may instead establish
`supported: false` and give the exact two-argument form without another lookup
solely for pagination, but still omits `caller_bound_supported`. Exact mode alone
exposes `caller_bound_supported` and `engine_max_pages`. Evaluate pagination
separately for every physical call.
Never reuse pagination support or a page bound from another operation, even one
on the same service or resource: guidance for `gmail.users.messages.list`
cannot authorize a third argument for `gmail.users.messages.get`. HTTP method
and provider parameter names never establish an Engine pagination policy.
Section-only retrieval is a continuation of the earlier detail lookup, not a
replacement for reading this operation guidance.

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
Physical-target pagination inside a Unified operation must stay target-keyed in
that documented Unified `pagination` parameter. Never move it into the separate
third argument used only by direct physical calls.
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
its description or operation/auth/injection scope returns
`app_version_immutable`; publish a new version. The runtime URL contains the exact opaque `app_id`, which is the
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
fused-cli mcp init <name> --description '<LLM-authored capability summary>' [--service '<service>=<version>'] [--operation '<service>=<operationId>']
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

Non-loopback transport discovery always returns HTTPS. Plain HTTP is reserved
for explicit localhost or loopback development origins; clients must not rely
on redirects preserving the execution token.

POST a JSON-RPC `initialize` request with `Authorization: Bearer <token>`.
Engine returns `Mcp-Session-Id` and the negotiated `MCP-Protocol-Version`.
Before `tools/list` or any tool call, POST a JSON-RPC
`notifications/initialized` notification with both returned headers; Engine
acknowledges it with HTTP 202 and no JSON-RPC response body. Send both headers
on subsequent POST, GET, and DELETE requests. The MCP client owns that
transport identity: never expose it to the model, ask the model to invent it,
or add it to `execute` arguments.
The `execute` tool description and `com.usefused/session` tool metadata tell
capable hosts that `session.get`, `session.set`, and `session.page` are already
attached inside each script and share state only across execute calls on the
same connection. A reinitialized connection has fresh state and cannot read
prior result references. DELETE terminates only that session. Revoke access with
`fused-cli mcp token revoke`; do not treat session deletion as token revocation.

An unavailable Streamable session returns HTTP 404 with a compact recovery
contract: `recovery_action: reinitialize_connection`,
`execute_request: reformat_if_session_state_used`,
`provider_execution: not_started`, and `automatic_replay: false`. The client
must initialize a new connection. An `execute` script independent of prior
session state can then keep its arguments; a script using `session.get`,
`session.page`, or a prior `result_ref` must be rebuilt because those values do
not cross the reset. `MCP_EXECUTION_OUTCOME_UNKNOWN` instead returns
`execute_request: do_not_replay` and `provider_execution: unknown`; inspect
external state before deciding whether to issue new work. Recovery fields never
contain the opaque session ID or bearer token, and detailed transport phase,
delivery, and side-effect classifications remain internal OTEL attributes.
When `execute_request` is `correct_arguments`, reformat the current `execute`
arguments; `provider_execution: not_started` proves that correction cannot
duplicate provider work. `adjust_projection` means the provider execution is
complete and only the session-local result shape or byte projection should
change. `use_next_request` means run the supplied request verbatim. These
closed actions replace inference from prose.
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

Successful `execute` values up to 16 KiB of UTF-8 JSON remain inline by default.
The optional `execute` argument `outputBudgetBytes` accepts integers from
1024 to 65536; retain it on continuation calls. This is a byte budget, not an
exact token count or a model-context guarantee. Larger
admitted values (up to the existing 1 MiB ceiling) are automatically retained
as JSON snapshots in the same session. The small `MCP_RESULT_STORED` envelope
contains `result_ref`, byte size, expiry, and an explicitly incomplete
structural preview with field names, types, and collection counts. It also
contains `recovery_action: continue_stored_result`,
`execute_request: use_next_request`, `provider_execution: complete`, and
`automatic_replay: false`, together with an exact `next_request` for a
session-only `execute` retrieval. `complete: false` describes visible preview
delivery, not provider pagination. Run that request directly instead of
reconstructing a session call or repeating the provider operation. Every
incomplete retained page supplies its next exact request with the same selector,
cursor, and byte budget. Previews
never sample scalar values. `collections` also advertises exact RFC 6901 array
paths, counts, observed immediate field names, and `fields_complete`.
`collections_complete` describes traversal completeness. The final model-facing
JSON ceiling is 64 KiB. For lists, choose known needed fields in the first
execution when possible. On overflow, use the existing `execute` tool:

```typescript
return session.page("<result_ref from the envelope>", {
  path: "/transactions",
  fields: ["date", "merchant", "amount", "currency"],
  offset: 0
});
```

Paging returns maximal contiguous whole-row prefixes within the same invocation
budget, including serialized metadata, escaping, and UTF-8. Read `items`,
`offset`, `total`, `returned`, `nextOffset`, and `complete`; continue at the exact
`nextOffset` with unchanged path/fields. `complete` means no rows remain after
this range, not that earlier pages were included. Return the page directly;
combining pages can overflow again. Omit path or use an empty string for a root
array. Fields are literal immediate keys, never dotted selectors; omit them
for whole or mixed-type rows. Sparse missing keys remain absent, and a field
that exists in no row is rejected. Empty collections return a complete empty
page. An individually oversized row fails with `MCP_RESULT_ROW_TOO_LARGE`;
narrow fields or use the supplied bounded inspection result to construct a
session-only property, key, or string-slice projection. These failures use
`recovery_action: adjust_result_projection`; never retry the provider
operation. Very long paths may require a larger byte budget
for metadata. For totals or analysis, compute inside `execute` and return the
answer instead of transferring every row.

Discovery inspects at most eight collections, 256 nodes, eight levels, 32
children per node, 512 rows per collection, and 32 field names (128 UTF-8 bytes
each). Output budgets may trim metadata further. Incomplete flags never imply
missing data: select advertised fields immediately, and use
`Object.keys(session.get(result_ref))` to inspect additional keys only when
needed. `session.get` accepts exactly one string key; extra arguments fail with
`MCP_SESSION_GET_ARGUMENTS_INVALID` instead of being ignored. Large custom
retrievals still produce a bounded envelope. Do not repeat `call()` to inspect
an already executed operation. Automatic retention is limited to 16 snapshots
and 4 MiB per session, with oldest-first eviction and an absolute five-minute
TTL that reads do not extend. Session closure releases all snapshots. A new
session cannot retrieve an old reference. `MCP_RESULT_UNAVAILABLE` means the
snapshot cannot be recovered here; report that limitation and make a deliberate
re-execution decision, especially for operations with side effects. Never
automatically retry. Error text is capped by the smaller of 8 KiB and the
invocation budget. Oversized error text returns
`MCP_EXECUTE_ERROR_OUTPUT_LIMIT_EXCEEDED` rather than being stored.

Agent-triggered result delivery and retrieval emit only fixed delivery state,
transport, actor type, bounded read/miss counts, and effective byte budget through
Engine OTEL. `session.page` counts as a retained read once it attempts the
snapshot lookup, including subsequent projection or size failures. Do not
log references, field names, previews, result bodies, or scripts, and do not
create another provider-execution receipt for a session-only read.

Pagination is derived from the selected endpoint and its current effective
service-version policy. Do not add pagination fields to MCP config or physical
provider schemas. Ranked `supported: true` guidance safely authorizes an
ordinary `call(operationId, params)`, which completes the reviewed provider
pagination loop inside Engine and returns one aggregate; MCP result retention
and `session.get`/`session.page` happen only afterward. Provider page-size
parameters such as Gmail `maxResults` do not limit total Engine traversal. Only
when exact detail for that same `operationId` reports
`caller_bound_supported: true` and the goal intentionally needs the first N
provider pages may you use the canonical caller bound as a separate third
argument with a positive N strictly lower than `engine_max_pages`:

```typescript
await call(operationId, params, { pagination: { maxPages: N } })
```

This option can only tighten the reviewed Engine policy. It never belongs in
provider params or bypasses Engine pagination. Never derive a numeric bound or
third argument from a ranked query result, including one with `supported: true`;
the exact lookup is the only authority for those fields. A physical operation
whose available guidance reports `supported: false`, or whose exact detail
reports `caller_bound_supported: false`, must use the two-argument form
`call(operationId, params)`; do not copy a third-argument bound from a different
operation. When `supported` is false, Engine makes one provider request for that
call and does not traverse provider pages. For a paged GET, repeat two-argument
calls only when the operation documentation explicitly supplies the page input,
continuation output, and stop condition. Pass the documented page input in
`params`, await the call, read the continuation from its result, and repeat until
that stop condition. Never infer page, cursor, offset, or termination semantics
from field names. Every manual page consumes the execute call and deadline
budgets. A supported one-page policy has `caller_bound_supported: false`,
because no positive lower bound exists. Unified calls retain their target-keyed
`pagination` inside the documented Unified invocation object.

A physical pagination-intent rejection is a pre-provider argument correction
only when no earlier or concurrent call in the same `execute` may have
dispatched. In that isolated case, invalid `maxPages`, unsupported pagination,
or a non-lowering bound returns `execute_request: correct_arguments` with
`provider_execution: not_started`. If an earlier or concurrent call may have
dispatched, the outer `execute` must instead return
`execute_request: do_not_replay` with `provider_execution: unknown`, because the
script may already have provider side effects. Inspect external state before
issuing new work. Invalid target-keyed physical pagination on a Unified call
uses the same rule: it is a pre-provider correction only when isolated.

If automatic traversal reaches an Engine pagination limit before provider
termination, narrow the provider query or deliberately choose a smaller caller
bound. Do not hide continuation fields through a provider partial-response
selector merely to make the Engine mistake an unfinished collection for a
complete one.

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
