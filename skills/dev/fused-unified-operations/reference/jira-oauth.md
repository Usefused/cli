# Bucket-scoped Jira OAuth

Use one visible bucket consistently for static provider secrets, Jira OAuth app
registration, user connections, and the immutable SDK. `bucket list`
does not prove `bucket.use`; stop on denial and never create or select a
fallback bucket. This example uses `default`:

```shell
fused-cli bucket list
printf '%s' 'client_id=...;client_secret=...;redirect_uri=http://localhost:8081/workspace/connect/callback' | \
  fused-cli connect set jira --bucket default --type oauth --auth-name OAuth2 --value-stdin
```

## Ingest the official contract

Fetch Jira's official OpenAPI document from
`https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json`.
Do not hand-author replacements for the Jira operations. The full document is
currently too broad for a focused SDK import, so derive a reviewed slice that:

- retains the exact official path items for `searchProjects`,
  `getCreateIssueMetaIssueTypes`, `getCreateIssueMetaIssueTypeId`, and
  `createIssue`;
- copies their deterministic transitive local `#/components/...` closure and
  every named security alternative they retain;
- strips passive `x-*`, `example`, and `examples` fields before adding any
  executable Fused metadata;
- fails on missing/non-local references or when the provider source already
  owns a field the derivation would add;
- adds the named `https://api.atlassian.com` server and the real
  `GET /oauth/token/accessible-resources` operation; and
- adds `offline_access`, the reviewed OAuth transport extensions, and one root
  `x-fused-connect` profile with operation-scoped `${resource.base_url}`
  binding.

Keep tenant/cloud IDs out of the derived document. The connection profile uses
`getAccessibleResources`, `base_url_template:
https://api.atlassian.com/ex/jira/{id}`, and binds only the four retained Jira
operations. Use `auth_name: OAuth2`, matching the official security scheme.
Declare `x-fused-token-request-media-type: application/json`,
`x-fused-token-endpoint-auth-method: client_secret_post`, and
`x-fused-pkce-required: false`; JSON exchange requires Engine capability
`auth.oauth2.token_request_media.v1`.

Keep `offline_access` in the consent request. Engine uses that rotating Jira
refresh grant in its managed startup/hourly refresh pool, and the normal SDK
request path refreshes only as a missed-work fallback. Jira access-token expiry
does not require another browser login while the refresh grant remains valid.
When Atlassian rejects or expires the refresh grant, Engine marks the exact
connection `reconnect_required`; repeat consent for the same stable user
reference and selected bucket rather than creating a replacement bucket.

## Attach the provider profile

Reference the imported provider profile by exact auth identity instead of
copying its body into workspace config. Keep the timeout on the immutable Jira
version and do not add retry policy:

```yaml
apiVersion: fused/v1
kind: workspace
services:
  jira:
    service_id: "<jira-service-id>"
    versions:
      - version: "<official-jira-version>"
        service_version_id: "<jira-service-version-id>"
        execution_policy:
          timeout_ms: 60000
        connection_profiles:
          - auth_type: oauth
            auth_name: OAuth2
```

Plan/apply should expose `profile_provenance: provider` and a separate
`create_bucket_binding` for `target_location: base_url` with
`binding_source: ${resource.base_url}`. Post-apply, verify a non-null Registry
profile ID, `provenance: provider`, no workspace override, and `is_public:
false`; that last field is workspace publication state, not Registry
visibility.

## Select and execute

The SDK config selects the official operation IDs and exact OAuth scheme:

```yaml
apiVersion: fused/v1
kind: sdk
name: jira-notifications
version: "1.0.0"
language: typescript
bucket: default
services:
  jira:
    version: "<official-jira-version>"
    operations: [searchProjects, getCreateIssueMetaIssueTypes, getCreateIssueMetaIssueTypeId, createIssue]
    auth: {type: oauth, name: OAuth2}
```

After consent, select only an opaque Engine resource UUID. Discover visible
projects, then issue types for the selected project, then required field
metadata; do not fan out across projects. Pass the chosen project key and issue
type ID as runtime input and route Jira with the exact selector:

```typescript
selectors: {
  jira: {
    endUserRef: "user-42",
    authType: "oauth",
    authName: "OAuth2",
    resourceId: selectedResource.id,
  },
}
```

An authorization-only diagnostic may plan the full SDK and use a throwaway
generated client solely for `auth.startConnectSession`. Prove zero app
execution receipts, then remove the private URL, Engine connection, static
credentials, and SDK token. This local cleanup does not revoke the external
Atlassian grant; successful mutation tests also leave their Jira issue and
email at the explicitly approved destinations.
