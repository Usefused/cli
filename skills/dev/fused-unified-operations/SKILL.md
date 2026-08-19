---
name: fused-unified-operations
description: "Configure or review Fused SDK Unified Operations that expose one typed TypeScript or Python operation over multiple selected services. Use when authoring unified_operations, bindings, depends_on, rollback, DynamicValue input/output mappings, normalized or provider-specific outputs, runtime targets/selectors, OAuth/OIDC routing, or interpreting results and rollbacks."
---

# Fused Unified Operations

## Result

Produce one generated SDK method that can call an explicit set of provider
operations, make ready targets eligible for bounded concurrency, wait for
declared dependencies, compensate successful direct dependencies after a graph
execution failure or dependency skip, and return every forward and rollback
outcome.

Unified Operations belong only in a TypeScript or Python `kind: sdk` config.
They wrap operations already selected under `services`; they do not grant a new
authorization scope or carry provider credentials. Read `fused-sdk` for SDK
identity, operation selection, plan/apply, download, and tokens. Read
`fused-bucket` when OAuth/OIDC connections or credentials are not ready.

## Properties and where they are used

### Operation properties

| Property | Location | Result |
|---|---|---|
| `unified_operations` | Top-level, beside `services` | Declares generated multi-service methods. It is invalid in Go SDKs and MCP configs. |
| `<operation-name>` | Key under `unified_operations` | Dot-separated segments become the generated namespace and method, such as `release.provision` -> `sdk.unified.release.provision`. |
| `description` | Operation | Optional descriptive metadata retained with the immutable definition; generated method comments are currently fixed. |
| `input` | Operation | Required JSON Schema for the generated method input. |
| `bindings` | Operation | Required map of public execution-step names to selected provider operations. Maximum 16 bindings. |
| `output` | Operation | Optional normalized schema and mapping applied to every successful target. Do not combine it with binding-level outputs. |

An SDK may declare at most 64 Unified Operations. Each binding key is a unique
execution-step name used by `depends_on`, `${response.<step>}`, caller
`targets`, and returned `result.target`. It does not need to match a service key.
An expanded binding may select a configured service explicitly; omission uses
the binding key as the service key. Quote a provider-qualified service such as
`"@acme/github"` when it starts with `@` in YAML.

### Binding properties

| Property | Location | Result |
|---|---|---|
| Scalar operationId | `bindings.<step>` | Compact pass-through binding, for example `github: createIssue`; the step key also selects the same-named service. |
| `service` | Expanded `bindings.<step>` | Optional configured service key. Omission defaults to the binding key. Use it when step and service names differ or several steps share one service. |
| `operation` | Expanded `bindings.<step>` | Exact, case-sensitive OpenAPI `operationId` already selected for the resolved service, or covered by `select_all`. Do not prefix it with the service name. |
| `input` | Expanded binding | Maps the Unified input and direct dependency responses into the provider request. |
| `depends_on` | Expanded binding | Makes this target wait for the named targets. Without it, ready bindings are eligible to run concurrently. |
| `rollback.operation` | Expanded binding | Exact selected operation on the same service used to compensate this binding after a direct consumer has a graph execution failure or dependency skip. |
| `rollback.input` | `rollback` | Maps the original Unified input and this binding's successful response into the rollback request. |
| `output` | Expanded binding | Optional provider-specific output schema and mapping. Use only when the operation has no root output. |

`depends_on` values are exact binding keys. Self-dependencies, duplicates,
unknown targets, and cycles are invalid. A caller's `targets` must be
dependency-closed: dependencies are never invoked secretly.

Several binding keys may select the same service. They remain separate graph
steps and produce separate results, but they share the one selector keyed by
that configured service. A selector keyed by an alias step is invalid unless
that alias is also the actual configured service key.

Ready forward calls run as dependencies settle, without a global level barrier,
up to four physical calls at a time. After all forwards settle, rollback is
direct, deduplicated, reverse-dependency ordered, and all-settled. A consumer's
input-mapping, physical-call, or canonical-response failure—or its dependency
skip—can compensate only successful direct dependencies that declare rollback.
Output mapping, schema, or encoding errors occur after graph success: they
neither trigger rollback nor block dependants, which consume the retained raw
canonical provider response. Planned rollback dependency edges set reverse
order; independent rollbacks may run concurrently under the same cap, and one
rollback error does not block another. Unused declarations remain inert. Do not
invent `on_failure` or fallback; application code owns fallback.

### DynamicValue properties

`bindings.<target>.input`, `rollback.input`, and `output.mapping` are
DynamicValue documents; the operation-level `input` is instead JSON Schema.
DynamicValue preserves JSON nulls, booleans, exact numbers, strings, arrays,
and objects. An expression must occupy its complete scalar: `${input.title}`
is valid; `prefix-${input.title}` is not. Function calls and operators other
than `??` and the terminal `?` marker are invalid.

| Mapping location | Values it may read |
|---|---|
| `bindings.<target>.input` | `${input.*}`, `${target}`, and `${response.<direct-dependency>.*}` only. |
| `bindings.<target>.rollback.input` | `${input.*}`, `${target}`, and that target's `${response.<target>.*}` only. |
| Root `output.mapping` | `${input.*}`, `${target}`, and `${response.<declared-target>.*}`. Only the current target response is present at evaluation time. |
| Binding `output.mapping` | `${input.*}`, `${target}`, and that binding's `${response.<target>.*}`. |

Use `??` to select the first present, non-null value, for example
`${response.github.iid ?? response.linkedin.id}`. A terminal `?` omits a
missing field or array item, for example `${input.body?}`. YAML aliases, merge
keys, custom tags, timestamps, and non-string object keys are not portable JSON
and are rejected.

The Registry's credential-free generator descriptor receives schemas, exact
operation identities, `depends_on`, and rollback identity. Forward, output,
and rollback DynamicValue mappings remain Engine-local.

### Output behavior

A root `output` gives every successful target the same declared shape and
cannot be combined with binding outputs. Without a root output, each binding
independently uses its own declared `output` or falls back to the selected
provider operation's generated response type. Every declared output requires
both `schema` and `mapping`.

### Generated-call properties

These properties are passed when invoking the generated method, not written
inside `bindings`:

| Property | Where | Result |
|---|---|---|
| `targets` | Required call option in TypeScript; second argument in Python | Exact dependency-closed binding subset to execute. One or many targets are valid. |
| `selectors.<service>.environment` | Per-service call selector | Chooses a declared provider environment for every selected step using that service. |
| `selectors.<service>.endUserRef` / `end_user_ref` | TypeScript / Python selector | Routes every selected OAuth/OIDC step on that service through the connected user. |
| `selectors.<service>.authType` / `auth_type` | TypeScript / Python selector | Disambiguates `oauth`, `oidc`, or another declared auth type. |
| `selectors.<service>.authName` / `auth_name` | TypeScript / Python selector | Disambiguates multiple declared schemes of the same type. |
| `selectors.<service>.resourceId` / `resource_id` | TypeScript / Python selector | Chooses an already connected tenant/account/resource UUID. |
| `idempotencyKey` / `idempotency_key` | Call option | Supplies a stable caller key; generated clients create a UUID when omitted. |

Selectors contain routing identifiers, never provider tokens or secrets. Key
them by configured service, while `targets` remains keyed by binding step. For
a single selected OAuth/OIDC scheme, `endUserRef`/`end_user_ref` is usually
sufficient. One service selector is preflighted and reused by every selected
step and active rollback on that service. Forward and rollback connected-auth
failures return an
`authAction`/`auth_action` of `connect`, `reconnect`, or `select_resource` so the
application can complete the existing SDK auth flow and retry deliberately.

Before dispatch, Engine validates the dependency-closed target set, selectors,
exact endpoints, and execution-token permission for every selected forward and
active rollback operation. Any preflight failure rejects the RPC with
zero physical calls and no `{results, rollbacks}` envelope; inactive rollback
declarations are not preflighted.

The generated call returns `{results, rollbacks}`. Forward results preserve
target order and have `success`, `error`, or `skipped` status. Rollback results
have `success` or `error`, plus `triggeredBy`/`triggered_by`. The Engine waits
for all eligible forward calls and compensations; one provider failure does not
erase another provider's result.

For bucket-scoped Jira OAuth—including the workspace profile, timeout, SDK bucket, token transport, and selector—read `reference/jira-oauth.md`.

## Complete example

This operation runs Jira project lookup and Nimble search concurrently. Jira
issue-type lookup starts after project lookup, and issue creation starts only
after its three direct dependencies succeed. Three binding steps share the one
configured `jira` service and therefore the one `jira` selector. The example
chooses visible `default` as its candidate; SDK plan/apply must pass
`bucket.use` for that exact bucket, or the action stops without creating a
fallback.

```yaml
apiVersion: fused/v1
kind: sdk
name: jira-research
version: "1.0.0"
language: typescript
bucket: default
services:
  jira:
    operations: [listProjects, listCreateIssueTypes, createIssue]
    auth: {type: oauth, name: JiraOAuth}
    connect: {scopes: ["read:jira-work", "write:jira-work", offline_access]}
  nimble:
    operations: [search]
    auth: {type: bearer, name: NimbleToken}

unified_operations:
  research.createIssue:
    description: Research a topic and create a Jira issue
    input:
      type: object
      additionalProperties: false
      required: [query, projectKey, issueSummary]
      properties:
        query: {type: string}
        projectKey: {type: string}
        issueSummary: {type: string}
    bindings:
      jira_projects:
        service: jira
        operation: listProjects
        input: {query: "${input.projectKey}"}
      jira_issue_types:
        service: jira
        operation: listCreateIssueTypes
        depends_on: [jira_projects]
        input:
          projectIdOrKey: "${response.jira_projects.values.0.key}"
      nimble:
        operation: search
        input:
          query: "${input.query}"
          max_results: 1
      jira:
        operation: createIssue
        depends_on: [jira_projects, jira_issue_types, nimble]
        input:
          fields:
            project: {key: "${response.jira_projects.values.0.key}"}
            issuetype: {id: "${response.jira_issue_types.issueTypes.0.id}"}
            summary: "${input.issueSummary}"
            description:
              type: doc
              version: 1
              content:
                - type: paragraph
                  content:
                    - type: text
                      text: "${response.nimble.results.0.description ?? response.nimble.results.0.content ?? response.nimble.results.0.url}"
```

Validate and plan before apply:

```shell
fused-cli sdk validate -f .fused/sdks/jira-research.yaml --json
fused-cli sdk plan -f .fused/sdks/jira-research.yaml --json
```

The generated TypeScript call targets binding keys and routes all three Jira
steps through one connected user selected by the configured service key:

```typescript
const outcome = await sdk.unified.research.createIssue(
  {query: "release readiness", projectKey: "OPS", issueSummary: "Review readiness"},
  {
    targets: ["jira_projects", "jira_issue_types", "nimble", "jira"],
    selectors: {
      jira: {endUserRef: "customer-42"},
    },
    idempotencyKey: "jira-research-2026-08-19",
  },
);

for (const result of outcome.results) console.log(result.target, result.status);
for (const rollback of outcome.rollbacks) console.log(rollback.target, rollback.status);
```

Python uses the same target keys with snake_case selector fields; the async
surface is `sdk.async_unified`:

```python
outcome = sdk.unified.research.create_issue(
    {"query": "release readiness", "projectKey": "OPS", "issueSummary": "Review readiness"},
    ["jira_projects", "jira_issue_types", "nimble", "jira"],
    selectors={"jira": {"end_user_ref": "customer-42"}},
    idempotency_key="jira-research-2026-08-19",
)
```

## Permissions and team access

Unified Operations use SDK control-plane permissions: `app.create`,
`app.manage`, `service.consume`, `bucket.use`, and `app.tokens.manage` as
described by `fused-sdk`. A generated call needs the SDK identity plus execution
token, authorized for every selected forward and active rollback operation; it
does not require `app.read` or a separate Unified scope. CLI download/invoke
lookup requires `app.read`; Activity additionally requires `audit.read`.

For team ownership, use `team eligible-owners`, `team build-access`,
`team access service`, `team access bucket`, and `team access app` as described
by `fused-cli`'s `reference/access-management.md`. On denial, report the
missing permission and resource. Never self-grant, switch credentials, or
broaden access.
