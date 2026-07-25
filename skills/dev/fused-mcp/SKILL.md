---
name: fused-mcp
description: "Use when the user wants to generate or manage an MCP server from a Fused workspace using fused-cli -- selecting services/operations/webhooks for the MCP tool surface, or running mcp plan/apply/validate/list. Trigger on 'MCP server', 'fused-cli mcp', 'kind: mcp' files. For SDK package generation read fused-sdk instead; for the auth_type/connect scope shape itself read fused-config."
---

# MCP artifact config

`kind: mcp`, managed by `fused-cli mcp ...`. Shares the same
`ArtifactConfig` shape as SDK (minus `language`) so plan results don't drift
between the two, but an MCP apply creates an Engine-hosted runtime only --
it never builds or archives a package.

```yaml
apiVersion: fused/v1
kind: mcp
name: customer-support
version: "1.0.0"
bucket: customer-accounts
services:
  <service-slug>:
    version: "v1"
    operations: ["getIssue", "createIssue"]   # or select_all: true
    webhooks: ["repo-a"]
    auth:    { type: "oauth" }                # see fused-config
    connect: { scopes: ["read:jira-work"] }   # see fused-config
```

`select_all: true` is the alternative to listing `operations` explicitly --
exactly one of the two is required (see `fused-sdk`; the validation and
sync-freezing behavior described there is the same struct shared with SDK).

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli mcp <subcommand> --help` to confirm before relying on one (see
`fused-cli` skill).

```shell
fused-cli mcp plan
fused-cli mcp apply
fused-cli mcp validate
fused-cli mcp list
fused-cli mcp <name> remove
```

`mcp apply` doesn't just validate config -- it stands up (or updates) a
persistent, named Engine-hosted server with its own URL, which stays live
until explicitly removed. `mcp list` shows each one's name, version, ID,
whether it's active, when it was created, and that URL -- that URL is what
you hand to an MCP client's SSE connection (see below). Removing one is
immediate and not gated behind the same SDK-config blocker `fused-workspace`
describes for removing a workspace service.

## Calling the running MCP

Neither the tool schema nor its `call()` function accepts provider tokens,
API keys, auth scheme names, or Fused user selectors as parameters -- those
are configured once on the MCP client connection, not per call:

```text
Authorization: Bearer <one-time MCP execution token>
X-Fused-End-User-Ref: user_123
X-Fused-Resource-ID: <optional Fused connection-resource UUID>
```

`X-Fused-End-User-Ref` is required only for connected OAuth/OIDC calls.
`X-Fused-Resource-ID` is required when that connection has multiple
provider sites/shops/portals/accounts and no default is set (see
`fused-bucket` for setting a default resource). Engine middleware resolves
the bucket credential and applies connection-resource bindings before
dispatch -- the agent driving the MCP only ever picks operations and
provider arguments.
