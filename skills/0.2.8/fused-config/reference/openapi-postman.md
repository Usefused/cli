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

Registry persists the effective server for every operation using OpenAPI
precedence: operation-level `servers`, then path-level, then document-root.
Variable declarations retain their default, enum, description, and required
state; actual tenant values belong in workspace
`execution_policy.server_variables`, never in Registry metadata.

Parameters retain all four locations (`path`, `query`, `header`, `cookie`) plus
their schema or content form, style, explode, allow-reserved/empty flags,
examples, defaults, and deprecation state. Explicit `false` remains distinct
from an absent option. Generated SDKs expose schema-derived TypeScript/Python
types and pass typed values to Engine; Engine alone applies OpenAPI wire
serialization. Standard methods such as `CONNECT`, `OPTIONS`, and `TRACE` are
imported as operations rather than dropped.

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

OpenAPI callbacks retain their runtime expression and parent operation, while
3.1 top-level webhooks retain the same parameter, request, response, server,
and security contract as outbound operations. Response links and their runtime
expressions are preserved but not executed. Safe namespaced `x-*` JSON carries
`source_spec` provenance for documentation; only the exact reviewed
`x-fused-*` allowlist can affect behavior, and strict planning rejects unknown
Fused execution extensions.

## Postman

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

## Who can publish what

Only the service owner (importing their own document) or a configured Fused
curator account (importing on another owner's behalf) can publish a profile
this way — both land as `provenance: provider` or `provenance: fused`
respectively, per `reference/connection-profiles.md`. Anyone else consumes
it read-only or defines a workspace-local override instead (see
`fused-workspace`).
