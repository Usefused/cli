# Unified Apps: `buildUnifiedApp`

A Unified App is a hosted TypeScript function that calls selected workspace
operations. It can coordinate calls across services and expose the result
through an SDK, MCP server, or REST API.

The entry point is a default export created with
`buildUnifiedApp`. The Engine compiles the source and runs it in an isolated
worker. The app is its own App kind; it does not need a generated SDK package.

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

Save this TypeScript as `.fused/unified_app/customer-app.tsx` and set
`source_path: customer-app.tsx` in the App YAML. Run `fused-cli unified-app plan`
and `fused-cli unified-app apply`. The [step-by-step apply guide](unified-apps/example.md)
shows the files and commands. The CLI reads the file into the plan cart; the
Engine compiles it during plan.

## Builder parts

| Part | Purpose |
| --- | --- |
| `input` | Required Zod schema. The Engine validates the request before calling `execute`. |
| `output` | Required Zod schema for the object returned by `execute`. |
| `execute({ input })` | Required synchronous or asynchronous function. `input` is typed from the input schema; its return value is typed from the output schema. |
| `fetch.searchable` | Optional list of paths in the stored data document that callers may search. Omit it to allow only Engine metadata searches. |

`execute` returns the authored output object. The Engine adds the execution ID,
status, and other metadata to the API response; these are not fields in the
authored `output` schema. The stored data document is not returned with the
output. Every invocation has its own execution record. Its result remains
available for at least 24 hours. See the [REST example](unified-apps/example.md)
for the shared SDK and Unified App route and their different response shapes.

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

## Switch traffic to a retained version

Pass the app name directly, or read its top-level `name` from a config:

```sh
fused-cli unified-app promote customer-app --version 1.0.0
fused-cli unified-app promote -f .fused/unified_app/customer-app.yaml --version 1.0.0
```

An explicit name takes precedence over config selection. `--version` selects the destination without changing the YAML or TypeScript. See the [promotion command reference](COMMANDS.md#unified-app-promote-app-name---version-version) for permissions, routing behavior, and conflict handling.

## Next steps

- [Generate and deploy from a goal with `fused-cli describe`](unified-apps/example.md#start-with-fused-cli-describe)
- [Complete config, TypeScript, deploy, and API example](unified-apps/example.md)
- [OAuth connections and connected-user routing](unified-apps/oauth.md)
