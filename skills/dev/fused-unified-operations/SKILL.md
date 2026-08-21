---
name: fused-unified-operations
description: "Configure or review Fused SDK Unified Operations that expose one typed TypeScript or Python operation over multiple selected services. Use when authoring unified_operations, bindings, depends_on, rollback, recursive typed outputs, runtime targets/selectors, OAuth/OIDC routing, or interpreting transformed results and rollbacks."
---

# Fused Unified Operations

## Result

Produce one generated SDK method that can call an explicit set of provider
operations, make ready targets eligible for bounded concurrency, wait for
declared dependencies, compensate successful direct dependencies after a graph
execution failure or dependency skip, and optionally return one exact
operation-level transformed value.

Use Unified Operations only in TypeScript or Python `kind: sdk` configs. They wrap
selected `services`, grant no new scope, and carry no credentials. Read `fused-sdk`
for lifecycle and `fused-bucket` for OAuth/OIDC or credential readiness.

## Properties and where they are used

### Operation properties

| Property | Location | Result |
|---|---|---|
| `unified_operations` | Top-level, beside `services` | Declares generated multi-service methods. It is invalid in Go SDKs and MCP configs. |
| `<operation-name>` | Key under `unified_operations` | Dot-separated segments become the generated namespace and method, such as `release.provision` -> `sdk.unified.release.provision`. |
| `description` | Operation | Optional descriptive metadata retained with the immutable definition; generated method comments are currently fixed. |
| `input` | Operation | Required JSON Schema for the generated method input. |
| `bindings` | Operation | Required map of public execution-step names to selected provider operations. Maximum 16 bindings. |
| `output` | Operation | Optional final recursive projection. Its constructed object is the generated method's exact success return value. It may be combined with binding outputs. |

An SDK may declare at most 64 Unified Operations. Each binding key is a unique execution-step name
used by `depends_on`, `${response.<step>}`, caller `targets`, and returned `result.target`; it need not match a service key.
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
| `output` | Expanded binding | Optional recursive projection for that provider result. It uses the same constructed-object root as operation output and may be combined with it. |

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
Binding output mapping, validation, or encoding errors occur after its provider
call succeeds: they neither trigger rollback nor block dependants, which consume
the retained raw canonical provider response. Planned rollback dependency edges
set reverse order; independent rollbacks may run concurrently under the same
cap, and one rollback error does not block another. Unused declarations remain
inert. Do not invent `on_failure` or fallback; application code owns fallback.

### DynamicValue properties

`bindings.<target>.input`, `rollback.input`, and output `value`/property shorthand
are DynamicValue documents; operation-level `input` is JSON Schema. DynamicValue
preserves every JSON type. A complete `${response.drive.files}` stays an array;
mixed strings interpolate non-null scalars, as in `File ${response.drive.files.2.id}`.
Objects, arrays, or null fail interpolation before dispatch. Use `$${` for a
literal `${`. Functions and operators other than `??` and terminal `?` fail.

| Mapping location | Values it may read |
|---|---|
| `bindings.<target>.input` | `${input.*}`, `${target}`, and `${response.<direct-dependency>.*}` only. |
| `bindings.<target>.rollback.input` | `${input.*}`, `${target}`, and that target's `${response.<target>.*}` only. |
| Operation output property/value | `${input.*}` and `${response.<declared-target>.*}` after selected bindings settle. A target with binding output exposes that transformed value; otherwise it exposes raw provider JSON. |
| Binding output property/value | `${input.*}`, `${target}`, and that binding's raw `${response.<target>.*}`. |

Use `??` to select the first present non-null value, for example
`${response.github.iid ?? response.linkedin.id}`. Terminal `?` omits a missing
value and contributes empty text inside interpolation. Forward input response
references retain direct `depends_on`; output scopes follow the table above.
Non-JSON YAML constructs are rejected.

The Registry's credential-free descriptor receives schemas, exact operation,
dependency, and rollback identities. DynamicValue mappings remain Engine-local.

### Output behavior

Use only the recursive output tree; `{schema, mapping}` is invalid. Operation
and binding roots are constructed `type: object` nodes with `properties`, and
they may coexist. Operation output is the exact method return value without a
`data` or `{results, rollbacks}` success wrapper; binding outputs run first and
become its response values.

Scalar shorthand such as `name: "${response.drive.name}"` infers its type from
the authored JSON scalar; expanded scalars use `{type, value}`. A nested object
either has mapping-bearing `properties`, or one pass-through `value` plus
schema-only `properties`/`required`. Arrays use one `value` plus optional
schema-only `items`. Schema-only scalar leaves have `type` but no `value`, and
`additionalProperties`, when present on an output object, is boolean.

Final operation-output mapping, validation, or encoding failure throws the
generated Unified output error with bounded forward and rollback diagnostics.
Without operation output, binding output replaces only its target's `data` in
the ordinary all-settled success envelope.

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

Without operation output, the call returns `{results, rollbacks}`. Forward
results preserve target order with `success`, `error`, or `skipped`; rollbacks
use `success` or `error` plus `triggeredBy`/`triggered_by`. One provider failure
does not erase another result. With operation output, the exact transformed
object replaces that success envelope as described above.

For bucket-scoped Jira OAuth—including the workspace profile, timeout, SDK bucket, token transport, and selector—read `reference/jira-oauth.md`.

## Complete example

This operation normalizes GitHub first, then builds one exact result from that
binding output and GitLab's raw provider response.

```yaml
apiVersion: fused/v1
kind: sdk
name: issues
version: "1.0.0"
language: typescript
services:
  github: {operations: [createIssue, deleteIssue]}
  gitlab: {operations: [createIssue]}

unified_operations:
  issues.create:
    input:
      type: object
      required: [title]
      properties: {title: {type: string}}
    bindings:
      github:
        operation: createIssue
        input: {title: "${input.title}"}
        rollback: {operation: deleteIssue, input: {id: "${response.github.id}"}}
        output:
          type: object
          properties:
            id: "${response.github.id}"
            issue:
              type: object
              value: "${response.github}"
              required: [title]
              properties: {title: {type: string}}
      gitlab: createIssue
    output:
      type: object
      required: [primary_id, title, provider_ids]
      properties:
        primary_id: "${response.github.id}"
        title: "${response.github.issue.title}"
        provider_ids:
          type: array
          value: ["${response.github.id}", "${response.gitlab.iid}"]
          items: {type: string}
```

Validate and plan before apply:

```shell
fused-cli sdk validate -f .fused/sdks/issues.yaml --json
fused-cli sdk plan -f .fused/sdks/issues.yaml --json
```

The configured object is the exact TypeScript return type and value:

```typescript
const result = await sdk.unified.issues.create(
  {title: "Fix login"},
  {targets: ["github", "gitlab"], idempotencyKey: "issue-42"},
);
console.log(result.primary_id, result.title, result.provider_ids);
```

## Permissions and team access

Unified Operations use `app.create`, `app.manage`, `service.consume`, `bucket.use`, and `app.tokens.manage` as
described by `fused-sdk`. A generated call needs the SDK identity plus execution
token authorized for every selected forward and active rollback operation; it
does not need a separate Unified scope. CLI lookup needs `app.read`; Activity
also needs `audit.read`. For team ownership, use `team eligible-owners`,
`team build-access`, and `team access app` per `fused-cli`'s `reference/access-management.md`.
On denial, report the missing permission and resource. Never self-grant, switch
credentials, or broaden access.
