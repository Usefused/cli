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
    version: 3
    policies:
      - name: minute_requests
        mode: enforce               # enforce | observe
        unit: requests              # requests | points | complexity | quota_units
        identity:
          inputs: [{kind: connection}]
        cost:
          default: 1
          rules: []
        algorithm: fixed_window
        fixed_window:
          limit: 300
          duration_ms: 60000
        response_signals:
          remaining: {source: header, name: X-RateLimit-Remaining}
          reset: {signal: {source: header, name: X-RateLimit-Reset}, format: unix_seconds}
    cooldown:
      statuses: [{min: 429, max: 429}]
      headers: [{name: Retry-After, formats: [delta_seconds, http_date], max_delay_ms: 60000}]
  retry:
    version: 3
    rules:
      - predicates:
          methods: [POST]
          operation_kinds: [write]
          statuses: [{min: 429, max: 429}, {min: 500, max: 599}]
          errors: [connect_timeout, connection_reset]
          body_replayability: replayable
          idempotency_key: {requirement: required, header: Idempotency-Key}
          required_provider_headers: []
        action:
          max_attempts: 3
          max_elapsed_ms: 30000
          backoff: {strategy: exponential, base_delay_ms: 250, max_delay_ms: 5000, jitter_ms: 100}
          retry_after_headers: [{name: Retry-After, formats: [delta_seconds, http_date], max_delay_ms: 10000}]
  pagination:
    version: 3
    request:
      - {state: cursor, target: {location: query, name: cursor}, value_type: string, apply: subsequent}
    response:
      items: {path: "$.items"}
      values:
        - {name: next_cursor, source: {location: body, path: "$.metadata.next_cursor", value_type: string}}
    continuation:
      - {kind: token, state: cursor, response_value: next_cursor}
    termination:
      stop_on_empty_items: true
      stop_on_missing_values: [next_cursor]
      repeated_value: error
    limits:
      max_pages: 100
      max_items: 10000
      max_bytes: 16777216
      max_duration_ms: 120000
  base_url: "https://api.example.com/v2"
  server_variables:
    tenant: acme
    region: eu1
  event_extraction_path: "body.eventType"
  incoming_webhook_config:
    auth_type: "hmac_sha256"
    signature_header: "X-Signature"
    verification_headers: ["X-Timestamp"]
```

Quota and concurrency v3 is an ordered set of simultaneous dimensions. Every
dimension declares `enforce` or observability-only `observe`, its unit, explicit
bucket-identity inputs, default/operation costs, and exactly one fixed-window,
rolling-window, token-bucket, or concurrency branch. Response signals may read
limit, remaining, reset, or cost from bounded headers/body paths. Config-level
cooldown is status/header driven and bounded. Retry v3 evaluates ordered rules:
values within one predicate field are ORed, fields are ANDed, and actions bound
attempts, elapsed time, backoff, jitter, and accepted provider retry headers.
Writes require explicit replayability/idempotency predicates; never infer safety
from a provider or method name alone.

When these policies come from OpenAPI, exact `x-fused-rate-limit`,
`x-fused-retry`, and root or operation-level `x-fused-pagination` extensions
are strict executable contracts. Unknown fields or invalid values reject the
import with a bounded policy/operation error; Registry never silently drops a
malformed exact policy. Vendor and fuzzy extensions remain inert evidence
until curation translates them into an exact Fused contract.

The CLI strictly preserves this shape but does not interpret it. Engine is the
sole enforcement, coordination, and provider-retry authority. Generated
TypeScript and Python SDKs contain no local window, bucket, semaphore, retry
loop, jitter, sleep, or acquire step.

Pagination v3 is an ordered request/response/continuation contract. Request
steps bind typed state or constants to query, header, body, or GraphQL-variable
targets and may apply to all, first, or subsequent pages. Response steps select
items through one path or request-dependent conditional paths and extract named
values from bodies, headers, RFC Links, GraphQL results, or a selected last
item. Continuations compose token, offset, page, RFC Link, and next-URL state;
GraphQL policies may also pin result aliases and distinct first/subsequent page
templates. Always declare termination behavior, repeated-value handling, and
all four hard limits. Next URLs require an explicit same-origin or allowlist
policy because a provider response must not redirect Engine toward an
unreviewed credential-bearing origin. All live config and import ingress accepts
only the canonical v3 policy shape; older strategy objects are rejected.

Do not repeat pagination in SDK or MCP config. Engine resolves endpoint policy
first, then the effective version/service fallback, and streams every successful
provider page as its own chunk to generated SDK and MCP consumers.

`retry_config` is also accepted as an ingress spelling for `retry` with the
same canonical v3 shape. Setting both is rejected so one policy cannot mask a
different policy during apply.

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

`server_variables` binds imported OpenAPI server-template variables for this
workspace. Keys must name variables declared on the effective operation server
(operation, then path, then document root); values use URL-unreserved characters
only. Engine owns enum, host, and final URL validation because those checks need
the selected operation's contract. These bindings are deliberately independent
of provider authentication and remain local even when `public: true`: CLI never
publishes tenant routing values to Registry, and `workspace sync` preserves the
local map. A forced `${resource.base_url}` connection binding remains the more
specific routing decision.

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
`rate_limit`/`retry`/`pagination`/`base_url`/`server_variables` are resolved per request at
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

`server_variables` is the exception to publishing: it is Engine-local routing
configuration and is never included in `UpdateServiceConfig`.

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
