---
name: fused-workspace
description: "Use when the user wants to configure a Fused workspace's service allowlist using fused-cli -- enabling/disabling or publishing services or versions, distinguishing service visibility from version visibility, listing a service's existing webhook registrations (read-only), or scheduling a deprecation. Trigger on 'workspace config', 'enable a service', 'publish a service', 'service visibility', 'version visibility', 'fused-cli workspace', 'deprecate a service version', or 'kind: workspace' files. For registering a new inbound webhook read fused-webhook instead; for rate limits/retries/pagination/outbound-webhook-verification (execution_policy), connection-profile attachments, or SDK/MCP auth/connect config, read fused-config instead; for bucket credentials read fused-bucket."
---

# Workspace config

`kind: workspace`, managed by `fused-cli workspace ...`. This is the service
allowlist: which services and versions are enabled, plus their deprecation
schedule and credential-free execution/profile policy.

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

Workspace YAML has no bucket credential configuration. Select an existing
bucket in each `kind: sdk` or `kind: mcp` file, and store credentials with the
bucket commands in `fused-bucket`. OAuth/OIDC targets may reuse an application
credential pair with the app service's `auth.ref`; the workspace allowlist
does not own that choice.

There is no `runtime_config` field on a workspace service anymore -- it was
removed with no backward compatibility once `kind: webhook` shipped (a
`workspace.yaml` still containing `runtime_config: {...}` now fails to parse
outright with a "field not found" error; that's the intended hard rejection,
not a bug). Everything that used to live there moved out: webhook
registration is now its own `kind: webhook` config file, spanning one or more
services and attached to whichever SDK should receive delivery
(see `fused-webhook`); `auth`/`connect` moved to
each `kind: sdk` or `kind: mcp` service selection (see `fused-sdk`,
`fused-mcp`, and `fused-bucket`);
`pagination`/`pagination_overrides` moved under `execution_policy.pagination`
(one composable v3 policy per service/version, no workspace per-operation
overrides map; provider-contract endpoint pagination remains the more specific tier); and
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
An exact version execution policy wins over the local service default for that
version. Pagination is not copied into SDK/MCP config; Engine resolves it at
dispatch and streams each provider page separately.

For staged publication, use the top-level service `public` value as the
pre-publication gate, not `versions[].public`. Keep the service `public: false`
through private validation. A version with `public: true` cannot expose a
private service, and versions default public, so do not fail private validation
merely because the version is public. Version visibility is an independent
staging control for versions of a public service. After validation, set the
service `public: true` and confirm the intended version is not staged private.

## Deprecating and removing a service

`deprecations` is a **soft, advisory** signal, not an immediate cutoff:
adding one does not deactivate anything by itself. It records intent (a
target date, a reason) while the service or version stays fully active, so
existing SDK/MCP configs that reference it keep working while consumers
migrate. Actually deactivating it later is a separate step: `workspace
service delete <slug>` (whole service) or `workspace service version delete
<slug> <v>` (one version).

Removal itself is blocked by default if any SDK/MCP config in the same
Registry account still references the service or version being removed --
the plan comes back with a blocker requiring an explicit decision rather
than silently breaking those configs. Pass `--force` on the corresponding
delete command to accept that and remove it anyway.
There's no automatic date-triggered cutover from `effective_at` alone --
treat it as the value you'll pass to the eventual `delete --force` once
you've actually confirmed nothing still needs the service.

Overriding the blocker with `--force` also writes a
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
fused-cli workspace init [--service '<service>[=<version>]']
fused-cli workspace init --extend --service '<service>[=<version>]'
fused-cli workspace plan
fused-cli workspace apply
fused-cli workspace services list [--q "<provider or product>"]
fused-cli workspace service versions <slug>
fused-cli workspace service operations <slug>
fused-cli workspace service webhooks <slug>   # read-only: lists this service's kind: webhook registrations, see fused-webhook
fused-cli workspace service add <query-or-slug> [query-or-slug...] [--version <v>] [--no-input] [--apply]
fused-cli workspace service delete <slug> [--force]
fused-cli workspace service deprecate <slug> --at <date> --reason "..."
fused-cli workspace service version add <slug> <v|latest>
fused-cli workspace service version delete <slug> <v> [--force]
fused-cli workspace service version deprecate <slug> <v> --at <date> [--reason "..."]
fused-cli workspace service connect <service-slug> --bucket <bucket-name-or-id> --user-ref <end-user-reference> [--type oauth|oidc --auth-name <scheme>] [--auth-ref '${bucket.auth.<source-service>.<source-auth-name>}'] [--scope ...]
```

Use the first command to create `.fused/workspace.yaml` without replacing an
existing file. Use `--extend` to add a service or another version; the result
is `extended` or idempotent `unchanged`, and existing service metadata remains
intact. This local authoring step neither activates the service nor creates a
bucket; run `workspace plan` and review its permission-aware result before
apply.

`connect` starts an OAuth/OIDC session for one user against a bucket. Select
the target scheme directly and use `--auth-ref` when standalone consent reuses
another service's registration. This initialization/debug command has no SDK or
MCP identity selector and sends no synthetic app ID. Generated SDK consent uses
the reference pinned in app configuration and attaches only its embedded app ID
as provenance on the bucket-owned connection; the standalone command never
infers one from app identity. The full flow
(buckets, secrets, connection resources) is documented in `fused-bucket`.

Use `workspace service add <query-or-slug> [query-or-slug...]` as the single discovery-and-author
action. It checks the access-filtered workspace first by exact name or slug
(`service.read`) and reuses that result. Only when absent does it call the
Registry's set-based reference resolver (`catalogue.read`), which shares the
lexical ranking and provider-identity policy of `service search --q`. In a
terminal, ambiguous results open selection and every Registry fallback is
confirmed before the selected Registry result is added. In CI or `--no-input`,
unique or exact results are added deterministically; ambiguous results fail
until the caller supplies an exact provider-qualified slug or service ID. A permission error is not a miss:
stop instead of falling through, changing credentials, or guessing another
service. Do not require a separate `service search` command first.

For add, a provider-qualified identity must use the complete
`@provider/service-slug` form. Reject a leading `@` with a missing, blank,
whitespace-containing, or nested segment locally before discovery. This is
intentionally narrower than read-only `service search`, where incomplete `@`
text remains a lexical catalogue query.

Multiple references share batched workspace and Registry reads and are written
to the config atomically. `--service-id` remains a single-reference escape hatch;
for multiple exact IDs, pass each UUID as a positional reference. Successful
adds print the canonical service slug and a direct Engine UI URL
whose route uses the stable service ID. Use the URL to inspect the resolved
service. Without `--apply`, this output does not replace the required
`workspace plan` review and does not activate the local draft.

Without `--apply`, this command authors local intent only. It does not activate
the Registry service or prove activation permission; run `workspace plan`,
review the resolved identity and required permissions, then `workspace apply`.
With `--apply`, it composes Engine's existing scoped additive service mutation
for only the resolved references; it does not run the full-workspace mirror and
therefore cannot remove an unrelated active service. If a later activation
fails, report the command's committed, failed, and unattempted groups plus its
stable code, failed phase, composite request ID, failed-target commit
possibility, and exact ID-pinned recovery command; do not describe the whole
composite as rolled back. One `--version` applies to every reference, so use
separate commands for different explicit provider versions. Keep `service
search --q` for an explicit read-only combined view: each Registry match is
marked `enabled` or `available_to_add`, and an exact workspace-only match is
included. It requires both `catalogue.read` and `service.read`, but remains
optional rather than a prerequisite to workspace addition.

## Permissions and team access

Finding a Registry service does not imply permission to activate it. The add
command's workspace lookup requires `service.read`; only its Registry fallback
requires `catalogue.read`. Writing the local draft without `--apply` does not
call an activation endpoint; `--apply` immediately calls the scoped additive
Engine endpoint for each resolved target. Planning a declarative draft requires `workspace.read` and `service.manage`
for every changed service; applying it also requires `workspace.update` and the
same `service.manage`. Bucket changes additionally require `bucket.manage`, and
credential material can require `credentials.manage`. Built-in Admin and Owner
workspace roles provide the activation permissions, while Builder and Viewer
do not.

If lookup or Registry fallback reports a permission denial, stop before writing
the config. If declarative plan/apply or scoped `--apply` reports a denial,
preserve the local draft and report any already-committed sibling activations.
Report the missing permission and resource; do not describe a local YAML edit as
successful activation. Never self-grant, switch credentials, broaden
scope, or retry with guessed authority. An authorised administrator can use the
narrow service/bucket grant or, when the missing permission is workspace-level,
assign a suitable workspace role:

```shell
fused-cli team access service grant <team> <service> manage
fused-cli team access bucket grant <team> <bucket> manage
fused-cli team access workspace set <team> admin
```

Do not run those access-changing commands unless the user explicitly asks and
the current identity is authorised. A scoped `service.manage` grant does not by
itself provide the workspace-level `workspace.update` needed to apply
activation. Read the `fused-cli` skill's
`reference/access-management.md` for the complete permission and role matrix.

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
- Provider auth requirements and server-variable declarations remain on the
  Registry service/operation contract; sync does not copy them into
  `workspace.yaml` or turn them into credentials. Connection-profile snapshots
  remain opaque transport maps, so Engine-defined routing bindings round-trip
  without the CLI normalizing their meaning or inventing a missing default.
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
- Registry-sourced profile attachments sync back as `profile_id`, while an
  inline credential-free profile remains inline. Sync never projects bucket
  credentials or SDK/MCP auth selections into `workspace.yaml` (see
  `fused-config`).
