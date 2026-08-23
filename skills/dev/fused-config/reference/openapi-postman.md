# Declaring a connection profile in OpenAPI or Postman

The same profile shape from `reference/connection-profiles.md` can be
declared directly in the source document at import time, instead of (or in
addition to) a workspace config. An import by the service owner publishes it
as a new provider revision in the same transaction as the contract.

## OpenAPI

Place `x-fused-connect` at the document root:

```yaml
openapi: 3.0.3
info: { title: Jira, version: "2026-07-01" }
servers:
  - url: https://api.atlassian.com
    x-fused-environment: api
components:
  securitySchemes:
    oauth:
      type: oauth2
      flows:
        authorizationCode:
          authorizationUrl: https://auth.atlassian.com/authorize
          tokenUrl: https://auth.atlassian.com/oauth/token
          scopes: {}
security: [{ oauth: [] }]
x-fused-connect:
  auth_type: oauth
  resource_discovery:
    operation_id: getAccessibleResources
    id_path: "$[*].id"
    name_path: "$[*].name"
    base_url_template: "https://api.atlassian.com/ex/jira/{id}"
    resource_type: jira_site
    allowed_hosts: [api.atlassian.com]
  bindings:
    - value: "${resource.base_url}"
      location: base_url
      mode: force
paths:
  /oauth/token/accessible-resources:
    get:
      operationId: getAccessibleResources
      responses:
        "200": { description: Accessible sites }
```

`servers[].x-fused-environment` names a server so `resource_discovery.server`
can reference it — discovery isn't limited to the default server. Use
`base_url_path` when the discovery response supplies a URL directly, or
`base_url_template` when the URL is built from the resource ID; both require
`allowed_hosts` for dynamic routing.

Registry accepts absolute, templated, protocol-relative, and path-relative
OpenAPI servers. It resolves a path-relative value such as `/wiki` against the
persisted HTTP(S) source-document URL. For uploaded sources, the relative value
is preserved because no authoritative host exists. Protocol-relative values
are also preserved rather than assigned a scheme. Either unresolved form needs
an absolute HTTP(S) workspace `base_url` override or a trusted forced
`${resource.base_url}` binding before execution (HTTP remains limited to
loopback development). This does not relax `allowed_hosts` validation. A
template such as `{protocol}://api.example.com` is validated from its declared
HTTP(S) variable default/enum while the template remains bindable at runtime.

Registry persists the effective server for every operation using OpenAPI
precedence: operation-level `servers`, then path-level, then document-root.
Variable declarations retain their default, enum, description, and required
state; actual tenant values never enter Registry metadata. Workspace execution
uses `execution_policy.server_variables`; an SDK/MCP app can instead use a
validated non-secret `server_variable` injection from its own bucket.

Parameters retain all five locations (`path`, `query`, `querystring`, `header`,
`cookie`) plus
their schema or content form, style, explode, allow-reserved/empty flags,
examples, defaults, and deprecation state. Explicit `false` remains distinct
from an absent option. Generated SDKs expose schema-derived TypeScript/Python
types and pass typed values to Engine; Engine alone applies OpenAPI wire
serialization. Cookie style and location/style-aware `allowReserved` stay
explicit rather than using generated-client URL conventions. Standard methods
such as `CONNECT`, `OPTIONS`, and `TRACE` are
imported as operations rather than dropped.

OpenAPI 3.2 also retains `QUERY` and the exact token of custom
`additionalOperations`, named servers, a single whole-query content parameter,
OAuth device-authorization metadata, response summaries, and sequential or
positional media encodings. Tag summary/parent/kind remains documentation-only.
Generated callers pass the whole-query value once
to Engine and do not apply its media-type serialization or stream framing.
Registry resolves reusable `components.mediaTypes` into the ordinary canonical
representations instead of exposing a second reference shape.
Device Authorization remains non-executable and blocks public admission; its
retained source fields are diagnostic evidence, not a selectable auth flow.

Schemas are retained as canonical raw JSON plus dialect and content hash, beside
one bounded Registry projection and explicit projection diagnostics. Raw keeps
composition, unions/null, constraints, cycles, and absent/false/true/schema
`additionalProperties` states. Request bodies preserve ordered media choices,
their reviewed default, and complete form/multipart property encodings.
Responses preserve exact/default/range status keys, descriptions, headers,
vendor JSON/text/XML/binary representations, examples, and links. CLI and SDK
code must consume the Registry projection rather than parsing raw schema again.

OAuth2 imports retain every declared flow, refresh URL, and scope description;
no preferred flow is inferred when several exist. Reviewed OAuth1 signing and
HTTP challenge behavior use generic strategy objects, and a security alternative
may select an exact declared server for one of its own schemes. Unknown challenge
schemes remain strict diagnostics until a complete execution capability exists.
OAuth2 token requests default to `application/x-www-form-urlencoded`. A scheme
that requires JSON must declare
`x-fused-token-request-media-type: application/json`; only those two exact
values are valid. JSON token exchange adds required execution capability
`auth.oauth2.token_request_media.v1`, so an incompatible Engine rejects the
contract before authorization-code or refresh traffic begins. PKCE remains
independent and is added only when `x-fused-pkce-required: true`.

OpenAPI callbacks retain their runtime expression and parent operation, while
3.1 top-level webhooks retain the same parameter, request, response, server,
and security contract as outbound operations. Response links and their runtime
expressions are preserved but not executed. Safe namespaced `x-*` JSON carries
`source_spec` provenance for documentation; only the exact reviewed
`x-fused-*` allowlist can affect behavior, and strict planning rejects unknown
Fused execution extensions.

## Postman

A nested host such as `https://api-{{app_id}}.sendbird.com` becomes canonical
`https://api-{app_id}.sendbird.com` with required `app_id` and no imported
tenant default. Postman URL variable arrays can mix host and path variables;
only names referenced by the request path become operation parameters. The
host `app_id` remains Engine routing metadata, while a path such as
`/v3/users/{user_id}` exposes only `user_id` to generated SDK methods or MCP
tools. A service-bearing `sdk init`/`mcp init`, including `--extend`,
uses one batched Engine lookup to add only missing required `server_variable`
bucket-value bindings. It preserves explicit injections and skips variables
already owned by workspace policy or native `x-fused-connect` routing. JSON
reports only `generated_binding_count` for enrichment; read service, variable,
and key from the written config, which never contains the value. Configure each
key once:

```shell
fused-cli value set sendbird-bucket sendbird env SENDBIRD_APP_ID your-app-id
```

```yaml
bucket: sendbird-bucket
services:
  sendbird:
    injections:
      - {location: server_variable, name: app_id, value: "${bucket.env.SENDBIRD_APP_ID}", mode: force}
```

The command's `env` argument writes the bucket's non-secret value namespace;
it is not an operating-system environment variable.
`server_variable` accepts only a complete `${bucket.env.KEY}` reference; literals, interpolation, and secrets fail. (`${bucket.values.KEY}` remains an accepted alias, but prefer `env` -- it is what `init` generates and what every example here uses.) Set `name` to a variable declared by the selected service or operation server; it configures server routing rather than an operation argument. Required provider variables still fail closed at execution when unresolved.
Omitted mode means `force`; `default` yields to connection-resource/workspace values but precedes the provider fallback, while `force` also overrides connection-resource input. Workspace policy is final.
Runtime checks variable syntax/enum and the final URL. Hostnames must retain a fixed registrable anchor (`.sendbird.com` passes); whole-host/public-suffix templates fail closed. Resource routing still uses reviewed `allowed_hosts`.

The same object sits as a collection-root sibling of `info`, `item`,
`variable`, and `auth`. Discovery operation IDs refer to the normalized
operation names the Postman importer produces. Postman collections commonly
have no discovery endpoint at all, in which case `resource_input` replaces
`resource_discovery` — the caller supplies the routing value directly:

```json
{
  "info": {
    "name": "Zendesk",
    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
  },
  "auth": { "type": "oauth2", "oauth2": [] },
  "x-fused-connect": {
    "auth_type": "oauth",
    "resource_input": {
      "fields": [
        {
          "name": "subdomain",
          "label": "Zendesk subdomain",
          "type": "text",
          "placeholder": "acme",
          "description": "Enter the part before .zendesk.com.",
          "required": true,
          "pattern": "^[a-z0-9-]+$"
        }
      ],
      "base_url_template": "https://{subdomain}.zendesk.com/api/v2",
      "resource_type": "zendesk_subdomain",
      "allowed_hosts": ["*.zendesk.com"]
    },
    "bindings": [
      {
        "value": "${resource.base_url}",
        "location": "base_url",
        "mode": "force"
      }
    ]
  },
  "item": []
}
```

Imported `resource_input.fields` use the same minimal typed contract as a
workspace profile: `text` (also the omitted-type default) or `select`, with
non-secret string values only. Text fields may add placeholder/help copy and a
server-side RE2 pattern. Select fields declare ordered
`{"value":"...","label":"..."}` options; Engine rejects values outside that
allowlist. These fields never acquire password, secret, numeric, boolean, or
browser URL semantics.

## Who can publish what

Only the service owner (importing their own document) or a configured Fused
curator account (importing on another owner's behalf) can publish a profile
this way — both land as `provenance: provider` or `provenance: fused`
respectively, per `reference/connection-profiles.md`. Anyone else consumes
it read-only or defines a workspace-local override instead (see
`fused-workspace`).
