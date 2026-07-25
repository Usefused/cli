---
name: fused-sdk
description: "Use when the user wants to generate or manage a typed SDK package from a Fused workspace using fused-cli -- selecting services/operations/webhooks, scoping auth or OAuth consent for the SDK, or running sdk plan/apply/validate/download/sync/token. Trigger on 'generate an SDK', 'fused-cli sdk', 'kind: sdk' files, or SDK language/version questions. For MCP server generation read fused-mcp instead; for the auth_type/connect scope shape itself read fused-config."
---

# SDK artifact config

`kind: sdk`, managed by `fused-cli sdk ...`. Declares a typed SDK package
generated from a bucket's already-configured services.

```yaml
apiVersion: fused/v1
kind: sdk
name: my-sdk
version: "1.0.0"
language: typescript
bucket: default
services:
  <service-slug>:
    version: "v1"
    operations: ["listUsers", "createUser"]   # or select_all: true
    webhooks: ["repo-a"]
    auth:    { type: "api_key" }              # see fused-config for the full auth_type reference
    connect: { scopes: ["read:users"] }       # OAuth/OIDC consent ceiling, see fused-config
```

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

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli sdk <subcommand> --help` to confirm before relying on one (see
`fused-cli` skill).

```shell
fused-cli sdk plan
fused-cli sdk apply
fused-cli sdk validate
fused-cli sdk download
fused-cli sdk sync [--sync-version]
fused-cli sdk token <sdk-id> generate|list|revoke <name>
fused-cli sdk service <slug> add
fused-cli sdk service <slug> remove
fused-cli sdk service <slug> operation add|remove
fused-cli sdk service <slug> webhook add|remove
```

`sdk token` manages named, revocable API tokens for calling an already
*generated* SDK's Engine endpoint (distinct from your own `fused-cli config
set api-key`, which authenticates CLI-to-Engine management calls, not a
generated SDK's own runtime traffic) -- `generate` prints the token exactly
once, so capture it immediately.

`sdk sync` full-mirrors the most recently generated SDK's actual
service/operation selections back into this file: anything the remote SDK
no longer selects is removed locally, not just flagged, and the remote's
values win on any conflict. The top-level `version` field is treated as
identity, not state, so sync leaves it alone by default -- pass
`--sync-version` to explicitly bump it to match the generated artifact.

Generated SDK calls only ever carry Fused selectors (`endUserRef`,
`authType`, `resourceId`) -- never a raw provider token, API key, or
provider base URL.
