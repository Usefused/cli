# Connection profiles

A connection profile declares how a service authenticates and, for
OAuth/OIDC services that front multiple tenants, how a selected provider
resource's fields get bound into a request. It's the same canonical shape
whether it comes from a workspace config, a bucket, or an OpenAPI/Postman
document (see `reference/openapi-postman.md`).

## Auth vs. connect vs. profile

Three related but distinct things, all under a bucket's
`service_config.<slug>` (see `fused-bucket`):

- `auth` (`AuthConfig`) — a static credential the Engine attaches to every
  call for this service. `auth_type` selects the public credential type; optional
  `auth_name` selects the exact Registry-declared scheme when the provider has
  more than one scheme of that type. Never omit `auth_name` in that case,
  because choosing the first same-type scheme is intentionally unsupported.
  Which credential fields it needs depends on `auth_type`:

  | `auth_type` | required fields |
  |---|---|
  | `basic` | `username`, `password` |
  | `api_key` | `api_key` |
  | `mtls` | `cert`, `key` |
  | `bearer` | `token` |
  | `oauth` / `oidc` | `token` — a *pre-obtained* static token, not the interactive flow below |

  Credential values themselves belong in a bucket secret (see
  `fused-bucket`), not inline here.
- `connect` — the interactive per-user OAuth/OIDC flow: app registration
  (`client_id`/`client_secret`) plus `redirect_uri`. Only `auth_type: oauth`
  or `oidc` are valid here — `basic`/`api_key`/`bearer`/`mtls` credentials
  don't have a browser consent step, so they only ever go through `auth`
  above.

  Registered via `fused-cli connect set <service-slug>` only (see
  `fused-bucket`) — there is no workspace.yaml field for it. This registers
  the app registration directly against the Engine admin endpoint — no
  plan/apply, immediate effect, the same "upsert with no separate apply
  step" category as `secret set`/`value set`. It is also *not* the same
  storage as `auth`'s bucket secrets: `auth` credentials and `connect`'s
  client_id/client_secret used to be reachable through the same `fused-cli
  secret set <service-slug>` command and the same derived key, which meant a
  static credential and an app registration could silently overwrite each
  other if both were ever configured for the same service+bucket. `connect
  set` writes to its own dedicated storage instead, so that collision cannot
  happen. It also supports partial updates — e.g. `printf '%s'
  'redirect_uri=https://...' | fused-cli connect set jira --bucket <bucket>
  --value-stdin` rotates only `redirect_uri` without resupplying
  `client_id`/`client_secret`, since the admin API never returns decrypted
  values for a caller to resend anyway. Credential values are never accepted
  in argv, so the whole registration goes over stdin as one `;`-delimited
  value or through `--interactive`; a key present but blank is rejected as an
  attempt to erase a credential, while an omitted key means leave-as-is. A
  declarative workspace.yaml `connect:` block used to exist alongside this
  command but was removed —
  it always required every field on every apply, which `connect set`'s
  partial-update support made strictly worse, and having two ways to
  register the same app was the exact kind of duplicated decision-making
  workspace.yaml is meant to avoid.

  Registering the app is separate from any one user connecting: start an
  actual user session with `fused-cli workspace service connect <slug>
  --user-ref <end-user-reference>` (see `fused-bucket`).

  The imported OAuth2 security scheme also determines how Engine authenticates
  those app credentials at the token endpoint. `client_secret_basic` uses HTTP
  Basic and keeps both credentials out of the form; `client_secret_post` puts
  both in the form and sends no Authorization header. Engine follows that
  reviewed contract for both authorization-code exchange and refresh and
  rejects an absent or unknown runtime method before contacting the provider.
  Token request media is independent of client authentication:
  `application/x-www-form-urlencoded` is the default, while
  `application/json` must be declared by the imported
  `x-fused-token-request-media-type` extension. JSON exchange requires Engine's
  `auth.oauth2.token_request_media.v1` execution capability; an Engine missing
  it rejects the service contract instead of attempting a differently encoded
  token request.
  The same imported public policy can require PKCE, join requested scopes with
  a comma instead of a space, add reviewed fixed authorization/token
  parameters, and declare refresh-token rotation. Engine always owns and
  overwrites state, client credentials, redirect URI, grant/code/refresh
  values, scopes, nonce, and PKCE fields; imported maps can never supply those
  fields or secret references.
- `profile` — the fuller `resource_discovery`/`resource_input`/`metadata`/
  `bindings` rule set below, for OAuth/OIDC services where one token can
  reach several sites/shops/portals/accounts.

This same `basic | api_key | mtls | bearer | oauth | oidc` vocabulary is also
what `auth.type` in an SDK or MCP server config accepts (see `fused-sdk` /
`fused-mcp`) — it's one shared type list across every layer, not a
per-context set.

## Canonical profile shape

```yaml
auth_type: oauth
auth_name: primaryOAuth # required when another named OAuth scheme exists
oauth2_flow: authorizationCode # required when the scheme declares several flows
resource_discovery:
  version: 1
  stage: post_auth
  operation_id: getAccessibleResources
  server: api
  id_path: "$[*].id"
  name_path: "$[*].name"
  base_url_path: "$[*].url"
  scopes_path: "$[*].scopes"
  resource_type: jira_site
  auto_run: after_oauth_callback
  lifecycle: authoritative
  allowed_hosts:
    - api.atlassian.com
metadata:
  account_id: "$[*].accountId"
bindings:
  - value: "${resource.base_url}"
    location: base_url
    mode: force
  - value: "${resource.metadata.account_id}"
    location: header
    name: X-Account-ID
    mode: force
    operations: [getAccount, listAccountUsers]
```

`oauth2_flow` is one of `implicit`, `password`, `clientCredentials`, or
`authorizationCode` and selects a flow already declared by that exact Registry
scheme. It carries no client secret, token, or signing material. Omit it only
when the scheme has zero or one unambiguous flow; Engine rejects a missing or
unknown selection for a multi-flow scheme instead of choosing a preferred flow.

`resource_discovery.version` is `1` and `stage` is `post_auth`; Registry may
normalize older omitted ingress values, but new config should always state both.
Discovery is an Engine-owned OAuth callback workflow, not an SDK/MCP method:
generated clients select an opaque `resourceId` after discovery and never call
the discovery operation or parse its provider response themselves.

`resource_discovery.operation_id` must be a `GET` on the pinned service
version; `server` names an imported server environment, not an arbitrary
URL. The portable JSONPath subset is `$.field`, `$.nested.field`,
`$[*].field`, `$[*].nested.field`. `resource_input` (fields +
`base_url_template`) is the alternative for services with no discovery
endpoint — the caller supplies the routing value (e.g. a Zendesk subdomain)
instead.

### Typed resource input

Resource input fields collect non-secret strings only. `type` accepts
`text` or `select`; an omitted type means `text`:

```yaml
resource_input:
  fields:
    - name: subdomain
      label: Zendesk subdomain
      type: text
      placeholder: acme
      description: Enter the part before .zendesk.com.
      required: true
      pattern: "^[a-z0-9-]+$"
    - name: region
      label: Data region
      type: select
      description: Choose the provider region for this account.
      options:
        - value: eu
          label: Europe
        - value: us
          label: United States
  base_url_template: "https://{subdomain}.{region}.zendesk.example.com"
  resource_type: zendesk_subdomain
  allowed_hosts:
    - "*.eu.zendesk.example.com"
    - "*.us.zendesk.example.com"
```

`placeholder` is display-only and never supplies a missing value.
`description` is safe help copy. Each select option has an exact string
`value` plus an optional display `label`; Engine rejects a submitted value
outside that declared option allowlist. Use `options` only for select fields.
Text `pattern` uses server-side RE2 and is never copied to HTML `pattern`.

Do not model passwords, secrets, numbers, booleans, or browser URL controls
through resource input. Provider credentials belong in the selected bucket.
Every resource-input value remains a string constrained by the profile,
template, and allowed hosts.

`auto_run` and `lifecycle` are effectively fixed-value fields today —
`after_oauth_callback` and `authoritative` are the *only* values currently
accepted (anything else, including omitting them, either normalizes to
those or is rejected outright). Don't invent other values for these; they
read as extensibility points for a future discovery mode, not a menu of
options that already exist.

## Dynamic value injection

This is how a selected connection resource's fields actually reach a
request. `bindings[].value` is a small closed grammar, not a general
template language — the whole value must be one expression:

```yaml
value: "2026-01"                            # literal
value: "${resource.provider_resource_id}"   # the selected resource's Fused ID
value: "${resource.base_url}"               # its routing URL
value: "${resource.metadata.portal_id}"     # a declared metadata key
```

For a literal sourced dynamically rather than hardcoded, set it as a bucket
value (`fused-cli value set ...` -- see `fused-bucket`) instead of writing it
inline here.

`prefix-${resource.base_url}` is rejected — build URLs with
`base_url_template` instead. `location` is one of `base_url`, `header`,
`query`, `path`, or literal-only `body` (dynamic values can't target
`body`). `mode` is `default` or `force`. Runtime precedence: service
defaults → default bindings → SDK parameters → forced literal bindings →
forced resource bindings → provider authentication. Forced query values
replace rather than append; `Authorization`, `Proxy-Authorization`, `Host`,
and other protected/hop-by-hop headers can never be targeted.

## Profile ownership, provenance, and where each lives

Three provenances, matched to who can set them:

- `provider` — the service owner, via `x-fused-connect` on import (or a
  curator on their behalf). Stored in the Registry as an immutable,
  versioned revision (only when `visibility: public`), plus a pinned
  snapshot in the Engine for anything that attaches it.
- `fused` — curated by an allowlisted curator account for someone else's
  service. Same Registry + Engine storage as `provider`.
- `workspace` — defined inline in a workspace config for one bucket. Never
  stored in the Registry — lives only as an Engine-side "override" layer,
  alongside any Registry-sourced "baseline" for the same
  service/version/auth_type/auth_name identity. An override takes precedence over a baseline
  when both exist.

There is no `public`/`visibility`/`provenance`/`scope`/owner field settable
in profile config — the backend rejects any of these if present. Visibility
and provenance come only from which authenticated path wrote the profile.

Selection order when a workspace service doesn't inline a `profile`:

1. An explicit workspace-local profile.
2. An explicitly selected public `profile_id`.
3. The only matching public provider/Fused profile.
4. No profile if zero match; an error requiring explicit selection if
   several match.

Profiles are immutable revisions — a newer provider revision shows up as a
plan change, it never silently changes production routing. Removing profile
fields from a file is non-destructive; detaching is always explicit via
`reset: true`, which can't be combined with `profile`/`profile_id`.

Registry-level baseline publish: `fused-cli service connection-profile set
<service-ref> --version <v> --auth-type <type> --file <path>`. Put the exact
`auth_name` inside the profile file when the type is ambiguous (owner/curator
only — see `fused-bucket`). Every other workspace (a separate Engine
deployment) still on the `baseline` layer for that exact auth identity
gets a `registry_connection_profile_changed` notification the next time
Engine's background poller checks — see `fused-notifications`.
