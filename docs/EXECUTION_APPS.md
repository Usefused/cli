# Execution Apps

An Execution App is a hosted TypeScript `execute` function bound to selected
workspace operations. It is a distinct App kind: a generated SDK package is not
required. Engine REST calls it, and hosted MCP can expose it when configured.

## Create and deploy

For an interactive goal, run `fused-cli describe '<goal>'`. It drafts TypeScript
from exact Registry operation contracts and shows the source and selections for
review. After confirmation, the CLI sends the source cart to Engine. Engine
resolves operation IDs and compiles during plan; apply stores the compiled
bundle and activates the new App version. The CLI does not compile TypeScript.
Describe creation requires 1–16 explicit operations. Use `--kind sdk`, `mcp`, or
`rest` when you want one of those outputs instead. To update an Execution App,
edit its source config and deploy a new version.

For a reviewable config, create `.fused/executions/customer-app.yaml`:

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

Replace the example service, version, and operation with ones enabled in your
workspace, and use a bucket you can access. Then run:

```bash
fused-cli execution plan -f .fused/executions/customer-app.yaml --json
fused-cli execution apply -f .fused/executions/customer-app.yaml --json
```

Save the returned App ID and one-time family execution token securely. Engine
accepts either inline `source` or a precompiled `bundle_digest`, never both.
Inline source is the normal path; the digest and bundle attachment path remains
available for existing precompiled configs. An Execution App must select at
least one explicit operation. `select_all` and `unified_operations` are invalid
for this kind. A source change requires a new immutable version. Only one
version in an App family receives new traffic at a time. Source is limited to
256 KiB; authored imports are limited to `@fused/execution`,
`@fused/operations`, `zod`, and `zod/mini`.

## Execute and read results

Execution Apps use the same REST path as SDK apps, with the authored operation
name `execute`. The request supplies only `operation` and the Zod-validated
`input`; the app source owns provider routing and calls.

```bash
BASE="$FUSED_ENGINE_URL/v1/apps/$FUSED_APP_ID/executions"
curl -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"operation":"execute","input":{"name":"Jane"}}' "$BASE"
```

The response contains an `executionId`, `status`, typed `output`, stored `data`,
and a `readHandle` shown to that caller. A queued or running call can return
HTTP 202; read the result later with both the family token and the returned
handle:

```bash
curl -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H "X-Execution-Read-Handle: $FUSED_READ_HANDLE" \
  "$BASE/$FUSED_EXECUTION_ID"
```

Each execution has one JSON data document written through `fused.db.set` and
read through `fused.db.get`. Its maximum size is 512 KiB. Completed results are
retained for at least 24 hours. `fetch.searchable` declares the data paths that
an app token with the `execution:read` grant may search. For example:

```bash
curl -G -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  --data-urlencode 'where={"data.customerId":"cus_123"}' "$BASE"
```

Replay runs from recorded provider calls without calling the provider again.
Rerun executes the original input again and may repeat provider effects. Both
require the source execution's read handle and the currently active App version;
rerun also requires an `Idempotency-Key`:

```bash
curl -X POST -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H "X-Execution-Read-Handle: $FUSED_READ_HANDLE" \
  "$BASE/$FUSED_EXECUTION_ID/replay"
curl -X POST -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H "X-Execution-Read-Handle: $FUSED_READ_HANDLE" \
  -H 'Idempotency-Key: customer-123-rerun-1' \
  "$BASE/$FUSED_EXECUTION_ID/rerun"
```

## Call selected workspace operations

Inside `execute`, `services.crm.createCustomer(...)` and `fused.fetch(...)`
call only operations selected by the App config. They dispatch through Engine,
including its workspace connection and execution policy. Authored code cannot
call arbitrary provider URLs directly. These calls are distinct from the REST
GET that reads a stored execution.

Use `fused.forUserRef("customer-42")` to bind one connected-user reference to
all calls through that helper, or `fused.forServiceUserRefs({ crm:
"crm-customer-42", billing: "billing-customer-42" })` to bind each service
separately. Connect those references in the App's bucket first. The helpers
reject a conflicting per-call user reference.

For a selected paginated operation, the helper can bind a user reference and
a page ceiling together:

```ts
const crm = fused.forServiceUserRefs({ crm: "crm-customer-42" });
const customers = await crm.fetch({
  service: "crm",
  operation: "listCustomers",
  input: {},
  selector: { authType: "oauth" },
  pagination: { maxPages: 2 },
});
```

The bound must be a positive integer strictly below the operation's configured
page limit; omitting it uses the operation's automatic Engine pagination
policy. The App cannot add pagination to an operation with no pagination
policy. Select `listCustomers` in the App config before using this example.
