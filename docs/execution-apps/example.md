# Execution App example

Use `describe` to draft and deploy from a goal, or author the config directly.
The manual example below selects one workspace operation, stores a searchable
customer ID, and returns a typed output. Replace `crm`, its version, and
`createCustomer` with an operation enabled in your workspace. Use a bucket you
can access.

## Start with `fused-cli describe`

In an interactive terminal connected to your Engine, describe the outcome and
name the services you want to use:

```bash
fused-cli describe --name customer-onboarding --bucket default \
  'Create a Stripe customer, create a HubSpot contact, and return both IDs'
```

Execution App is the default output; `--kind execution` is optional. The CLI
resolves 1–16 exact operations, drafts `buildExecutionApp` TypeScript from their
contracts, and shows the full source and selections before asking you to
apply the proposal. Review the provider calls and output schema. If they do
not match your goal, cancel and make the request more specific.

After confirmation, the CLI sends the source cart to Engine for compilation,
plans the App, saves `.fused/executions/customer-onboarding.yaml`, and applies
the plan. A successful run prints the App ID and one-time execution token.
`describe` requires terminal review; it cannot run unattended in CI. To
change the App later, edit that saved config, increase the version, and run
`execution plan` and `execution apply` with its path:

```bash
fused-cli execution plan -f .fused/executions/customer-onboarding.yaml
fused-cli execution apply -f .fused/executions/customer-onboarding.yaml
```

`describe --update` does not update Execution Apps.

## Author the config yourself

### 1. Put the TypeScript in the config

Create `.fused/executions/customer-app.yaml`. The `source: |` block is the
TypeScript entry point, and every source line is indented by two spaces. If
you draft the code in a separate `.ts` file, copy its complete contents into
this block before planning:

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

### 2. Compile and host it

Run these commands from the directory containing `.fused/`:

```bash
fused-cli execution plan -f .fused/executions/customer-app.yaml --json
fused-cli execution apply -f .fused/executions/customer-app.yaml --json
```

`execution plan` sends the YAML's source and selected operations to the
Engine, where the TypeScript is validated and compiled. `execution apply`
uses that plan to host the compiled bundle and activate the App. There is no
local `tsc` step or separate `.ts` upload command. Save the returned App ID and
one-time family execution token securely.

## Execute the deployed App

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
