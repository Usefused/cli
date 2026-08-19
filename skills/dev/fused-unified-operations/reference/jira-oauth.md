# Bucket-scoped Jira OAuth

Run `bucket list`; its result contains only buckets visible through
`bucket.read` and does not prove `bucket.use`. Choose visible `default` or
another visible candidate, then run connect and SDK plan/apply with that exact
candidate. On `bucket.use` denial, stop and report it; never create a fallback.
Create only for an explicit user request or stated enterprise, tenant, or
environment isolation requirement with workspace `bucket.manage`. Creation
does not grant `bucket.use`; never self-grant. Use the chosen bucket consistently
for provider secrets, Jira OAuth registration, user connection, and immutable
SDK identity. This ordinary example chooses `default`:

```shell
fused-cli bucket list
printf '%s' 'client_id=...;client_secret=...;redirect_uri=http://localhost:8081/workspace/connect/callback' | \
  fused-cli connect set jira --bucket default --type oauth --auth-name JiraOAuth --value-stdin
```

An imported Jira profile can supply this same versioned routing contract. A
workspace-local profile and timeout-only execution policy use these exact
version locations; no tenant or cloud ID belongs in the file:

```yaml
apiVersion: fused/v1
kind: workspace
services:
  jira:
    service_id: "<jira-service-id>"
    versions:
      - version: "2026-08-01"
        service_version_id: "<jira-service-version-id>"
        execution_policy:
          timeout_ms: 60000
        connection_profiles:
          - auth_type: oauth
            profile:
              auth_type: oauth
              auth_name: JiraOAuth
              oauth2_flow: authorizationCode
              resource_discovery:
                version: 1
                stage: post_auth
                operation_id: getAccessibleResources
                server: api
                id_path: "$[*].id"
                name_path: "$[*].name"
                scopes_path: "$[*].scopes"
                base_url_template: "https://api.atlassian.com/ex/jira/{id}"
                resource_type: jira_site
                auto_run: after_oauth_callback
                lifecycle: authoritative
                allowed_hosts: [api.atlassian.com]
              bindings:
                - value: "${resource.base_url}"
                  location: base_url
                  mode: force
                  operations: [listProjects, listCreateIssueTypes, getCreateFieldMetadata, createIssue]
```

Atlassian's imported OAuth scheme declares
`x-fused-token-request-media-type: application/json` and
`x-fused-pkce-required: false`. Form encoding is the default; JSON requires
Engine capability `auth.oauth2.token_request_media.v1`.

The SDK config uses the same bucket and selects the exact named OAuth scheme:

```yaml
apiVersion: fused/v1
kind: sdk
name: jira-notifications
version: "1.0.0"
language: typescript
bucket: default
services:
  jira:
    version: "2026-08-01"
    operations: [listProjects, listCreateIssueTypes, getCreateFieldMetadata, createIssue]
    auth: {type: oauth, name: JiraOAuth}
```

After the generated auth helper completes consent and lists resources, keep the
selected opaque resource ID in application state. Pass project key and issue
type ID as Unified method input, and route only the Jira binding with the exact
runtime selector:

```typescript
selectors: {
  jira: {
    endUserRef: "user-42",
    authType: "oauth",
    authName: "JiraOAuth",
    resourceId: selectedResource.id,
  },
}
```
