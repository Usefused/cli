---
name: fused-workspace
description: "Use when the user wants to configure a Fused workspace's service allowlist using fused-cli -- enabling/disabling services or versions, editing runtime_config (base_url, headers, pagination, webhooks), or scheduling a deprecation. Trigger on 'workspace config', 'enable a service', 'fused-cli workspace', 'deprecate a service version', or 'kind: workspace' files. For rate limits/retries or auth/connect/dynamic-binding config nested inside a workspace file, read fused-config instead; for bucket credentials read fused-bucket."
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
    execution_policy: {...}      # see fused-config skill
    runtime_config:
      base_url: "https://api.example.com"
      default_headers: {...}
      auth: {...}                # see fused-config skill
      connect: {...}             # see fused-config skill
      webhooks: {...}
      pagination: {...}
      pagination_overrides: {...}
    connection_profiles: [...]   # raw, Engine-validated -- see fused-config skill
buckets:
  <bucket-name>: {...}           # see fused-bucket skill
deprecations:
  - service_id: "..."
    effective_at: "YYYY-MM-DD"
    reason: "..."
```

`auth`/`connect` under a service's own `runtime_config` is rejected by
validation -- that credential material moved to
`buckets.<bucket>.service_config.<slug>` (see `fused-bucket`).

`public` is Registry visibility of the service page itself (via
`updateServicePublic`) -- `true` makes it visible to every Registry
consumer, `false` keeps it private to this account. Only the owning account
can set it; it's omitted entirely for a third-party service you don't own.
Don't confuse this with `execution_policy.public`, a different owner-only
toggle that publishes *rate limit/retry* settings to the Registry instead
(see `fused-config`).

## Commands

```shell
fused-cli workspace plan
fused-cli workspace apply
fused-cli workspace services list
fused-cli workspace service <slug> versions
fused-cli workspace service <slug> operations
fused-cli workspace service <slug> webhooks
fused-cli workspace service <slug> add --version <v>
fused-cli workspace service <slug> remove
fused-cli workspace service <slug> deprecate --effective-at <date> --reason "..."
fused-cli workspace service <slug> version <v>
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

`fused-cli workspace sync` pulls the Engine's current state back into a
local file. It preserves any `runtime_config.connect` block that has no
remote projection (a workspace-local profile isn't erased just because
Registry has nothing to report), and writes Registry-sourced profile
attachments as `profile_id` vs. workspace-local ones as an inline `profile`.
