---
name: fused-webhook
description: "Use when the user wants to register inbound provider webhook ingress using fused-cli -- creating a kind: webhook config, attaching it to an SDK via webhook_attachment, or running webhook plan/apply/validate. Trigger on 'register a webhook', 'kind: webhook', 'webhook_attachment', 'provider events in my SDK', or 'fused-cli webhook'. Fused-owned connected-auth lifecycle events need no ingress registration; read fused-sdk for that generated receiver behavior. For the read-only workspace service webhooks command read fused-workspace instead."
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

- `name` is this config's identity (like `kind: sdk`'s `name`) -- there is
  no per-service label anymore. `(service, name)` must be globally unique per
  account: a second `kind: webhook` config trying to claim a `(service,
  name)` pair another config already owns is a plan-time conflict, never a
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

## Imported verification headers

For a service whose imported signed-webhook contract declares
`verification_headers`, treat that list as the complete, reviewed header set.
Preserve source order and spelling; Fused removes blank and case-insensitive
duplicate names. Only when a `signature_header` list is absent does OpenAPI
import infer it from required header parameters on webhook operations. Do not
add guessed provider headers or put header values in the Registry contract.

## Attaching provider events to an SDK

Registering a webhook here only makes Fused *accept* the inbound delivery --
it does not, by itself, route that event to an SDK. A `kind: sdk` config opts
into provider delivery with `webhook_attachment` (top-level, sibling to
`name`/`bucket` -- not nested under `services`, since one `kind: webhook`
config can span services the attaching config also uses) plus a per-service
explicit event allowlist:

The ordinary attachment example chooses visible `default` as its candidate.
SDK plan/apply must pass `bucket.use` for that exact bucket, or the action stops
without creating a fallback.

```yaml
apiVersion: fused/v1
kind: sdk
name: jira-sdk
bucket: default
webhook_attachment: team-x-webhooks
services:
  jira:
    operations: [...]
    webhooks: ["issue.created", "issue.updated"]   # or webhooks_select_all: true
  github:
    operations: [...]
    webhooks: ["push"]
```

- `webhook_attachment` names exactly one `kind: webhook` config -- one
  attachment per SDK today (a list isn't supported yet).
  Required as soon as any service below sets `webhooks` or
  `webhooks_select_all: true`; omitting it while selecting webhooks is
  rejected at plan time, both locally and by the Engine.
- `services.<slug>.webhooks` is the explicit event allowlist for that
  service. Empty/omitted means no events (explicit opt-in, never an implicit
  "all"). The Engine checks `webhook_attachment` the same way it checks
  `bucket`: the named `kind: webhook` config must already exist, and must
  register every service that sets `webhooks` or `webhooks_select_all` here
  -- both checked at plan time and re-checked at apply. Attaching to a name
  that was never applied, or that doesn't cover this service, is rejected
  with a named error instead of silently never delivering. The CLI itself
  still only checks that `webhook_attachment` is non-empty when a service
  selects webhooks (it has no store access to look the config up); the
  coverage check is Engine-only.
- `webhooks_select_all: true` is the webhook-only counterpart to the
  operations `select_all: true` you already know from `fused-sdk`/
  `fused-mcp` -- either can be set independent of the other. `kind: mcp`
  cannot select provider webhooks at all (neither field is valid on an MCP
  service).
- Delivery is scoped to exactly this attachment: the WS bridge resolves
  which `kind: webhook` config a connecting SDK attached
  server-side (from its own applied config), so two different registrations
  for the same service+event never cross-deliver to an SDK that only
  attached to one of them.

## Fused-owned auth lifecycle events

Do not create a `kind: webhook` config for connected-auth lifecycle events.
When an SDK's selected auth path uses connected OAuth/OIDC, Engine implicitly
adds family- and service-scoped Fused subjects and generated event types to the
same SDK webhook receiver. No callback URL, signing secret,
`webhook_attachment`, `services.<slug>.webhooks`, or separate listener is
needed. Their reserved suffixes are `fused.auth.connection.completed`,
`fused.auth.token.refreshed`, `fused.auth.token.refresh_failed`, and
`fused.auth.connection.reconnect_required`. Use the generated per-service enum,
such as `JiraWebhook.FUSED_AUTH_CONNECTION_COMPLETED`, because the wire value
keeps the existing `<service-id>.<event-name>` receiver namespace.

The application still starts the generated receiver and registers handlers at
runtime. Provider events and Fused-owned events share its durable ack/nack and
reconnect behavior; retained deliveries are at least once. Auth lifecycle
publication after the database commit is best-effort until Fused has a
transactional outbox. Read `fused-sdk` for SDK-family routing and runtime
consumption.

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

A new webhook plan requires `app.create` and `service.read`; an update
requires `app.manage` and `service.read`. It also needs `bucket.read` for
each bucket named by a secret reference. Apply requires `app.create` for a
new registration bundle or `app.manage` for an existing one, plus
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
