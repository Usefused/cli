# Config as code

Fused can manage workspace services, generated SDKs, MCP servers, and webhook
registrations through YAML stored under `.fused/`.

## Create or extend a config

Use `init` for the working app path and `workspace init` when you only want an
editable workspace skeleton:

```bash
fused-cli workspace init
fused-cli init my-sdk --sdk \
  --service okta \
  --operation okta=listLogEvents
fused-cli init support-agent --mcp \
  --description "Help support teams manage issues" \
  --service jira \
  --select-all jira
```

The default output follows `.fused/` discovery; `-f <path>` overrides it. An
existing file is never replaced implicitly. Use top-level `extend` to merge
another service or operation while preserving existing selections. It reads the
existing YAML to infer SDK, API, or MCP mode:

```bash
fused-cli extend my-sdk \
  --service stripe \
  --operation stripe=createPayment
```

The same YAML path is updated; Fused keeps the stable app family and creates a
new immutable version when apply runs. If the merge changes a stable SemVer version,
Fused infers the next minor version and includes it in terminal confirmation;
`--no-input` and `CI=true` use the same deterministic inference. Idempotent
repeats keep the current version. Pass `--version` to override inference, and
always pass it when the current version is a prerelease or not SemVer.

Top-level init requires at least one service and resolves an omitted provider
version to one concrete enabled or latest public version. In a terminal, omit
`--sdk`, `--api`, or `--mcp` to choose the outcome, and omit operation flags to
choose all operations or search a narrower set. If a version is not enabled,
the CLI asks once to enable it and create the app; workspace and app changes
still receive separate plan receipts. `--no-input` and `CI=true` skip prompts,
require one explicit mode, and require `--operation` or `--select-all` for each
service. Top-level init does not support `--json`.

Use `--no-apply` when initialization is only preparing files for later review.
The CLI still resolves concrete service versions, operation selections,
buckets, and required local workspace additions, then writes semantically
validated config and saves available plan receipts. It does not apply Engine
state, generate an SDK, or download a package. If a missing workspace service
prevents the app plan, the workspace plan is saved first and app planning is
listed after workspace apply. Otherwise the app receipt is ready for apply.
The command prints the exact remaining commands; generated SDKs finish with
`sdk apply --download`. A stale receipt is handled by rerunning its plan
command before apply.

`fused-cli init <name> --api` uses the same `kind: sdk` execution resource with
`generate: false`, then prints a central Engine REST request template instead of
downloading a package. The hidden `sdk init` and `mcp init` compatibility
commands remain callable for scripts that only need the older scaffold flow.

SDK and MCP validation becomes actionable after each declared service also has
an operation list or `--select-all`. Init does not create buckets; pass
`--bucket` only after choosing an existing bucket the caller may use.

A service-bearing top-level init or extend adds only missing required
server-variable bindings; explicit injections, workspace policy, and native
`x-fused-connect` routing are preserved. Empty SDK scaffolds and MCP init retain
JSON scaffold output with `generated_binding_count`; use each key written into
the config with `fused-cli value set`. The `fused-config` OpenAPI/Postman
reference contains the single canonical Sendbird setup example and routing
safety rules.

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
fused-cli sdk plan -f .fused/sdks/my-sdk.yaml --json
```

`sdk plan` performs the same local validation first, before its Engine request.
The standalone `sdk validate` command remains available for offline-only checks.

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

Generated-SDK apply returns once Engine has atomically stored the app, token,
and queued package job. The app remains non-runnable while
`generation_status` is `pending`; Engine activates it only after background
generation completes. Add `--download` when the same command should wait by
exact Version ID and fetch the package.

For terminal setup, `fused-cli sdk plan -f .fused/sdks/my-sdk.yaml` securely
fills only credentials Engine reports missing from that file's resolved
`bucket`, confirms the immediate bucket write, and retries once. Declining
keeps the valid plan, and the app can still be published. It never creates or
falls back to another bucket. Automation should pass `--no-input` or `--json`
and inspect non-blocking `credential_readiness`; an affected runtime call later
returns `bucket_credentials_missing` with the exact safe setup command.

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
