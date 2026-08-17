# Config as code

Fused can manage workspace services, generated SDKs, MCP servers, and webhook
registrations through YAML stored under `.fused/`.

## SDK configuration

Create `.fused/sdks/my-sdk.yaml`:

```yaml
apiVersion: fused/v1
kind: sdk
name: my-sdk
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-09"
    operations:
      - listLogEvents
      - getUser
```

Operation values are OpenAPI `operationId`s for the selected version. Browse
and update selections with:

```bash
fused-cli sdk service add okta -f .fused/sdks/my-sdk.yaml --version 2026-07-09
fused-cli workspace service operations okta --version 2026-07-09 --json
fused-cli sdk operation add okta listLogEvents getUser -f .fused/sdks/my-sdk.yaml
fused-cli sdk validate -f .fused/sdks/my-sdk.yaml --json
```

## Workspace configuration

Create `.fused/workspace.yaml`:

```yaml
apiVersion: fused/v1
kind: workspace
services:
  stripe:
    versions:
      - version: "2026-07-09"
      - version: "2026-08-01"
  okta:
    versions:
      - version: "1.0.0"
```

Service keys are Registry slugs. Engine resolves slugs and version identities
during planning. If versions are omitted, the Engine resolves the latest public
version during planning and records its exact identity.

Each version entry can carry `public`, `execution_policy`, and
`connection_profiles` overrides. Credentials and OAuth/OIDC app registrations
do not belong in workspace YAML; use `secret set` and `connect set`.

## Plan and apply

```bash
fused-cli workspace plan
fused-cli workspace apply

fused-cli sdk plan -f .fused/sdks/my-sdk.yaml
fused-cli sdk apply -f .fused/sdks/my-sdk.yaml --json
```

Plan output contains the complete Engine change summary. A saved receipt is
bound to the exact config content and normalized Engine URL. Apply validates
every selected config before its first remote mutation and rejects stale,
unbound, or cross-Engine receipts.

CLI-managed config files and receipts are replaced atomically. Existing file
permissions are preserved, and structured content is validated before it
replaces the previous file.

## Sync remote state

```bash
fused-cli workspace sync -f .fused/workspace.yaml
fused-cli sdk sync my-sdk -f .fused/sdks/my-sdk.yaml
```

Workspace sync mirrors active Engine services. SDK sync mirrors one exact
generated SDK version, including selected services, versions, and operation
names.

## Execution policy ownership

Execution policies live on workspace services and versions. SDK and MCP
configs reference them; generated clients do not duplicate pagination, quota,
concurrency, or retry logic.

```yaml
services:
  google-drive:
    execution_policy:
      pagination:
        version: 3
        request:
          - state: cursor
            target: {location: query, name: pageToken}
            value_type: string
            apply: all
        response:
          items: {path: "$.files"}
          values:
            - name: next_cursor
              source: {location: body, path: "$.nextPageToken", value_type: string}
        continuation:
          - {kind: token, state: cursor, response_value: next_cursor}
        termination:
          stop_on_empty_items: true
          stop_on_missing_values: [next_cursor]
          repeated_value: error
        limits:
          max_pages: 100
          max_items: 10000
          max_bytes: 16777216
          max_duration_ms: 120000
```

The policy always affects the local Engine. `execution_policy.public: true`
also publishes it when the caller owns the service. Version policy overrides
the service-level default, while endpoint policy remains more specific.

Version 3 supports composed token, offset, page, RFC Link, next-URL,
conditional path, derived cursor, and GraphQL pagination strategies. Engine
validates origins, termination, repeated values, and hard limits, then streams
successful provider pages. Quota, concurrency, and retry policies follow the
same Engine-owned boundary; generated clients make one logical Engine request.

See the bundled `fused-config`, `fused-workspace`, `fused-sdk`, and `fused-mcp`
skills for task-specific guidance, or use the [command reference](COMMANDS.md)
for every plan/apply/sync flag.
