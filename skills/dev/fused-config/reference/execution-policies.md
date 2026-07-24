# Execution policy

`ExecutionPolicy`, nested under a service or a specific `version_policies`
entry in a workspace config. Controls rate limiting and retries:

```yaml
execution_policy:
  public: false
  rate_limit:
    strategy: "fixed_window"       # engine-defined strategies
    requests_per_second: 10
    requests_per_minute: 300
  retry:
    strategy: "exponential_backoff"
    max_retries: 3
    backoff_ms: 500
```

`public`, when `true`, publishes this service's `rate_limit`/`retry` to the
Registry via `UpdateServiceConfig` so every SDK/MCP consumer of the service
inherits these same provider-declared limits — not just this workspace.
Only the owning account can set it; the Engine rejects it from a non-owner
at apply. This is a distinct concept from a *service's* own top-level
`public` (Registry visibility of the service page itself, set via
`updateServicePublic` — see `fused-workspace`) even though both are named
`public` and both only work for the owning account.

Set `reset: true` to clear an inherited policy back to defaults on apply.
`reset` cannot be combined with `public`, `rate_limit`, or `retry` in the
same block — it's all-or-nothing.

`version_policies` lets a specific version override the service-level
policy:

```yaml
version_policies:
  - version: "v2"
    execution_policy:
      rate_limit: {...}
```
