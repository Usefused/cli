---
name: fused-sdk
description: "Use when the user wants to generate or manage a typed SDK package from a Fused workspace using fused-cli sdk -- selecting services/operations, receiving webhook events, scoping auth, or running SDK plan/apply/validate/download/sync/token commands. Trigger on 'generate an SDK', 'fused-cli sdk', or 'kind: sdk' files. For Engine-hosted MCP runtime behavior read fused-mcp instead."
---

# SDK package config

If the user starts with a business goal and no existing config, first follow
the `fused-cli` skill's `reference/build-sdk-or-mcp.md` workflow. Use this skill
for the SDK-specific config and lifecycle once Engine setup, service discovery,
workspace activation, and credential requirements are understood.

`kind: sdk`, managed by `fused-cli sdk ...`, declares a typed SDK package
generated from a bucket's already-configured services.

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
type. Omitting `auth` pins the first scheme the selected service version
declares. `connect.scopes` narrows OAuth/OIDC consent -- an application can
request fewer scopes per user but never more than declared here. Credential
material itself never lives in this file -- it's resolved from `bucket` at
generation/dispatch time (see `fused-bucket`).

`select_all: true` is the alternative to listing `operations` explicitly --
exactly one of the two is required, never both/neither. Be aware `sdk sync`
(below) always freezes whatever's currently selected into an explicit,
sorted operation list; a service configured with `select_all: true` does
not come back as `select_all: true` after a sync, even if nothing else
about it changed.

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
local secret manager. The current CLI has no SDK deprecate/deactivate command;
do not invent one. Those lifecycle actions are available through Engine's App
UI/API.

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli sdk <subcommand> --help` to confirm before relying on one (see
`fused-cli` skill).

```shell
fused-cli sdk plan
fused-cli sdk apply
fused-cli sdk validate
fused-cli sdk download <sdk-name@version-or-version-id>
fused-cli sdk sync <sdk-name>
fused-cli sdk show <sdk-name@version-or-version-id>
fused-cli sdk services <sdk-name@version-or-version-id>
fused-cli sdk buckets <sdk-name-or-id>
fused-cli sdk token generate <sdk-name-or-id> <token-name>
fused-cli sdk token list <sdk-name-or-id>
fused-cli sdk token revoke <sdk-name-or-id> <token-name-or-id>
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
for the selected bucket. Download requires `app.read`; `sdk token`
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
<sdk-id>` grants bounded workspace-wide use across every SDK version. This does
not replace
the generated SDK's runtime token or reveal it; token issuance/revocation stays
with SDK managers. Likewise, a platform-owned bucket shared with
`workspace access bucket grant <bucket-name>` can be selected by any eligible
owning team without granting those teams secret management.

`sdk sync` full-mirrors the exact Engine app version declared by the local
SDK config back into that file. Anything the Engine app no longer selects is
removed locally, not just flagged, and Engine values win on any conflict.
There is no implicit latest lookup or sync-time version upgrade; change the
config's `version`, then plan and apply that exact version deliberately.

An SDK definition created before portable selection metadata was versioned
cannot be synced safely: its auth choice, narrowed connect scopes, or injection
expressions may have been discarded. The CLI rejects that definition before
writing the local file. Use the original config to publish a new SDK version,
then retry `sdk sync`; do not infer missing security policy from endpoint IDs.

Generated SDK calls only ever carry Fused selectors (`endUserRef`,
`authType`, `resourceId`) -- never a raw provider token, API key, or
provider base URL.

## Runtime timeout contract

Generated clients expose `ExecutionTimeoutError` in TypeScript and Python.
`timeoutMs` / `timeout_ms` defaults to 30 seconds and bounds `Connect`, buffered
execution, and the wait for an SSE stream's first event. It is a caller-side
deadline; it does not configure the service owner’s Engine policy.

For long-lived SSE operations, configure `streamIdleTimeoutMs` /
`stream_idle_timeout_ms` for the gap between events and
`maxStreamDurationMs` / `max_stream_duration_ms` for an optional total stream
cap. If neither stream setting is supplied, an established stream may remain
open until the provider, Engine, consumer, or Engine execution policy closes
it. Stopping iteration cancels the underlying gRPC call.

Service owners set the independent Engine cap in workspace configuration:

```yaml
services:
  jira:
    execution_policy:
      timeout_ms: 45000  # 1..86400000; omitted means no Engine cap
```

An exact service-version execution policy overrides the service-level timeout.
If the Engine policy expires, the client receives `ExecutionTimeoutError` with
code `execution_timeout` and the enforced `timeout_ms`. If the caller deadline
expires first, the client still receives the same typed class with its own
configured budget; Engine telemetry must not misattribute it to policy expiry.

## SDK runtime: shared gRPC channel

Every generated SDK opens **exactly one gRPC channel** to the Engine when
`FusedSDK` is instantiated. All services in the SDK share that channel over
a single HTTP/2 connection -- there is no per-service connection overhead.

### Configuring the Engine endpoint

The channel target is resolved in this order (first non-empty value wins):

1. `engine_url` / `engineUrl` passed directly to `FusedSDK(...)`.
2. `FUSED_ENGINE_GRPC_URL` environment variable.
3. `FUSED_ENGINE_URL` environment variable.
4. Default: `http://127.0.0.1:50051`.

> **Local dev**: the Fused Engine binds REST to port **8081** and gRPC to
> **8082** by default. Always point `engine_url` / `FUSED_ENGINE_GRPC_URL` at
> the gRPC port; connecting to the REST port returns HTTP 405.

### Python

```python
import os
from src import FusedSDK

sdk = FusedSDK({
    "engine_url": os.environ.get("FUSED_ENGINE_GRPC_URL"),  # e.g. http://localhost:8082
    "token": os.environ["FUSED_SDK_TOKEN"],
})

# Async context manager closes the channel on exit:
async with FusedSDK({"engine_url": "http://localhost:8082"}) as sdk:
    result = await sdk.async_jira.issues.list()

await sdk.close()  # manual close
```

### TypeScript

```typescript
import { FusedSDK } from 'your-sdk-package';

const sdk = new FusedSDK({
  engineUrl: process.env.FUSED_ENGINE_GRPC_URL, // e.g. http://localhost:8082
  token: process.env.FUSED_SDK_TOKEN,
});

process.on('SIGTERM', () => sdk.close()); // release channel on shutdown
```
