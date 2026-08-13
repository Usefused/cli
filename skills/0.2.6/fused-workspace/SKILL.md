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
    public: false                 # Registry visibility of the service page itself -- owner-only, see below
    execution_policy: {...}      # default applied to every version below unless it overrides -- see fused-config skill
    versions:
      - version: "v1"
        service_version_id: "..."   # Engine-resolved; written back by plan/sync, not something you set by hand
        public: false                # per-version Registry visibility override -- owner-only
        execution_policy: {...}      # overrides the service-level default for just this version -- see fused-config skill
        connection_profiles: [...]   # raw, Engine-validated, scoped to this version -- see fused-config skill
buckets:
  <bucket-name>: {...}           # see fused-bucket skill
deprecations:
  - service_id: "..."
    effective_at: "YYYY-MM-DD"
    reason: "..."
```

`versions` is a list of objects, one per enabled version -- not a bare list of
version strings. Each entry's own `public`/`execution_policy`/
`connection_profiles` are scoped to just that version; a version with none of
these overrides is just `{version: "v1"}`. Version identity and all overrides
must be nested in that one entry. Flat version strings and service-level
override lists fail to parse so no policy can be detached from its version.

`buckets.<name>` here only configures an *existing* bucket's
`service_config`/`secrets` -- **it cannot create the bucket itself.** Apply
resolves `<bucket-name>` against a bucket that must already exist and fails
with "bucket not found" if it doesn't; it never creates one implicitly. Run
`fused-cli bucket create <bucket-name>` first (see `fused-bucket`) for any bucket
name you're about to reference here.

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

Overriding the blocker with `--force`/`--version-force` also writes a
`workspace_service_removed`/`workspace_version_removed` notification -- a
record that you made that decision, not a new warning. See
`fused-notifications` for what that (and the separate, proactive
`registry_*` notifications a service you depend on can trigger just by
being changed upstream) actually means and where you see it.

## Commands

This list may be behind the CLI's actual flags/subcommands -- run
`fused-cli workspace <subcommand> --help` to confirm before relying on one
(see `fused-cli` skill).

```shell
fused-cli workspace plan
fused-cli workspace apply
fused-cli workspace services list
fused-cli workspace service versions <service-slug>
fused-cli workspace service operations <service-slug>
fused-cli workspace service webhooks <service-slug>
fused-cli workspace service add <service-slug> --version <version>
fused-cli workspace service delete <service-slug> [--force]
fused-cli workspace service deprecate <service-slug> --at <RFC3339> --reason "..."
fused-cli workspace service version add <service-slug> <version|latest>
fused-cli workspace service version delete <service-slug> <version> [--force]
fused-cli workspace service version deprecate <service-slug> <version> --at <RFC3339>
fused-cli workspace service connect <service-slug> --bucket <bucket-name-or-id> --user-ref <end-user-reference> [--scope ...]
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
- `versions` is compared as a set of version names, not an ordered list -- a
  difference in order alone is never reported as a change, and the
  currently-active version always has an entry even if the Engine's
  enabled-list didn't separately include it. Only an already-enabled version
  can carry a per-version override; sync never creates a new `versions`
  entry just to attach one.
- If a service's local YAML key doesn't match its current canonical slug
  (e.g. it was written under an old display name, or the slug changed),
  sync recognizes it's the same `service_id` and **rekeys** the block to the
  current slug -- reported as one `Added` + one `Removed` in the sync
  summary, not data loss. Each version entry's `execution_policy` and
  `connection_profiles` carry over to the new key intact.
- Sync only touches the fields it owns (`versions` and, within each entry,
  `public`, `execution_policy`, connection profile attachment) -- any other
  local block you've hand-written under a service carries over untouched,
  including across that key-rename case above. Webhook registrations live in
  their own `kind: webhook` files entirely outside this sync (see
  `fused-webhook`), so there's nothing webhook-related for `workspace sync`
  to preserve or touch in the first place.
- `public` (service-level or per-version) is written only for services you
  own (`true` or `false`, whichever the Registry reports); it's omitted
  entirely for a third-party service, matching that it can't be set for one
  either (see above).
- A version's `execution_policy` round-trips from the Registry only when you
  own the service **and** that version has a published `public: true` policy
  there -- it comes back with the real published values, so a subsequent
  `apply` is a no-op instead of silently wiping that policy. But if your
  local file already has an `execution_policy` block on that version, sync
  leaves it alone unconditionally and never overwrites it with what it
  fetched from the Registry, published or not -- local always wins over
  remote here. This also means: your own local-only `execution_policy`
  declarations for services you don't own or haven't published (see
  `fused-config`) are never touched by sync, since the Engine's local
  override table those feed into (separate from anything this reads) isn't
  something sync queries at all.
- A `buckets.<bucket>.service_config.<slug>.connect` block with no remote
  projection is left alone (a workspace-local profile isn't erased just
  because the Registry has nothing to report for it); Registry-sourced
  profile attachments sync back as `profile_id`, workspace-local ones as an
  inline `profile` (see `fused-config`).
