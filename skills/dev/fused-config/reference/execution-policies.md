# Execution policy

`execution_policy`, nested under a workspace service (as the default for
every version) or under one of its own `versions[]` entries (an override for
just that version). Controls rate limiting, retries, pagination, and
outbound webhook verification -- and, as of the local-override work, always
has a real local effect in this workspace the moment it's applied, whether
or not `public` is set:

```yaml
execution_policy:
  public: false
  rate_limit:
    version: 2
    policies:
      - name: minute_requests
        unit: requests              # requests | points | quota_units
        scope: service_version      # service_version | connection
        default_cost: 1
        operation_costs: {}
        algorithm: fixed_window     # exactly one matching algorithm block
        fixed_window:
          limit: 300
          duration_ms: 60000
    retry_after:
      enabled: true
      max_delay_ms: 60000
  retry:
    strategy: "exponential_backoff"
    max_retries: 3
    backoff_ms: 500
  pagination:
    version: 2
    type: cursor                    # exactly one matching strategy block
    cursor:
      request: {location: query, name: cursor}
      initial: {type: string, string: first} # optional; integer is also supported
      next: {location: body, path: "$.metadata.next_cursor", value_type: string}
      has_more: {location: body, path: "$.metadata.has_more", value_type: string}
    items_path: "$.items"
    limits:
      max_pages: 100
      max_items: 10000
      max_bytes: 16777216
      max_duration_ms: 120000
  base_url: "https://api.example.com/v2"
  event_extraction_path: "body.eventType"
  incoming_webhook_config:
    auth_type: "hmac_sha256"
    signature_header: "X-Signature"
    verification_headers: ["X-Timestamp"]
```

Rate limiting is a discriminated v2 transport contract. Each named policy has
one unit and scope, a default cost plus optional stable-operation-key cost
overrides, and exactly one algorithm block: `fixed_window` (`limit`,
`duration_ms`) or `token_bucket` (`capacity`, `refill_units`,
`refill_interval_ms`). Optional response-header metadata can
name limit/remaining/reset headers and the reset format. There is no legacy
`strategy`/`requests_per_second`/`requests_per_minute` workspace shape.

When these policies come from OpenAPI, exact `x-fused-rate-limit`,
`x-fused-retry`, and root or operation-level `x-fused-pagination` extensions
are strict executable contracts. Unknown fields or invalid values reject the
import with a bounded policy/operation error; Registry never silently drops a
malformed exact policy. Vendor and fuzzy extensions remain inert evidence
until curation translates them into an exact Fused contract.

The CLI strictly preserves this shape but does not interpret its algorithms.
Engine validates the policy and is the sole enforcement authority. Generated
TypeScript and Python SDKs contain no local token bucket, window, sleep, or
acquire step, avoiding per-process counters that disagree with shared
connection and service-version quotas.

Pagination is a discriminated v2 contract. `type` is one of `cursor`,
`offset`, `page_number`, or `next_url`, and exactly one same-named block is
present. Request targets contain `location` and `name`. Response value sources
use `location` (`body`, `header`, or `link`) plus the applicable `path`, `name`,
or `relation`, and declare `value_type`. Offset supports `start`, a
`fixed`/`items_returned` increment, optional page size, next-offset/total/has-more
signals, and short-page stopping. Page-number supports start/increment, optional
page size, total-pages/has-more signals, and short-page stopping. Next-URL has a
single `next` value source. `items_path` and all four safety limits are required.
There is no legacy `request_param`/`response_path` workspace shape.

Do not repeat pagination in SDK or MCP config. Engine resolves endpoint policy
first, then the effective version/service fallback, and streams every successful
provider page as its own chunk to generated SDK and MCP consumers.

`retry_config` is also accepted as an alias for `retry` (same shape); if
both are set, `retry` wins -- it's the one the Registry publish path
actually reads, so keeping the same precedence locally means the value that
publishes is also the value that takes effect.

`base_url` overrides a wrong or missing spec-derived base URL for this
service (or, on one `versions[]` entry's own `execution_policy`, just that
version) -- it *is*
workspace-settable via `execution_policy`, both locally and, with
`public: true`, as a value every other consumer of the service inherits
too. This is a plain string, not published/local in some restricted sense:
the same two-tier behavior (local effect always, publish only with
`public: true` and only for the owning account) applies to it exactly like
`rate_limit`/`retry`/`pagination` below. It's a separate value from
whatever the original OpenAPI/Postman/AsyncAPI spec declared as the
server URL -- that spec-derived value stays intact and inspectable on its
own; this is purely the override layered on top when the spec was wrong or
silent. There's no equivalent field for `default_headers` -- it has no
owner-editable path in workspace.yaml at all today, under `execution_policy`
or anywhere else; it's still Registry/import-derived only (see
`fused-workspace`).

`event_extraction_path` and `incoming_webhook_config` describe *this
service's own* outbound webhook signing/verification recipe (how the
provider signs events it sends) -- not this workspace's own webhook
*registrations*, which are their own `kind: webhook` config files (see
`fused-webhook`) and are never published. `incoming_webhook_config` never
carries a secret, only the verification mechanism (`auth_type`,
`auth_location`, `auth_key_name`, `signature_header`,
`verification_headers`).

## Local effect vs. publishing to the Registry -- two independent things

Every field above takes effect **locally, in this workspace, immediately on
apply** -- regardless of `public`, and regardless of whether this workspace
owns the service. This mirrors how a workspace connection profile override
works: declare it, it's enforced for this workspace's own dispatch and proxy
traffic (SDK calls and the HTTP execution path both read it), and -- for
`event_extraction_path`/`incoming_webhook_config` specifically -- real
inbound webhook verification at this workspace's own registered slugs too.
`rate_limit`/`retry`/`pagination`/`base_url` are resolved per request at
dispatch time; the two webhook fields are resolved once per `apply` (a local
override still wins over the Registry-sourced value there, just captured at
apply time instead of read per inbound request, since ingress denormalizes
this onto the registration once rather than re-resolving it on every
delivery). A connection-resource-forced dynamic binding
(`${resource.base_url}`, see `connection-profiles.md`) still wins over a
`base_url` override at dispatch time -- it's the more specific, per-connection
value; `execution_policy.base_url` is the static fallback underneath it, not
a way to override a live connection's routing.

`public: true` is a *separate*, additional action: it also publishes
`rate_limit`/`retry`/`pagination`/`base_url`/`event_extraction_path`/
`incoming_webhook_config` to the Registry via `UpdateServiceConfig`, so every
other SDK/MCP consumer of the service inherits these same provider-declared
values -- not just this workspace. **Only the owning account can set
`public: true`; the Engine rejects it from a non-owner at apply.** This is a
distinct concept from a *service's* own top-level `public` (Registry
visibility of the service page itself, set via `updateServicePublic` -- see
`fused-workspace`) even though both are named `public` and both only work
for the owning account.

Every other workspace (a separate Engine deployment) that inherits this
published value (has no local override at that service/version) gets a
`registry_execution_policy_changed` notification the next time Engine's
background poller checks -- see `fused-notifications` for what that means
and where it shows up.

So a non-owner (or an owner who just hasn't published yet) declaring
`execution_policy` without `public` is not a no-op -- it's the normal case
for "enforce this locally, don't affect anyone else."

## `reset: true`

Clears this workspace's **local** override for the tier back to whatever the
Registry-sourced snapshot provides -- nothing more. It does not touch
anything published to the Registry (there's no equivalent "unpublish"; a
prior `public: true` publish stays published until superseded by another
publish). `reset` has no documented restriction against combining it with
other fields in the same block, but there's no reason to -- pair it with
nothing else.

A `versions[]` entry's own `execution_policy` lets that specific version
override the service-level policy, resolved independently at both the
local-effect and publish layers (a version-tier local override wins over the
service-default local override for that version; same precedence, unrelated
mechanism, on the publish side). This replaced the old sibling
`version_policies` list -- the override now nests directly on the version
entry it applies to instead of being declared in a separate list keyed by a
repeated `version` string:

```yaml
versions:
  - version: "v2"
    execution_policy:
      rate_limit: {...}
```
