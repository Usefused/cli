# Connection profiles

A connection profile declares credential-free OAuth/OIDC discovery and routing
behavior, including how a selected provider resource's fields bind into a
request. It may come from the imported OpenAPI/Postman contract or a workspace
service-version `connection_profiles` attachment. Bucket credentials and
SDK/MCP auth references are separate concerns.

## Auth vs. connect vs. profile

Three related but distinct things use different owners:

- SDK/MCP `services.<target>.auth` selects the target scheme. `type` selects the
  public credential type; `name` selects the exact Registry-declared scheme.
  OAuth/OIDC targets may set a complete-pair `ref` to application credentials
  stored for another service in the same selected app bucket:

  ```yaml
  auth:
    type: oauth
    name: calendarOAuth
    ref: "${bucket.auth.gmail.gmailOAuth}"
  connect:
    scopes: ["https://www.googleapis.com/auth/calendar.readonly"]
  ```

  The source service need not be selected by the app, but it must be enabled in
  the workspace with that named pair stored in the selected bucket. Engine
  validates the exact source scheme, completeness, compatibility, and
  non-chaining invariant.
  Credential fields depend on the selected type:

  | `type` | required bucket fields |
  |---|---|
  | `basic` | `username`, `password` |
  | `api_key` | `api_key` |
  | `mtls` | `cert`, `key` |
  | `bearer` | `token` |
  | `oauth` / `oidc` | application `client_id`/`client_secret` pair plus an interactive connected-user grant |

  Credential values themselves belong in a bucket secret, never in workspace
  or app YAML (see `fused-bucket`).
- SDK/MCP `services.<target>.connect.scopes` is the service-specific consent
  ceiling for the interactive per-user OAuth/OIDC flow. Store the application's
  complete `client_id`/`client_secret` pair atomically with `fused-cli secret
  set <service-slug> --bucket <bucket> --type oauth|oidc --auth-name <scheme>
  --value-stdin|--interactive`. The pair uses deterministic encrypted bucket
  secret rows and is distinct from connected-user access, refresh, and ID
  tokens. Blank, partial, extra, token, and `redirect_uri` fields are rejected.

  Engine derives the callback from its validated canonical public URL and
  persists it with the consent session; neither credential input nor an HTTP
  Host header can select the redirect. A target service may reuse a compatible
  source application through `${bucket.auth.<source-service>.<authName>}`.
  Consent, callback exchange, managed refresh, SDK readiness, and MCP readiness
  use that same exact resolver.

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
- A connection profile is the fuller `resource_discovery`/`resource_input`/
  `metadata`/`bindings` rule set below, for OAuth/OIDC services where one token
  can reach several sites/shops/portals/accounts. Its `auth_type` and
  `auth_name` identify provider contract metadata; they are not credentials or
  an app bucket selection.

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
