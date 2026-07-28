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
  call for this service. Which fields it needs depends on `auth_type`:

  | `auth_type` | required fields |
  |---|---|
  | `basic` | `username`, `password` |
  | `api_key` | `api_key` |
  | `mtls` | `cert`, `key` |
  | `bearer` | `token` |
  | `oauth` / `oidc` | `token` — a *pre-obtained* static token, not the interactive flow below |

  Credential values themselves belong in a bucket secret (see
  `fused-bucket`), not inline here.
- `connect` (`ConnectConfig`) — the interactive per-user OAuth/OIDC flow:
  app registration (`client_id`/`client_secret`) plus `redirect_uri`. Only
  `auth_type: oauth` or `oidc` are valid here — `basic`/`api_key`/`bearer`/
  `mtls` credentials don't have a browser consent step, so they only ever go
  through `auth` above. `client_secret` must be `${bucket.secret.<key>}` —
  set the value once as a bucket secret (`fused-cli secret <service-slug>
  set` -- see `fused-bucket`) and reference it here; Engine resolves it
  server-side at apply time against this connect config's own bucket, the
  same as `auth` credentials, so nothing needs to be re-supplied on every
  apply. The named-bucket form (`${bucket.<name>.secret.<key>}`) is rejected
  here -- a connect config already belongs to one specific bucket, so naming
  a different one would only ever mean reading a secret out of the wrong
  bucket. Start an actual user session with `fused-cli workspace service
  <slug> connect --user-ref <ref>` (see `fused-bucket`).
- `profile` — the fuller `resource_discovery`/`resource_input`/`metadata`/
  `bindings` rule set below, for OAuth/OIDC services where one token can
  reach several sites/shops/portals/accounts.

This same `basic | api_key | mtls | bearer | oauth | oidc` vocabulary is also
what `auth.type` in an SDK/MCP artifact config accepts (see `fused-sdk` /
`fused-mcp`) — it's one shared type list across every layer, not a
per-context set.

## Canonical profile shape

```yaml
auth_type: oauth
resource_discovery:
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

`resource_discovery.operation_id` must be a `GET` on the pinned service
version; `server` names an imported server environment, not an arbitrary
URL. The portable JSONPath subset is `$.field`, `$.nested.field`,
`$[*].field`, `$[*].nested.field`. `resource_input` (fields +
`base_url_template`) is the alternative for services with no discovery
endpoint — the caller supplies the routing value (e.g. a Zendesk subdomain)
instead.

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
value (`fused-cli value ... set` -- see `fused-bucket`) instead of writing it
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
  service/version/auth_type. An override takes precedence over a baseline
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
`profile_mode: detach`, which can't be combined with `profile`/`profile_id`.

Registry-level baseline publish: `fused-cli connection-profile set
<service-ref> --version <v> --auth-type <type> --file <path>` (owner/curator
only — see `fused-bucket`). Every other workspace (a separate Engine
deployment) still on the `baseline` layer for that service/version/auth_type
gets a `registry_connection_profile_changed` notification the next time
Engine's background poller checks — see `fused-notifications`.
