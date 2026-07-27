---
name: fused-workspace
description: "Use when the user wants to configure a Fused workspace's service allowlist using fused-cli -- enabling/disabling services or versions, listing a service's existing webhook registrations (read-only), or scheduling a deprecation. Trigger on 'workspace config', 'enable a service', 'fused-cli workspace', 'deprecate a service version', or 'kind: workspace' files. For registering a new inbound webhook read fused-webhook instead; for rate limits/retries/pagination/outbound-webhook-verification (execution_policy) or auth/connect/dynamic-binding config, read fused-config instead; for bucket credentials read fused-bucket."
---

# Workspace config

`kind: workspace`, managed by `fused-cli workspace ...`. This is the service
allowlist: which services and versions are enabled, plus the deprecation
schedule and per-service runtime behavior that isn't rate-limit or auth
related.

```yaml
apiVersion: fused/v1
kind: workspace
services:
  <service-slug>:
    versions: ["v1"]
    public: false                 # Registry visibility of the service page itself -- owner-only, see below
    execution_policy: {...}      # rate limits, retries, pagination, base_url override, webhook verification -- see fused-config skill
    connection_profiles: [...]   # raw, Engine-validated -- see fused-config skill
buckets:
  <bucket-name>: {...}           # see fused-bucket skill
deprecations:
  - service_id: "..."
    effective_at: "YYYY-MM-DD"
    reason: "..."
```

There is no `runtime_config` field on a workspace service anymore -- it was
removed with no backward compatibility once `kind: webhook` shipped (a
`workspace.yaml` still containing `runtime_config: {...}` now fails to parse
outright with a "field not found" error; that's the intended hard rejection,
not a bug). Everything that used to live there moved out: webhook
registration is now its own `kind: webhook` config file, spanning one or more
services and attached to whichever SDK/MCP artifact should receive delivery
(see `fused-webhook`); `auth`/`connect` moved to
`buckets.<bucket>.service_config.<slug>` (see `fused-bucket`);
`pagination`/`pagination_overrides` moved under `execution_policy.pagination`
(one value per service/version, no more per-operation overrides map); and
`base_url` moved under `execution_policy.base_url` -- an owner override for a
wrong or missing spec-derived base URL, workspace-settable and, with
`execution_policy.public: true`, publishable to every other consumer too
(see `fused-config`'s `reference/execution-policies.md` for both).
`default_headers` is the one field from the old `runtime_config` that
genuinely has **no** owner-editable path today -- it's still
Registry/import-derived only, not settable via `execution_policy` or
anywhere else in workspace.yaml.

`public` is Registry visibility of the service page itself (via
`updateServicePublic`) -- `true` makes it visible to every Registry
consumer, `false` keeps it private to this account. Only the owning account
can set it; it's omitted entirely for a third-party service you don't own.
Don't confuse this with `execution_policy.public`, a different owner-only
toggle that publishes rate limit/retry/pagination/base_url/webhook-verification
settings to the Registry instead -- and unlike this `public`,
`execution_policy` itself always has a real *local* effect in this
workspace regardless of whether `execution_policy.public` is set (see
`fused-config`).

## Deprecating and removing a service

`deprecations` is a **soft, advisory** signal, not an immediate cutoff:
adding one does not deactivate anything by itself. It records intent (a
target date, a reason) while the service or version stays fully active, so
existing SDK/MCP configs that reference it keep working while consumers
migrate. Actually deactivating it later is a separate step: `workspace
service <slug> remove` (whole service) or `workspace service <slug> version
remove <v>` (one version).

Removal itself is blocked by default if any SDK/MCP config in the same
Registry account still references the service or version being removed --
the plan comes back with a blocker requiring an explicit decision rather
than silently breaking those configs. Pass `--force` (service-level) or
`--version-force` (version-level) to accept that and remove it anyway.
There's no automatic date-triggered cutover from `effective_at` alone --
treat it as the value you'll pass to the eventual `remove --force` once
you've actually confirmed nothing still needs the service.

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli workspace <subcommand> --help` to confirm before relying on one
(see `fused-cli` skill).

```shell
fused-cli workspace plan
fused-cli workspace apply
fused-cli workspace services list
fused-cli workspace service <slug> versions
fused-cli workspace service <slug> operations
fused-cli workspace service <slug> webhooks   # read-only: lists this service's kind: webhook registrations, see fused-webhook
fused-cli workspace service <slug> add --version <v>
fused-cli workspace service <slug> remove [--force]
fused-cli workspace service <slug> deprecate --effective-at <date> --reason "..."
fused-cli workspace service <slug> version add <v|latest>
fused-cli workspace service <slug> version remove <v> [--version-force]
fused-cli workspace service <slug> version deprecate <v>
fused-cli workspace service <slug> connect --bucket <name> --user-ref <ref> [--scope ...]
```

`connect` starts an OAuth/OIDC session for one user against a bucket -- the
full flow (buckets, secrets, connection resources) is documented in
`fused-bucket`.

## Production warning

Applying a workspace config can activate or deactivate services
workspace-wide. The CLI warns when the target Engine reports
`environment=production` -- surface that warning to the user before running
`apply`.

## Sync

`fused-cli workspace sync` overwrites the local `services:` map with
whatever the Engine reports as actually activated for this workspace --
it's a full mirror, not a merge of additions only:

- A service the Engine no longer reports as activated is **dropped from the
  local file entirely**, not just flagged. An empty remote result empties
  the whole `services:` map.
- `versions` is compared as a set, not an ordered list -- a difference in
  order alone is never reported as a change, and the currently-active
  version is always folded into `versions` even if the Engine's enabled-list
  didn't separately include it.
- If a service's local YAML key doesn't match its current canonical slug
  (e.g. it was written under an old display name, or the slug changed),
  sync recognizes it's the same `service_id` and **rekeys** the block to the
  current slug -- reported as one `Added` + one `Removed` in the sync
  summary, not data loss. Nested config (`execution_policy`,
  `connection_profiles`, etc.) carries over to the new key intact.
- Sync only touches the fields it owns (`versions`, `public`,
  `execution_policy`, connection profile attachment) -- any other local
  block you've hand-written under a service carries over untouched,
  including across that key-rename case above. Webhook registrations live in
  their own `kind: webhook` files entirely outside this sync (see
  `fused-webhook`), so there's nothing webhook-related for `workspace sync`
  to preserve or touch in the first place.
- `public` is written only for services you own (`true` or `false`,
  whichever the Registry reports); it's omitted entirely for a third-party
  service, matching that it can't be set for one either (see above).
- `execution_policy` round-trips from the Registry only when you own the
  service **and** it has a published `public: true` policy there -- it
  comes back with the real published values, so a subsequent `apply` is a
  no-op instead of silently wiping that policy. But if your local file
  already has an `execution_policy` block for that service, sync leaves it
  alone unconditionally and never overwrites it with what it fetched from
  the Registry, published or not -- local always wins over remote here. This
  also means: your own local-only `execution_policy` declarations for
  services you don't own or haven't published (see `fused-config`) are never
  touched by sync, since the Engine's local override table those feed into
  (separate from anything this reads) isn't something sync queries at all.
- A `buckets.<bucket>.service_config.<slug>.connect` block with no remote
  projection is left alone (a workspace-local profile isn't erased just
  because the Registry has nothing to report for it); Registry-sourced
  profile attachments sync back as `profile_id`, workspace-local ones as an
  inline `profile` (see `fused-config`).
