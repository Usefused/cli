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
