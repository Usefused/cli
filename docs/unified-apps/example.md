# Unified App example

Use `describe` to draft and deploy from a goal, or author the config directly.
The manual example below selects one workspace operation, stores a searchable
customer ID, and returns a typed output. Replace `crm`, its version, and
`createCustomer` with an operation enabled in your workspace. Use a bucket you
can access.

## Start with `fused-cli describe`

In an interactive terminal connected to your Fused deployment, describe the outcome and
name the services you want to use:

```bash
fused-cli describe --name customer-onboarding --bucket default \
  'Create a Stripe customer, create a HubSpot contact, and return both IDs'
```

Unified App is the default output; `--kind unified` is optional. The CLI
resolves 1–16 exact operations, drafts `buildUnifiedApp` TypeScript from their
contracts, and shows the full source and selections before asking you to
apply the proposal. Review the provider calls and output schema. If they do
not match your goal, cancel and make the request more specific.

After confirmation, the CLI saves `.fused/unified_app/customer-onboarding.tsx`,
sends its source to Fused for compilation, plans the App, saves
`.fused/unified_app/customer-onboarding.yaml`, and applies
the plan. A successful run prints the App ID and one-time execution token.
`describe` requires terminal review; it cannot run unattended in CI. To
change the App later, edit that saved config, increase the version, and run
`unified-app plan` and `unified-app apply` with its path:

```bash
fused-cli unified-app plan -f .fused/unified_app/customer-onboarding.yaml
fused-cli unified-app apply -f .fused/unified_app/customer-onboarding.yaml
```

`describe --update` does not update Unified Apps.

## Author the config yourself

### 1. Create the config and TypeScript file

Create `.fused/unified_app/customer-app.yaml`:

```yaml
apiVersion: fused/v1
kind: unified_app
name: customer-app
version: "1.0.0"
bucket: default
source_path: customer-app.tsx
services:
  crm:
    version: "2026-01-01"
    operations: [createCustomer]
```

Create `.fused/unified_app/customer-app.tsx`:

```ts
import * as z from "zod/mini";
import { buildUnifiedApp, fused } from "@fused/unified-app";
import { services } from "@fused/operations";

export default buildUnifiedApp({
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

`source_path` is relative to the YAML file. You can edit and lint the `.tsx`
file directly. Omit `language` and `generate`; Unified Apps always use
TypeScript and do not generate an SDK package. For an older inline `source: |`
config, `fused-cli unified-app sync -f <config.yaml>` creates the `.tsx` file and
replaces the inline block with `source_path`. `unified-app plan` also performs this
conversion before planning. An existing file with different contents causes
an error instead of being overwritten.

`bucket: default` supplies the default credentials. As with SDK configs,
`services.crm.bucket: crm-team` can select another bucket for that service.

### 2. Compile and host it

Run these commands from the directory containing `.fused/`:

```bash
fused-cli unified-app plan -f .fused/unified_app/customer-app.yaml --json
fused-cli unified-app apply -f .fused/unified_app/customer-app.yaml --json
```

`unified-app plan` reads the linked TypeScript and sends its bytes and selected operations to the
Fused, where the TypeScript is validated and compiled. `unified-app apply`
uses that plan to host the compiled bundle and activate the App. There is no
local `tsc` step or separate `.ts` upload command. Save the returned App ID and
one-time family execution token securely.

## Execute the deployed App

Call the Fused REST route used for both SDK apps and Unified Apps. Use the
version's App ID (a UUID), not its name, in the URL:

```bash
BASE="$FUSED_ENGINE_URL/v1/apps/$FUSED_APP_ID/executions"
curl -H "Authorization: Bearer $FUSED_APP_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"operation":"execute","input":{"name":"Jane"}}' "$BASE"
```

For a Unified App, `operation: "execute"` returns an execution record:

```json
{
  "executionId": "<execution-uuid>",
  "appId": "<app-uuid>",
  "version": "1.0.0",
  "status": "succeeded",
  "mode": "live",
  "readHandle": "<caller-held-handle>",
  "output": { "customerId": "cus_123" },
  "createdAt": "<timestamp>",
  "completedAt": "<timestamp>"
}
```

SDK apps use the same POST route with a selected provider operation name, such
as `"createCustomer"`. Those calls return a physical operation response with
`status_code` and `results`, rather than the Unified App execution record.
Stored `data` is not returned in either Unified App execution or result-read
responses; it remains searchable. A queued or running Unified App execution
can return HTTP 202. Read it later with the returned handle:

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

A Unified App needs at least one explicit selected operation. `select_all`
is invalid for this kind. A source change needs a new
immutable version; only one version in the App family receives new traffic.
`source_path` uses Fused compilation. `bundle_digest` is for code compiled
separately and uploaded with `unified-app bundle attach`; use one or the other.
Source is limited to 256 KiB, and authored imports are limited to
`@fused/unified-app`, `@fused/operations`, `zod`, and `zod/mini`.

See the [`buildUnifiedApp` guide](../UNIFIED_APPS.md) for the function's
contract and the [OAuth guide](oauth.md) for connected-user calls.
