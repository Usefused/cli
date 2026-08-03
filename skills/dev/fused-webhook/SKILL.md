---
name: fused-webhook
description: "Use when the user wants to register inbound webhook ingress (a provider calling into Fused) using fused-cli -- creating a kind: webhook config, attaching it to an SDK via webhook_attachment so that SDK receives delivery, or running webhook plan/apply/validate. Trigger on 'register a webhook', 'kind: webhook', 'webhook_attachment', 'receive webhooks in my SDK', or 'fused-cli webhook'. For the read-only workspace service webhooks command read fused-workspace instead; for the auth_type/connect scope shape itself read fused-config."
---

# Webhook registration config

`kind: webhook`, managed by `fused-cli webhook ...`. A named, team-owned
bundle of inbound webhook registrations that can span multiple services --
independent of any one SDK/MCP/workspace file, with its own plan/apply
lifecycle. This is the only way to register an inbound webhook today;
`kind: workspace`'s old `runtime_config.webhooks` field has been removed with
no backward compatibility.

```yaml
apiVersion: fused/v1
kind: webhook
name: team-x-webhooks
services:
  jira:
    secret: "${bucket.default.secret.jira_signing}"
  github:
    secret: "${bucket.default.secret.github_signing}"
```

- `name` is this artifact's identity (like `kind: sdk`'s `name`) -- there is
  no per-service label anymore. `(service, name)` must be globally unique per
  account: a second `kind: webhook` artifact trying to claim a `(service,
  name)` pair another artifact already owns is a plan-time conflict, never a
  silent takeover.
- `services.<slug>.secret` is a `${bucket.<bucket-name>.secret.<key>}`
  reference (or the `${bucket.secret.<key>}` shorthand against the `default`
  bucket) -- the signing secret this registration verifies inbound
  deliveries against. Omit it for a provider that doesn't sign webhooks.
  This is the same bracketed `${...}` grammar connection profiles use for
  `${resource.*}` expressions (see `fused-config`), just with a mandatory
  bucket segment since webhook verification has no dispatch-selected bucket
  to fall back on the way an SDK/MCP call does.
- Removing a service from the map (or deleting the whole file) is a normal
  apply-time diff, same as any other kind -- no separate imperative delete
  command.

## Attaching to an SDK/MCP so it actually receives events

Registering a webhook here only makes Fused *accept* the inbound delivery --
it does not, by itself, route that event to any SDK or MCP. A `kind: sdk` or
`kind: mcp` artifact opts into delivery with `webhook_attachment` (top-level,
sibling to `name`/`bucket` -- not nested under `services`, since one
`kind: webhook` artifact can span services the attaching artifact also uses)
plus a per-service explicit event allowlist:

```yaml
apiVersion: fused/v1
kind: sdk
name: jira-sdk
bucket: customer-accounts
webhook_attachment: team-x-webhooks
services:
  jira:
    operations: [...]
    webhooks: ["issue.created", "issue.updated"]   # or webhooks_select_all: true
  github:
    operations: [...]
    webhooks: ["push"]
```

- `webhook_attachment` names exactly one `kind: webhook` artifact -- one
  attachment per SDK today (a list isn't supported yet).
  Required as soon as any service below sets `webhooks` or
  `webhooks_select_all: true`; omitting it while selecting webhooks is
  rejected at plan time, both locally and by the Engine.
- `services.<slug>.webhooks` is the explicit event allowlist for that
  service. Empty/omitted means no events (explicit opt-in, never an implicit
  "all"). The Engine checks `webhook_attachment` the same way it checks
  `bucket`: the named `kind: webhook` artifact must already exist, and must
  register every service that sets `webhooks` or `webhooks_select_all` here
  -- both checked at plan time and re-checked at apply. Attaching to a name
  that was never applied, or that doesn't cover this service, is rejected
  with a named error instead of silently never delivering. The CLI itself
  still only checks that `webhook_attachment` is non-empty when a service
  selects webhooks (it has no store access to look the artifact up); the
  coverage check is Engine-only.
- `webhooks_select_all: true` is the webhook-only counterpart to the
  operations `select_all: true` you already know from `fused-sdk`/
  `fused-mcp` -- either can be set independent of the other. `kind: mcp`
  cannot select webhooks at all (neither field is valid on an MCP service).
- Delivery is scoped to exactly this attachment: the WS bridge resolves
  which `kind: webhook` artifact a connecting SDK/MCP attached
  server-side (from its own applied config), so two different registrations
  for the same service+event never cross-deliver to an SDK/MCP that only
  attached to one of them.

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli webhook <subcommand> --help` to confirm before relying on one
(see `fused-cli` skill).

```shell
fused-cli webhook plan
fused-cli webhook apply
fused-cli webhook validate
```

`apply` prints each registration's URL as `<engine-url>/webhook/<slug>-<service>`.
To look up a service's registrations later without re-running apply, use the
read-only `fused-cli workspace service webhooks <slug>` command (see
`fused-workspace`) -- its `SIGNATURE` column is `set`/`none` only, never the
secret value.

## Permissions and team access

A new webhook plan requires `artifact.create` and `service.read`; an update
requires `artifact.manage` and `service.read`. It also needs `bucket.read` for
each bucket named by a secret reference. Apply requires `artifact.create` for a
new registration bundle or `artifact.manage` for an existing one, plus
`service.consume` for every registered service and `bucket.use` for each
referenced secret bucket. A webhook without a secret reference has no bucket
permission requirement.

For team ownership, preflight the team and dependencies before planning:

```shell
fused-cli team eligible-owners
fused-cli team build-access <team> --resource service
fused-cli team build-access <team> --resource bucket
fused-cli webhook plan --owner-team <team>
```

An authorised administrator can grant the missing dependency narrowly:

```shell
fused-cli team access service grant <team> <service> use
fused-cli team access bucket grant <team> <bucket> use
```

On denial, stop the blocked action, preserve the config and plan, and tell the
user the missing permission and resource. Never self-grant, switch credentials,
broaden scope, or retry with guessed authority. Do not run access-changing
commands unless explicitly requested and authorised. Read the `fused-cli`
skill's `reference/access-management.md` for the complete matrix.
