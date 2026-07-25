# Execution policy

`execution_policy`, nested under a workspace service or a specific
`version_policies` entry. Controls rate limiting, retries, pagination, and
outbound webhook verification -- and, as of the local-override work, always
has a real local effect in this workspace the moment it's applied, whether
or not `public` is set:

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
  pagination:
    type: "cursor"                 # cursor | offset | page_number | next_url
    request_param: "cursor"
    response_path: "metadata.next_cursor"
  event_extraction_path: "body.eventType"
  incoming_webhook_config:
    auth_type: "hmac_sha256"
    signature_header: "X-Signature"
    verification_headers: ["X-Timestamp"]
```

`retry_config` is also accepted as an alias for `retry` (same shape); if
both are set, `retry` wins -- it's the one the Registry publish path
actually reads, so keeping the same precedence locally means the value that
publishes is also the value that takes effect.

`event_extraction_path` and `incoming_webhook_config` describe *this
service's own* outbound webhook signing/verification recipe (how the
provider signs events it sends) -- not this workspace's own webhook
*registrations*, which stay under `runtime_config.webhooks` and are never
published. `incoming_webhook_config` never carries a secret, only the
verification mechanism (`auth_type`, `auth_location`, `auth_key_name`,
`signature_header`, `verification_headers`).

## Local effect vs. publishing to the Registry -- two independent things

Every field above takes effect **locally, in this workspace, immediately on
apply** -- regardless of `public`, and regardless of whether this workspace
owns the service. This mirrors how a workspace connection profile override
works: declare it, it's enforced for this workspace's own dispatch and proxy
traffic (SDK calls and the HTTP execution path both read it), and -- for
`event_extraction_path`/`incoming_webhook_config` specifically -- real
inbound webhook verification at this workspace's own registered slugs too.
`rate_limit`/`retry`/`pagination` are resolved per request at dispatch time;
the two webhook fields are resolved once per `apply` (a local override still
wins over the Registry-sourced value there, just captured at apply time
instead of read per inbound request, since ingress denormalizes this onto
the registration once rather than re-resolving it on every delivery).

`public: true` is a *separate*, additional action: it also publishes
`rate_limit`/`retry`/`pagination`/`event_extraction_path`/
`incoming_webhook_config` to the Registry via `UpdateServiceConfig`, so every
other SDK/MCP consumer of the service inherits these same provider-declared
values -- not just this workspace. **Only the owning account can set
`public: true`; the Engine rejects it from a non-owner at apply.** This is a
distinct concept from a *service's* own top-level `public` (Registry
visibility of the service page itself, set via `updateServicePublic` -- see
`fused-workspace`) even though both are named `public` and both only work
for the owning account.

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

`version_policies` lets a specific version override the service-level
policy, resolved independently at both the local-effect and publish layers
(a version-tier local override wins over the service-default local override
for that version; same precedence, unrelated mechanism, on the publish
side):

```yaml
version_policies:
  - version: "v2"
    execution_policy:
      rate_limit: {...}
```
