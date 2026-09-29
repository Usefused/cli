# Execution App example

This example selects one workspace operation, stores a searchable customer ID,
and returns a typed output. Replace `crm`, its version, and `createCustomer`
with an operation enabled in your workspace. Use a bucket you can access.

Create `.fused/executions/customer-app.yaml`:

```yaml
apiVersion: fused/v1
kind: execution
name: customer-app
version: "1.0.0"
language: typescript
generate: false
bucket: default
services:
  crm:
    version: "2026-01-01"
    operations: [createCustomer]
source: |
  import * as z from "zod/mini";
  import { buildExecutionApp, fused } from "@fused/execution";
  import { services } from "@fused/operations";

  export default buildExecutionApp({
    input: z.object({ name: z.string() }),
    output: z.object({ customerId: z.string() }),
    fetch: { searchable: ["customerId"] },
    async execute({ input }) {
      const customer = await services.crm.createCustomer({ name: input.name });
      await fused.db.set({ customerId: customer.id });
      return { customerId: customer.id };
    },
  });
```

Plan and apply the config:

```bash
fused-cli execution plan -f .fused/executions/customer-app.yaml --json
fused-cli execution apply -f .fused/executions/customer-app.yaml --json
```

The CLI sends the source and selected-operation cart to the Engine. The Engine
resolves the operations and compiles the TypeScript during plan, then publishes
the bundle during apply. Save the returned App ID and one-time family execution
token securely. `fused-cli describe '<goal>'` can draft and deploy an Execution
App through an interactive review; this YAML path is useful when you want to
edit the source directly.

Call the same Engine REST route used for SDK apps:

```bash
BASE="$FUSED_ENGINE_URL/v1/apps/$FUSED_APP_ID/executions"
curl -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"operation":"execute","input":{"name":"Jane"}}' "$BASE"
```

The response has an `executionId`, `status`, typed `output`, stored `data`, and
a caller-specific `readHandle`. A queued or running execution can return HTTP
202. Read it later with the returned handle:

```bash
curl -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H "X-Execution-Read-Handle: $FUSED_READ_HANDLE" \
  "$BASE/$FUSED_EXECUTION_ID"
```

With an app token granted `execution:read`, search the stored customer ID:

```bash
curl -G -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  --data-urlencode 'where={"data.customerId":"cus_123"}' "$BASE"
```

Replay uses recorded provider calls and does not call the provider again.
Rerun executes the original input again and may repeat provider effects. Both
need the source execution's read handle and the currently active App version;
rerun also needs an idempotency key:

```bash
curl -X POST -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H "X-Execution-Read-Handle: $FUSED_READ_HANDLE" \
  "$BASE/$FUSED_EXECUTION_ID/replay"
curl -X POST -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H "X-Execution-Read-Handle: $FUSED_READ_HANDLE" \
  -H 'Idempotency-Key: customer-123-rerun-1' \
  "$BASE/$FUSED_EXECUTION_ID/rerun"
```

An Execution App needs at least one explicit selected operation. `select_all`
and `unified_operations` are invalid for this kind. A source change needs a new
immutable version; only one version in the App family receives new traffic.
Inline `source` and a precompiled `bundle_digest` are mutually exclusive.
Source is limited to 256 KiB, and authored imports are limited to
`@fused/execution`, `@fused/operations`, `zod`, and `zod/mini`.

See the [`buildExecutionApp` guide](../EXECUTION_APPS.md) for the function's
contract and the [OAuth guide](oauth.md) for connected-user calls.
