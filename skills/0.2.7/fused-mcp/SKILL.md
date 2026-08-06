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

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli mcp <subcommand> --help` to confirm before relying on one (see
`fused-cli` skill).

```shell
fused-cli mcp plan
fused-cli mcp apply
fused-cli mcp validate
fused-cli mcp list
fused-cli mcp deactivate <mcp-name@version-or-version-id>
```

`mcp apply` doesn't just validate config -- it stands up (or updates) a
persistent, named Engine-hosted server with its own URL, which stays live
until explicitly deactivated. `mcp list` shows each server's name, version, ID,
and active state. The server URL is what
you hand to an MCP client's SSE connection (see below). Deactivating one is
immediate and not gated behind the same SDK-config blocker `fused-workspace`
describes for removing a workspace service.

## Permissions and team access

A new MCP plan requires `app.create`, `service.read`, and `bucket.read`.
Planning an update requires `app.manage` plus the dependency reads. Apply
requires `app.create` for a new server or `app.manage` for an existing
one, together with `service.consume` for every selected service and `bucket.use`
for its bucket. `mcp list` requires `app.read`, deactivation requires
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
