# Config as code

Fused can manage workspace services, Unified Apps, Apps with SDK, MCP, and
REST delivery, and webhook registrations through YAML stored under `.fused/`.

## Create or extend a config

Use `init` for the working app path and `workspace init` when you only want an
editable workspace skeleton:

```bash
fused-cli workspace init
fused-cli init customer-app \
  --service okta@v2 \
  --operation okta=listLogEvents
fused-cli init my-sdk --sdk \
  --service okta@v2 \
  --operation okta=listLogEvents
fused-cli init support-agent --mcp \
  --description "Help support teams manage issues" \
  --service jira@v3 \
  --select-all jira
```

The default output follows `.fused/` discovery; `-f <path>` overrides it. An
existing file is never replaced implicitly. Use top-level `extend` to merge
another service or operation while preserving existing selections. It reads the
existing YAML to infer SDK, API, or MCP mode:

```bash
fused-cli extend my-sdk \
  --service stripe@2026-08-01 \
  --operation stripe=createPayment
```

The same YAML path is updated; Fused keeps the stable app family and creates a
new immutable version when apply runs. If the merge changes a stable SemVer version,
Fused infers the next minor version and includes it in terminal confirmation;
`--no-input` and `CI=true` use the same deterministic inference. Idempotent
repeats keep the current version. Pass `--version` to override inference, and
always pass it when the current version is a prerelease or not SemVer.

Top-level init requires at least one service and resolves an omitted provider
version to one concrete enabled or latest public version. With no method flag,
it creates one App with SDK, MCP, and REST delivery. Use `--sdk`, `--mcp`, or
`--rest` (also available as `--api`) for a single method. In a terminal, omit
operation flags to choose all operations or search a narrower set. The combined
App uses one `kind: sdk` file with `generate: true` and an `mcp` section. Its
immutable version and execution token are shared by all three methods.
If a version is not enabled, the CLI asks once to enable it and create the app; workspace and app changes
still receive separate plan receipts. `--no-input` and `CI=true` skip prompts
and require `--operation` or `--select-all` for each
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

## Unified App configuration

A Unified App is a distinct `kind: unified_app` resource. Its config selects
explicit workspace operations and links one TypeScript `source_path` export using
`buildUnifiedApp({ input, output, execute, fetch? })`. Input and output use
Zod. The CLI reads the `.tsx` file and sends its bytes to Engine, which
compiles them during `unified-app plan` and publishes the bundle during
`unified-app apply`. `unified-app sync` moves legacy inline `source: |` into a `.tsx`
file and updates the YAML. `language` and `generate` are unnecessary here.

Store the config and source under `.fused/unified_app/` and run:

```bash
fused-cli unified-app plan -f .fused/unified_app/customer-app.yaml --json
fused-cli unified-app apply -f .fused/unified_app/customer-app.yaml --json
```

Engine returns the immutable App version ID and shows its family execution
token once. A new source revision needs a new App version. `source_path` and a
`bundle_digest` for code compiled outside Engine are mutually exclusive;
`select_all` is not valid for Unified Apps. See
the [`buildUnifiedApp` guide](UNIFIED_APPS.md) for the function contract,
or the [complete example](unified-apps/example.md) for YAML, apply, and API
calls.

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

### One App with SDK, MCP, and REST

Add `mcp:` to a `kind: sdk` declaration to expose the same immutable App version
through hosted MCP. REST execution is already available for every SDK-kind App.
The App ID, version, selected operations, credential bucket, and execution token
are shared across all three methods.

```yaml
apiVersion: fused/v1
kind: sdk
name: customer-app
version: "1.0.0"
language: typescript
bucket: default
mcp:
  description: Search and manage customer records
services:
  crm:
    version: "2026-07-01"
    operations: [listCustomers, getCustomer]
```

Run `fused-cli sdk plan` and `fused-cli sdk apply --download`. Apply returns one
App ID and token plus the Engine-owned MCP URLs. The MCP route becomes callable
when package generation completes. This App uses both the SDK and MCP family
allowances. A successor version keeps the same family identity and can change
its MCP description or selected operations through a new reviewed config.

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

## Workspace configuration

Local workspace configuration is optional when the UI owns the workspace. Use
`.fused/workspace.yaml` when one aggregate file is convenient:

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

For independently owned service sets, use `type: services` files anywhere
under `.fused/services/`:

```yaml
apiVersion: fused/v1
type: services
services:
  stripe:
    versions:
      - version: "2026-07-09"
```

Default discovery composes the aggregate file and every services file into one
sparse workspace plan. A service may be declared in only one local file.
Omitting or deleting a local declaration does not remove the active service;
explicit service/version removal remains a separately reviewed operation.

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
fused-cli workspace sync
fused-cli workspace sync --file .fused/services/payments.yaml
fused-cli workspace sync --service stripe
fused-cli workspace sync --service stripe@v1,github@v3 --file .fused/services/payments.yaml
fused-cli workspace sync --all
fused-cli sdk sync my-sdk -f .fused/sdks/my-sdk.yaml
```

Workspace sync is a non-destructive Engine-to-local pull. By default it refreshes
only services already declared in local aggregate or `type: services` files;
`--file` scopes that set to one file. `--service <service>[@<version>]` accepts
comma-separated values or repeated flags and creates or refreshes selected
services in one explicit file. A declaration in another local file transfers
to that destination with authored policy intact. Without `--file`, each new
service gets its own readable default file; a stable service-identity suffix
disambiguates colliding slugs. An omitted version pulls all active versions; an
exact version pulls only that version without deleting unselected local
versions. Only `--all` imports every active service. Removing a service from a
local file does not remove it from the workspace, and sync retains declarations
the Engine no longer reports. SDK sync mirrors one exact generated SDK version,
including selected services, versions, and operation names.

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

See the bundled `fused-config`, `fused-workspace`, `fused-sdk`, and `fused-mcp`
skills for task-specific guidance,
or use the [command reference](COMMANDS.md) for every plan/apply/sync flag.
