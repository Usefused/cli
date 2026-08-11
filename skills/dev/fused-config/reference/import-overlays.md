# Import overlays

Use an import overlay only for facts the provider source omits. Provider-standard
semantics and exact `x-fused-*` metadata are stronger; Registry rejects a
conflicting overlay instead of silently overriding the source. An identical
value is an idempotent no-op.

The v1 document is strict: unknown or duplicate fields, multiple YAML
documents, aliases, credential literals, and unsupported URLs or policies are
rejected. The top-level shape is:

```yaml
schema_version: 1
declared_version: v1       # optional
base_url: https://api.example.com
servers: []                # optional canonical Fused Server objects
auth_configs:              # optional named, credential-free scheme corrections
  - name: basicAuth
    basic_password_mode: empty
  - name: browserOAuth
    type: oauth2
    flow: authorizationCode
    authorization_url: https://auth.example.com/authorize
    pkce_required: true
    scopes_delimiter: comma
    extra_auth_params: {prompt: consent}
    extra_token_params: {audience: payments}
    refresh_token_rotates: true
security_requirements:     # optional service default: ordered alternatives (OR)
  - schemes:               # every scheme in one alternative is required (AND)
      - scheme: basicAuth  # exact auth_configs[].name
        scopes: []
rate_limit:                # optional existing Fused policy shape
  version: 2
  policies:
    - name: request_burst
      unit: requests
      scope: service_version
      default_cost: 1
      operation_costs: {}
      algorithm: token_bucket
      token_bucket: {capacity: 10, refill_units: 2, refill_interval_ms: 1000}
retry:                     # optional existing Fused retry shape
  strategy: exponential
  max_retries: 3
  backoff_ms: 250
pagination:                # optional service default
  version: 2
  type: cursor
  cursor:
    request: {location: query, name: cursor}
    next: {location: body, path: "$.next_cursor", value_type: string}
  items_path: "$.items"
  limits: {max_pages: 100, max_items: 10000, max_bytes: 16777216, max_duration_ms: 120000}
connect: {}                # optional credential-free connection profile
operations:
  - operation_id: listWidgets
    # Or use the complete method + path pair. If both forms are supplied,
    # they must identify the same single endpoint.
    request_media_type: application/json
    security_requirements:
      - schemes: []        # explicit anonymous alternative
    pagination:
      version: 2
      type: cursor
      cursor:
        request: {location: query, name: cursor}
        next: {location: body, path: "$.next_cursor", value_type: string}
      items_path: "$.items"
      limits: {max_pages: 100, max_items: 10000, max_bytes: 16777216, max_duration_ms: 120000}
```

Run `fused-cli import plan <spec> --overlay <local-file> ...`. The CLI sends
the file bytes without interpreting them. Registry validates and canonicalizes
the overlay, pins the detected source format and adapter version, and returns
an opaque `review_hash`. Apply uses only the receipt's `plan_id` and
`review_hash`; it does not reread the spec or overlay. Source and overlay
hashes are audit information, never substitutes for the combined review hash.

Never place tokens, passwords, signing secrets, default authorization headers,
or literal connection binding values in an overlay. Put credential material in
a bucket secret and keep overlays limited to contract metadata.

Security scheme references must exactly match a unique `auth_configs[].name`;
unknown or duplicate names are rejected. Standard OpenAPI security is stronger
than exact `x-fused-security-requirements`, which is stronger than the overlay.
For HTTP Basic, an omitted mode is defaulted to `required` only after extensions
and overlay corrections are applied, so `empty` is a deliberate credential
shape rather than a password value.

OAuth2 token requests use the security-scheme field
`token_endpoint_auth_method`. Its only values are `client_secret_basic`
(HTTP Basic, with neither client credential repeated in the form) and
`client_secret_post` (both credentials in the form, without an Authorization
header). OpenAPI writes this as the strict
`x-fused-token-endpoint-auth-method` extension. A blank OAuth2 method defaults
to `client_secret_post` only after overlay merging; non-OAuth schemes, legacy
`basic`/`body` values, and unknown methods are rejected.

The remaining exact security-scheme extensions are
`x-fused-pkce-required`, `x-fused-scopes-delimiter` (`space` or `comma`),
`x-fused-extra-auth-params`, `x-fused-extra-token-params`, and
`x-fused-refresh-token-rotates`. Their overlay field names omit the
`x-fused-` prefix. Parameter maps are bounded fixed public strings; credential-
shaped names or references and Engine-owned fields such as `client_id`,
`client_secret`, `grant_type`, `code`, `redirect_uri`, `state`, `scope`, and
PKCE fields are rejected. PKCE, authorization parameters, and refresh-token
rotation require an `authorizationCode` flow with `authorization_url`; include
that public context in an overlay rather than relying on a sparse inference.

Templated server URLs remain templated. Each `{placeholder}` must have exactly
one matching `variables` entry with `name`, optional `default`, optional `enum`,
and explicit `required`; variable definitions that are absent from the URL are
rejected. Do not resolve tenant values into the overlay.

Registry rollout is exact by source format. `import_rollout.writer_formats` is
an allowlist, so a disabled format fails before an import plan is persisted.
`shadow_formats` replays the canonical execution summary for deterministic
adapters; documentation extraction uses its reviewed canonical validator result
as the shadow boundary and never invokes the model a second time. Capability
metrics contain only bounded source format, fidelity, and diagnostic-code
dimensions—never source text, provider URLs, hashes, operation names, or model
responses.
