---
name: fused-sdk
description: "Build, generate, configure, manage, or call a typed Fused SDK directly inside a coding agent using fused-cli sdk or the Engine execution REST API. Use when the user gives Codex, Claude Code, Cursor, Windsurf, or Antigravity a business goal for an SDK; when editing a kind: sdk config; when constructing a physical or Unified Engine operation request; or when selecting operations, receiving webhooks, scoping auth, managing immutable versions/runtime tokens, or running SDK plan/apply/validate/download/sync commands. This is the coding-agent entry point: never invoke sdk prompt, because it starts a separate Fused agent. For Engine-hosted MCP runtime behavior read fused-mcp instead."
---

# SDK package config

## IDE-agent workflow boundary

Never run `fused-cli sdk prompt` from a coding-agent task. That user-invoked
command starts the Fused-hosted agent. Run the deterministic CLI workflow
yourself. Do not delegate the work to another agent or call an AI endpoint.

Maintain one compact working-facts record: CLI/Engine context, config paths,
SDK identity, exact service/operation selections, bucket, ownership, and open
credential or permission decisions. Reuse local facts and read only the
relevant CLI `--help` branch. Do not load every sibling skill up front:

- read `fused-workspace` only when service activation is required; read `fused-bucket` only when credentials or OAuth are unresolved;
- read `fused-config` only for auth, connect scopes, injections, or policy;
- read `fused-cli` and `reference/access-management.md` only for setup or permission remediation.

For a business goal without a complete config, follow the `fused-cli` skill's
`reference/build-sdk-or-mcp.md` SDK path, then return here after Engine setup,
service activation, and credential requirements are known. Apply or download
only when requested and no authority or production decision remains open.
Use `service operations --json` for bounded discovery, inspect body contracts only with `service operation show` opt-in flags, and run `sdk validate --json` after writing the config.
Use `--json` on every SDK command whose confirmed help exposes it. In
particular, never parse SDK/Version IDs or the one-time token from human apply
text: use `sdk plan --json`, `sdk apply --json`, `sdk token generate --json`,
`sdk download --json`, `sdk invoke --json`, and `sdk activity --json`.

`kind: sdk`, managed by `fused-cli sdk ...`, declares a typed SDK package from a bucket's already-configured services.
Run `bucket list`; its results prove `bucket.read` visibility, not use. Choose
visible `default` or another candidate and let SDK plan/apply check its exact
`bucket.use`. On denial, stop without creating a fallback. Follow
`fused-bucket` for the narrow conditions that permit creation.

Each service map key is an activated service's persisted Registry slug. Confirm
it with `fused-cli workspace services list -q <slug> --json`; Registry
visibility alone grants neither activation nor `service.consume`.
```yaml
apiVersion: fused/v1
kind: sdk
name: my-sdk
version: "1.0.0"
language: typescript
bucket: default
webhook_attachment: my-webhooks             # optional -- names a kind: webhook config, see fused-webhook
services:
  <service-slug>:
    version: "v1"
    operations: ["listUsers", "createUser"]   # or select_all: true
    webhooks: ["user.created"]                # event names, requires webhook_attachment -- or webhooks_select_all: true
    auth:    { type: "api_key" }              # see fused-config for the full auth_type reference
    connect: { scopes: ["read:users"] }       # OAuth/OIDC consent ceiling, see fused-config
    injections:                               # optional dynamic variable injection
      - location: header
        name: X-Custom-Header
        value: ${bucket.env.MY_VAR}           # Supports ${bucket.env.*}, ${bucket.values.*} (identical alias), and ${bucket.secrets.*}
```

`injections[].value` tags always resolve against *this SDK's own* `bucket:` -- there's no way to name a different bucket here, unlike `kind: webhook`'s `${bucket.<name>.secret.<key>}` (see `fused-webhook`). A value can also merge a tag with surrounding text (e.g. `"Bearer ${bucket.secrets.API_KEY}"`), which a webhook's secret field cannot. Writing a webhook-style named-bucket reference here (e.g. `${bucket.prod.secrets.API_KEY}`) is rejected at dispatch time with an explicit error naming the unsupported reference, rather than one of the three ambient forms above.

`webhooks`/`webhooks_select_all` only make sense once a `kind: webhook`
config exists and is named via top-level `webhook_attachment` -- this
registers *which events* this SDK receives, not the webhook registration
itself (that's `fused-webhook`). Setting either without `webhook_attachment`
is rejected at plan time.

`auth.type` selects a Registry-declared scheme (`basic`, `bearer`,
`api_key`, `oauth`, `oidc`, `mtls` -- the same list `fused-config` documents
for workspace `auth`); `auth.name` disambiguates two schemes of the same
type. An explicit selector must occur in a valid alternative for every secured
selected operation. Without one, Engine chooses each operation's first
provider-declared OR alternative. The immutable SDK definition records every
scheme in those chosen alternatives, and bucket readiness checks the full AND
sets from metadata without decrypting values. Anonymous-only and webhook-only
selections record no required auth and perform no credential read.
For a mix of anonymous and secured operations, any inferred common selector is
applied only to secured calls; an anonymous call stays anonymous unless it
supplies an explicit per-call auth selector; that selector may choose another
declared OR branch at runtime. `connect.scopes` narrows OAuth/OIDC consent
-- an application can request fewer scopes per user but never more than
declared here. Credential material itself never lives in this file -- it's
resolved from `bucket` at generation/dispatch time (see `fused-bucket`).

Ordinary `sdk plan` is read-only. `sdk plan --interactive` may respond only to typed `bucket_credentials_missing`: show the exact YAML-resolved bucket, securely prompt for the reported static secret or OAuth/OIDC app-registration fields, confirm the write, and retry once. Never collect an end-user provider token, create/substitute a bucket, or prompt with `--json`, `--no-input`, or `CI=true`; denied credential writes stop without fallback or self-grant (see `fused-bucket`).

The selected operation's imported `security_requirements` is authoritative:
alternatives are OR, schemes within one alternative are AND, and an empty
alternative permits anonymous execution. Do not collapse an AND alternative
to one convenient scheme or invent a scheme absent from the operation. Basic
password mode and templated server declarations are carried from Registry as
Engine routing metadata; they never become generated method options. SDK
methods expose only endpoint parameters and body fields, while SDK config still
contains neither provider credentials nor resolved tenant URLs.
Generated SDKs use only Registry schema/media projections and auth selectors; raw schemas, OAuth flow choice, credentials, signing, and challenges remain Engine-owned. Callback/link/runtime-expression and unknown-extension metadata stays inert; only normalized documentation fields may shape generated prose.
For a non-object request root, generated TypeScript and Python methods expose a
typed `payload` argument and forward that scalar or array as the declared root;
do not wrap it in a caller-authored object or spread an array into numeric
keys. Object and referenced-object inputs retain their named option fields,
while sequential-media arrays retain the existing ordered `body` option so
Engine can apply JSONL/JSON-seq framing.
Operation selection always uses the exact imported `operationId`. Generated
language members are safe identifiers: an empty or numeric-leading operation
becomes `operation...` in TypeScript and `operation_...` in Python, while the
wire request still carries the original operation ID. Use the generated member
shown by the package or service-detail example; do not rewrite the configured
operation ID to match it.

`select_all: true` is the alternative to listing `operations` explicitly --
exactly one of the two is required, never both/neither. Be aware `sdk sync`
(below) always freezes whatever's currently selected into an explicit,
sorted operation list; a service configured with `select_all: true` does
not come back as `select_all: true` after a sync, even if nothing else
about it changed.

## Unified Operations

TypeScript and Python SDK configs and `kind: mcp` configs may declare top-level
`unified_operations` over operations in the same immutable app version; Go SDKs cannot.
Read `fused-unified-operations` for graph, mapping, selector, and output rules.
A configured root `output` is the exact success value with no wrapper; only an
operation without it returns the all-settled `{results, rollbacks}` envelope.

## Identity, versions, and authentication

One SDK name has one stable SDK ID shared by all its versions. Each explicit
`version` also has an immutable Version ID. There is no implicit latest or
default. Name normalization preserves punctuation, including colons, so never
derive identity by splitting a composite config key.

Applying the same canonical content to the same version is a no-op: it does not
regenerate the package or rotate tokens. Changing services, operations, auth,
injections, language, or other scope under an existing version returns
`app_version_immutable`; publish a new version. Do not edit a generated package
to impersonate another version: Engine authorizes the embedded opaque `app_id`,
not a client-reported semantic version.

SDK execution tokens belong to the SDK, not one version. A token therefore
works with every active or deprecated version of that SDK, while each version
still enforces its own operation scope. A plan that expands capability in a new
version should report the existing tokens affected. Teams that must not share
capability should use different SDK names.

Keep these credentials distinct:

- the CLI API key authenticates management calls to Engine;
- the SDK execution token authenticates `app_id + token` runtime calls;
- provider credentials stay in the SDK's selected Engine bucket.

Never place the License Key or provider credentials in generated SDK config.
Pass the one-time SDK execution token to the generated client and store it in a
local secret manager. The first successful `sdk apply` prints that token once;
capture it immediately because an idempotent apply or later lookup will not
return it. Never log it or persist it in the SDK config, plan receipt, or CLI
state. The current CLI has no SDK deprecate/deactivate command; do not invent
one. Those lifecycle actions are available through Engine's App UI/API.

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli sdk <subcommand> --help` to confirm before relying on one. The
`fused-cli` skill documents init's batched server-variable enrichment.

```shell
fused-cli sdk init <name> [--extend] [--service '<service>=<version>'] [--operation '<service>=<operationId>']
fused-cli sdk plan [--interactive]
fused-cli sdk apply
fused-cli sdk validate
fused-cli sdk download <sdk-name@version-or-version-id>
fused-cli sdk openapi <sdk-name@version-or-version-id> [--operation <exact-operation-id>] [-o <path>] [--format yaml|json]
fused-cli sdk sync <sdk-name>
fused-cli sdk show <sdk-name@version-or-version-id>
fused-cli sdk services <sdk-name@version-or-version-id>
fused-cli sdk buckets <sdk-name-or-id>
fused-cli sdk invoke <sdk-name@version-or-version-id> <operation>
fused-cli sdk activity <sdk-name@version-or-version-id>
fused-cli sdk token generate <sdk-name-or-id> <token-name> [--expires-in 4h]
fused-cli sdk token list <sdk-name-or-id>
fused-cli sdk token revoke <sdk-name-or-id> <token-name>
fused-cli sdk service add <service-slug>
fused-cli sdk service remove <service-slug>
fused-cli sdk operation add|remove <service-slug> <operation-id...>
fused-cli sdk webhook add|remove <service-slug> <webhook-id...>
```

`sdk token` manages named, revocable API tokens for calling an already
*generated* SDK's Engine endpoint (distinct from your own `fused-cli config
set api-key`, which authenticates CLI-to-Engine management calls, not a
generated SDK's own runtime traffic) -- `generate` prints the token exactly
once, so capture it immediately. Generating, listing, or revoking by SDK name
or SDK ID affects the token set shared by every version of that SDK.
Use `--expires-in <duration>` for temporary testing access and omit it for a non-expiring service credential; it limits time, not SDK operations. Do not invent or suggest SDK `--allow` or fixed-binding flags.
For automation, run `sdk apply --json`; it returns an array of explicit apply,
generation, and download outcomes and includes `execution_token` only on the
one response where Engine created it. `--download` remains a convenience, but
`sdk download` is a visible independent command. Prefer a separate apply and
download when a pipeline needs to retry package transfer without replaying an
apply. Structured failures identify the failed stage and retain SDK/Version IDs
when apply succeeded before generation waiting or download failed.

`sdk openapi` resolves one exact Version ID with the control credential and
`app.read`, then GETs `/apps/{app_id}/openapi`; the execution token cannot authorize it.
It atomically writes YAML or JSON; `--operation` is one exact physical or Unified name.
`--json` reports metadata only: `operation_count` and `sha256:<64 lowercase hex>` of final file bytes.
Before replacement it accepts only OpenAPI 3.1.x with matching `x-fused-app-id`,
the real POST path and matching `app_id` enum, and count-consistent request branches.
That POST uses the SDK-wide execution-token Bearer scheme; no credential is embedded.
The default is `<safe-sdk-name>-<version>.openapi.yaml` (or `<app-id>.openapi.yaml`
for UUID input); `--out` changes it. Input is capped at 16 MiB and uses the configured Engine URL.

`sdk invoke` is the repeatable JSON smoke-test/debug surface. It resolves one
exact SDK version with the control credential, then calls
`POST /v1/apps/{app_id}/executions` on the globally configured Engine using only
the SDK execution token as `Authorization: Bearer`. Place that token in
`FUSED_SDK_TOKEN` by default; `--token-env` selects another variable and
`--token-stdin` avoids environment storage. Never pass the CLI key, License Key,
or a provider credential as the execution token. Pass one duplicate-free JSON
value with `--params` (physical operations require an object), a
physical selector with `--selector`, or Unified targets and service selectors
with 1–16 repeatable, explicit, unique `--target` values plus `--selectors`;
Unified execution never defaults to every declared target. Each JSON option
also accepts an `@file`. `--environment` is backward-compatible physical
`selector.environment` sugar. This REST command supports one buffered JSON
provider document up to 1 MiB and a bounded 17 MiB Unified aggregate; generated
SDKs retain their broader gRPC transports. The command produces one logical
Engine execution and does not implement local provider retries or redirects. For direct HTTP request construction, read [reference/engine-execution-api.md](reference/engine-execution-api.md).

`sdk activity` reads the canonical Engine execution receipts for one exact SDK
version. Use `--all-versions` only when SDK-wide history is intended, and
filter with `--status`, `--start`, `--end`, `--limit`, and `--offset`. Its JSON
page contains receipt and trace IDs, provider status, total/provider latency,
attempt counts, bounded timing dimensions, and failure classification. Query
this command instead of calling Engine GraphQL directly or inventing another
analytics path.

`sdk download` resolves one exact app version. Engine downloads its leased ZIP
from Registry; on a cache miss it may regenerate the same immutable package
from Engine-local build metadata and the generator version pinned when that app
version was created. It does not silently use a newer generator. Registry is
not an SDK configuration archive and cannot restore SDKs after an Engine
database reset; reapply the local SDK config and issue a new execution token.

## Permissions and team access

A new SDK plan requires `app.create`, `service.read`, and `bucket.read`.
Planning an update requires `app.manage` plus the dependency reads. Apply
requires `app.create` for a new SDK or `app.manage` for an existing
one, together with `service.consume` for every selected service and `bucket.use`
for the selected bucket. Download, OpenAPI export, invoke target resolution,
and activity require `app.read`; activity also requires `audit.read`. Runtime invocation
additionally requires a valid SDK-scoped execution token. `sdk token`
generate/list/revoke requires `app.tokens.manage`.

For team ownership, preflight the owner and every dependency before planning:

```shell
fused-cli team eligible-owners
fused-cli team build-access <team> --resource service
fused-cli team build-access <team> --resource bucket
fused-cli sdk plan --owner-team <team>
```

An authorised administrator can grant only the missing scope:

```shell
fused-cli team access service grant <team> <service> use
fused-cli team access bucket grant <team> <bucket> use
fused-cli team access app grant <team-slug-or-id> <sdk-id> read|use|manage
```

Use `workspace access bucket grant` or `workspace access app grant` only
when bounded use is intentionally workspace-wide. On denial, stop the blocked
action, preserve the config and plan, and tell the user the missing permission
and resource. Never self-grant, switch credentials, broaden scope, or retry
with guessed authority. Do not run an access command unless the user explicitly
requests it and the caller is authorised. Read the `fused-cli` skill's
`reference/access-management.md` for the full matrix.

SDK ownership does not have to match its audience. The owning person or team
keeps management authority, while `fused-cli workspace access app grant
<sdk-id>` grants bounded workspace-wide use across every SDK version without
replacing or revealing its runtime token. Likewise, a platform-owned bucket shared with `workspace access bucket grant <bucket-name>` can be selected by any eligible
owning team without granting those teams secret management.

`sdk sync` full-mirrors the exact Engine app version declared by the local
SDK config back into that file. Anything the Engine app no longer selects is removed locally, not just flagged, and Engine values win on any conflict.
There is no implicit latest lookup or sync-time version upgrade; change the
config's `version`, then plan and apply that exact version deliberately.

Every persisted selection returned by Engine must carry exactly
`schema_version: 3`; this is Engine response metadata, not an `sdk.yaml` field.
`sdk sync` accepts only that current value. A missing or older value requires
the original config to publish a new SDK version before retrying.
A higher unknown value requires a newer CLI. Never infer or normalize
selection policy.

Generated SDK calls only ever carry Fused selectors (`endUserRef`, `authType`, `resourceId`) -- never a raw provider token, API key, or provider base URL. For execution-contract negotiation, keep each pinned service version's `contract_version` and `required_capabilities` intact: Registry and Engine accept additive documentation fields but fail closed on unsupported execution semantics. Never bypass that error by dropping fields or adding provider-specific client logic; upgrade Engine or publish semantics implemented end to end. These identifiers describe provider execution, not SDK operation grants or token allowlists.
Pagination comes from the endpoint and effective service-version policy. Do not
put strategy or invocation bounds in `sdk.yaml`. Generated clients make one
Engine call; Engine owns continuation and aggregation. Physical calls may only
lower the limit with `fused.pagination.maxPages`/`max_pages`; Unified uses a
target-keyed `pagination` map beside `targets` and `selectors`. It remains one
buffered graph call whose success is the configured output or, without one,
the all-settled envelope. Never loop pages or pass continuation tokens locally.

Quota, concurrency, and retry v3 policy is inherited and enforced only by Engine. Generated TypeScript and Python clients never maintain local windows,
buckets, semaphores, retry loops, jitter, or sleeps. Do not add those policy fields to SDK config or replay provider calls: Engine coordinates identities,
response-driven limits, idempotency predicates, and retry bounds across pages and processes. Application-level business retries remain a separate concern.

Structured webhook verification, post-auth discovery, media-upload workflows, catalog composition, and OpenAPI 3.2 whole-query/sequential-media behavior are Engine-owned. Generated callers preserve exact `QUERY` or custom method tokens and pass a declared `querystring` value once in their one logical Engine call; they never expose `secret_ref`, sign, percent-encode whole queries, frame provider streams, advance workflows, or merge catalogues. Ordinary operation inputs and Fused selectors such as `resourceId` are the only caller surface.

## Runtime behavior

For generated-client timeouts, SSE lifetime controls, the shared gRPC channel,
and Engine endpoint precedence, read `reference/runtime.md` only when configuring
or diagnosing SDK runtime behavior.
