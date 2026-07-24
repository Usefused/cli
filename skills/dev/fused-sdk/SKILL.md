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
`api_key`, `oauth`, `oidc`, `mtls`); `auth.name` disambiguates two schemes of
the same type. Omitting `auth` pins the first scheme the selected service
version declares. `connect.scopes` narrows OAuth/OIDC consent -- an
application can request fewer scopes per user but never more than declared
here. Credential material itself never lives in this file -- it's resolved
from `bucket` at generation/dispatch time (see `fused-bucket`).

## Commands

```shell
fused-cli sdk plan
fused-cli sdk apply
fused-cli sdk validate
fused-cli sdk download
fused-cli sdk sync
fused-cli sdk token
fused-cli sdk service <slug> add
fused-cli sdk service <slug> remove
fused-cli sdk service <slug> operation add|remove
fused-cli sdk service <slug> webhook add|remove
```

Generated SDK calls only ever carry Fused selectors (`endUserRef`,
`authType`, `resourceId`) -- never a raw provider token, API key, or
provider base URL.
