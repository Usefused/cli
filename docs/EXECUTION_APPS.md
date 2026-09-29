# Execution Apps: `buildExecutionApp`

An Execution App is a hosted TypeScript function bound to operations selected
from the workspace. Its entry point is a default export created with
`buildExecutionApp`. The Engine compiles the source and runs it in an isolated
worker. The app is its own App kind; it does not need a generated SDK package.

```ts
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

To deploy this TypeScript, place the complete source under `source: |` in an
Execution App YAML config, then run `fused-cli execution plan` and
`fused-cli execution apply`. The [step-by-step apply guide](execution-apps/example.md)
shows the file and commands. The Engine compiles the source during plan; no
local TypeScript build is required.

## Builder parts

| Part | Purpose |
| --- | --- |
| `input` | Required Zod schema. The Engine validates the request before calling `execute`. |
| `output` | Required Zod schema for the object returned by `execute`. |
| `execute({ input })` | Required synchronous or asynchronous function. `input` is typed from the input schema; its return value is typed from the output schema. |
| `fetch.searchable` | Optional list of paths in the stored data document that callers may search. Omit it to allow only Engine metadata searches. |

`execute` returns the authored output object. The Engine adds the execution ID,
status, and other metadata to the API response; these are not fields in the
authored `output` schema. Every invocation has its own execution record. Its
result remains available for at least 24 hours.

## Operations and stored data

The selected workspace operations are available through typed
`services.<service>.<operation>(input)` methods or `fused.fetch({ service,
operation, input })`. Both run through the Engine's connection and execution
policy. App code can call only operations selected in its config; it cannot
send an arbitrary URL request. The optional `selector` in `fused.fetch`
chooses an environment, connected user, auth scheme, or resource when needed.

`fused.db.get()` reads the execution's JSON data document and
`fused.db.set(value)` replaces it. The document may be up to 512 KiB. This data
is separate from the returned `output`. `fetch.searchable` names paths in that
document, without the `data.` prefix; for example, `customerId` makes
`data.customerId` searchable. Up to 16 paths are allowed, each with at most
four dot-separated segments. A caller needs an app token with the
`execution:read` grant to search stored data.

## Next steps

- [Complete config, TypeScript, deploy, and API example](execution-apps/example.md)
- [OAuth connections and connected-user routing](execution-apps/oauth.md)
