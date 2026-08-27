# Config as code

Fused can manage workspace services, generated SDKs, MCP servers, and webhook
registrations through YAML stored under `.fused/`.

## Create or extend a config

Use one deterministic command for workspace, SDK, and MCP files:

```bash
fused-cli workspace init
fused-cli sdk init my-sdk \
  --service okta=2026-07-09 \
  --operation okta=listLogEvents
fused-cli mcp init support-agent \
  --service jira=1001.0.0 \
  --select-all jira
```

The default output follows `.fused/` discovery; `-f <path>` overrides it. An
existing file is never replaced implicitly. Repeat the command with
`--extend` to merge another service or operation while preserving existing
selections:

```bash
fused-cli sdk init my-sdk --extend \
  --service stripe=2026-08-01 \
  --operation stripe=createPayment
```

Running `init` without service flags creates an editable empty skeleton. SDK
and MCP validation becomes actionable after each declared service also has an
operation list or `--select-all`. The command does not create buckets; pass
`--bucket` only after choosing an existing bucket the caller may use.

A service-bearing SDK/MCP init or `--extend` makes one Engine query and adds
only missing required server-variable bindings; explicit injections, workspace
policy, and native `x-fused-connect` routing are preserved. JSON output reports
`generated_binding_count`; use each key written into the config with
`fused-cli value set`. The `fused-config` OpenAPI/Postman reference contains the
single canonical Sendbird setup example and routing safety rules.

## SDK configuration

Create `.fused/sdks/my-sdk.yaml`:

```yaml
apiVersion: fused/v1
kind: sdk
name: my-sdk
version: "1.0.0"
language: typescript
bucket: default
services:
  okta:
    version: "2026-07-09"
    operations:
      - listLogEvents
      - getUser
    auth:
      type: oidc
      name: oktaOidc
      ref: "${bucket.auth.okta.oktaOidc}"
    connect:
      scopes: [openid, profile]
```

`auth.ref` reuses the complete OAuth/OIDC application pair stored for the
named source service/auth family in this app's `bucket`. The source service
need not be selected by the app, but it must be enabled in the workspace with
that named pair stored in the bucket. Target scopes and connected-user grants
remain service-specific; neither credentials nor grants belong in this file.

Operation values are OpenAPI `operationId`s for the selected version. Browse
and update selections with:

```bash
fused-cli sdk service add okta -f .fused/sdks/my-sdk.yaml --version 2026-07-09
fused-cli workspace service operations okta --version 2026-07-09 --json
fused-cli sdk operation add okta listLogEvents getUser -f .fused/sdks/my-sdk.yaml
fused-cli sdk validate -f .fused/sdks/my-sdk.yaml --json
```

### Unified Operations

TypeScript and Python SDK configs may add top-level `unified_operations`. A
binding key is an exact key from `services` (including a provider-qualified key
such as `@acme/github`), and its `operation` is the exact, case-sensitive
OpenAPI `operationId` selected for that service. Do not qualify an operationId
with the service slug.

```yaml
unified_operations:
  issues.create:
    description: Create the same issue in selected providers
    input:
      type: object
      required: [title]
      properties:
        title: {type: string}
        body: {type: string}
    bindings:
      jira:
        operation: createProject
        rollback:
          operation: deleteProject
          input:
            projectId: ${response.jira.id}
      github:
        operation: createIssue
        depends_on: [jira]
        input:
          title: ${input.title}
          body: ${input.body?}
      gitlab: createIssue # compact pass-through form
    output:
      type: object
      required: [id, issue]
      properties:
        id: ${response.github.id ?? response.gitlab.iid}
        issue:
          type: object
          value: ${response.github.issue}
          required: [number]
          properties:
            number: {type: integer}
            title: {type: string}
        labels:
          type: array
          value: ${response.github.labels}
          items: {type: string}
```

An expanded binding accepts `operation`, optional `input`, `depends_on`,
`rollback`, and `output`. A binding without `depends_on` is ready to run in
parallel. A binding with dependencies waits for those exact binding targets to
succeed; missing targets, repeated targets, self-dependencies, and cycles are
rejected before plan. Its forward `input` may reference `${response.<target>}`
only for targets directly listed in its own `depends_on`.

`rollback` names an exact operation already selected for the same service and
may map its input from the original Unified input and its own successful binding
response only. If a binding fails, Engine compensates only its successful direct
`depends_on` targets that declare a rollback. It does not recursively compensate
ancestors or unrelated bindings. In the example, a GitHub failure can invoke
Jira's rollback because GitHub directly names Jira.

DynamicValue documents preserve JSON nulls, booleans, exact numbers, strings,
arrays, and objects across forward, rollback, and output mappings. A complete
`${...}` retains its JSON type, while strings may also interpolate scalar
values. YAML aliases, merge keys, custom tags, timestamps, and non-string
object keys are rejected because they are not portable JSON values.

The only output form is a recursive typed tree; the removed `{schema, mapping}`
form is invalid. Operation and binding outputs both have a constructed
`type: object` root with `properties`, and they may coexist. A scalar property
may use mapping shorthand as above or expanded `{type, value}`. Nested objects
may either construct mapping-bearing properties or pass through one `value`
with schema-only `properties`/`required`. Arrays use one `value` plus an optional
schema-only `items` shape.

When the operation declares output, the generated method returns exactly that
object with no `data` wrapper. Binding outputs run first and become the values
read by the operation output; a binding without output contributes its raw
provider JSON. Without operation output, the generated method retains the
all-settled results/rollbacks envelope and any binding output becomes that
target's normalized result data.

The CLI bounds this source contract to 64 Unified Operations, 16 bindings per
operation, 512 expressions, 10,000 DynamicValue nodes, depth 32, and 1 MiB of
encoded Unified definition data. Engine plan validates expression grammar and
bounds, exact endpoints, dependency/dataflow rules, output-schema syntax, and
generated-name collisions.

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
`connection_profiles` overrides. Credentials, including OAuth/OIDC application
`client_id`/`client_secret` pairs, do not belong in workspace YAML; use
`secret set`.

## Plan and apply

```bash
fused-cli workspace plan
fused-cli workspace apply

fused-cli sdk plan -f .fused/sdks/my-sdk.yaml
fused-cli sdk apply -f .fused/sdks/my-sdk.yaml --json
```

For terminal setup, `fused-cli sdk plan --interactive -f
.fused/sdks/my-sdk.yaml` can securely fill only credentials Engine reports
missing from that file's resolved `bucket`, confirm the immediate bucket write,
and retry once. It never creates or falls back to another bucket. Automation
should keep using ordinary or `--json` planning and handle the structured
`bucket_credentials_missing` result.

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
validates origins, termination, repeated values, and hard limits, then returns
one aggregate document after the provider page loop succeeds. A generated SDK
invocation may only lower the maximum page count for that call. Quota,
concurrency, and retry policies follow the same Engine-owned boundary;
generated clients make one logical Engine request.

See the bundled `fused-config`, `fused-workspace`, `fused-sdk`,
`fused-unified-operations`, and `fused-mcp` skills for task-specific guidance,
or use the [command reference](COMMANDS.md) for every plan/apply/sync flag.
