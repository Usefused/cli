---
name: fused-unified-app
description: "Build, configure, deploy, or call a hosted Fused Unified App. Use for `kind: unified_app`, `buildUnifiedApp`, `.fused/unified_app/`, `fused-cli unified-app`, selected operations, webhook events, connected-user routing, and Unified App OAuth. For generated SDK packages use fused-sdk; for credential setup use fused-bucket."
---

# Unified Apps

A Unified App is hosted TypeScript that calls selected provider operations through Engine. Keep its YAML config and a default-exported `buildUnifiedApp` source file in `.fused/unified_app/`. Use `fused-cli unified-app --help` to check the installed command surface.

When service auth is omitted, Engine planning selects a unique compatible scheme
with complete credentials in that service's bucket. Multiple ready schemes require
`services.<service>.auth.type` and `auth.name`; none ready produces the ordinary
missing-credentials warning. Review the resolved auth in the plan summary. It is
saved with the immutable version and does not change when bucket credentials change.
Explicit auth and managed references always take precedence.

## Create and deploy

Use `fused-cli describe '<goal>' --kind unified` to draft a new app from exact Registry operations and webhook events. When the goal needs inbound provider events, describe reuses a compatible registration or provisions one before deploying the app. Review the generated source, event scope, and registration before confirmation. To write one directly, create `.fused/unified_app/customer-app.yaml`:

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

Create `customer-app.tsx` next to that YAML file:

```ts
import * as z from "zod/mini";
import { buildUnifiedApp } from "@fused/unified-app";
import { fused } from "@fused/operations";

export default buildUnifiedApp({
  input: z.object({ name: z.string() }),
  output: z.object({ customerId: z.string() }),
  async execute({ input }) {
    const customer = await fused.crm.createCustomer({ name: input.name });
    return { customerId: customer.id };
  },
});
```

Select at least one exact operation or webhook event; operation `select_all` is invalid. `source_path` is relative to the YAML file. The top-level bucket supplies default credentials; `services.<name>.bucket` overrides it for one service. Do not put provider secrets in source or config. Each source change needs a new immutable version.

For provider events, add `webhook_attachment: <registration>` at the top level and `webhooks: [<exact-event-name>]` under each selected service. An event-only app can use `operations: []`. The hosted `execute({ input })` receives the normalized envelope `{body, headers, query, path:{urlSlug,eventName}}`; validate the selected `eventName` and payload before effects. Register ingress first when authoring config directly with `fused-cli webhook apply -f <webhook-config>`; the Unified App plan checks attachment coverage. The browser Unified App editor also lets you select events and name an existing registration.

```sh
fused-cli unified-app plan -f .fused/unified_app/customer-app.yaml --json
fused-cli unified-app apply -f .fused/unified_app/customer-app.yaml --json
```

Engine compiles the linked TypeScript during plan and hosts it on apply. Save the returned `app_family_id` and one-time execution token securely. Call `POST /v1/apps/{app_family_id}/executions` with that token and `{"operation":"execute","input":{...}}`. The response exposes `appFamilyId` and the executed `version`; exact version IDs stay in internal receipts and version management. `fused-cli unified-app sync -f <config>` moves older inline source into a linked TypeScript file. `bundle_digest` and `unified-app bundle attach` are only for code compiled separately; never combine `bundle_digest` with `source_path`.

## Switch traffic to an existing version

Use the app's exact name and a human-readable version label:

```sh
fused-cli unified-app promote "Customer lookup" --version 1.0.0
# Alternatively, read the app name from the YAML config.
fused-cli unified-app promote -f .fused/unified_app/customer-app.yaml --version 1.0.0
```

An explicit app name takes precedence over config selection and needs no local file. Without a name, the CLI reads the top-level `name` from the selected `kind: unified_app` config. Config discovery must select exactly one Unified App; use `-f` to disambiguate. Always pass `--version`; the YAML `version` does not select the traffic destination. Promotion leaves the YAML and linked TypeScript unchanged and does not rebuild the app.

Only a retained, runnable version can receive traffic. Promotion requires `app.unified_app.read` and `app.unified_app.manage`. It switches new executions while preserving historical results. The stable REST family URL and token follow each traffic switch without client changes; exact Unified App version IDs are rejected as runtime URLs. Historical reads use that family URL and their original read handle. Rerun/replay require the source version to remain current; the hosted MCP family URL follows the active version, and previous-version MCP sessions must reconnect. If another deployment changes traffic concurrently, inspect the current destination before retrying; do not blindly repeat a failed mutation. Add `--json` for a receipt containing `name`, `version`, `app_family_id`, and `active_app_id`.

## OAuth and user references

Connect a user to each provider service in the app's selected bucket before calling that service. Pass a stable user reference in the request input, then use `fused.forUserRef(input.userRef).serviceName.operationName(input)` in TypeScript. For a person with different references across services, use `fused.forServiceUserRefs({ crm: input.crmUserRef, billing: input.billingUserRef })`. The references route through existing bucket connections; the app does not receive provider tokens. Use `fused-bucket` for OAuth application credentials and connecting users, and `fused-config` for auth selection. A fixed reference can be held in app code or config when all executions intentionally use the same connected user.

## Permissions and team access

Creating and applying an app requires `app.unified_app.create` or `app.unified_app.manage` as appropriate, plus `service.consume` for selected services and `bucket.use` for each selected bucket. Reading an app requires `app.unified_app.read`; token administration requires `app.unified_app.tokens.manage`. Check `fused-cli team eligible-owners`, `team build-access`, and `team access app` when ownership or access is unclear. Follow `fused-cli`'s `reference/access-management.md` for the exact resource and missing permission reported by a denial. Never self-grant access.

## Use from an SDK or MCP server

Declare the hosted app by name and exact version in the consumer config. The map key is its callable alias:

```yaml
apiVersion: fused/v1
kind: sdk
name: customer-portal
version: "1.0.0"
language: typescript
bucket: default
unified_apps:
  customer_lookup:
    name: customer-app
    version: "1.0.0"
```

Apply through the normal consumer workflow:

```bash
fused-cli sdk plan -f .fused/sdks/customer-portal.yaml --json
fused-cli sdk apply -f .fused/sdks/customer-portal.yaml --json
```

For an existing SDK or MCP, keep its existing config and add `unified_apps`, then increment the consumer's `version`. For MCP, use `kind: mcp`, omit SDK-only `language`, provide the server's `description`, and use `fused-cli mcp plan` / `fused-cli mcp apply`. Physical `services` can remain alongside these references; they are optional for a consumer containing only hosted apps.

Plan resolves each name and version to an immutable app ID and public schemas. The actor and owner need `app.unified_app.use` on the target, in addition to ordinary consumer permissions. Execution uses the consumer token; never copy the Unified App's token into the config. The hosted code uses its own configured credential buckets. Restricted consumer tokens must allow `unified_app:customer_lookup`.

The generated TypeScript client exposes `sdk.unifiedApps.customer_lookup(input)`. Pass the Engine HTTP origin as `engineUrl` and the consumer execution token as `token` to `FusedSDK`. Python exposes `await sdk.unified_apps.customer_lookup(input)` and `sdk.unified_apps.customer_lookup_sync(input)` with `engine_url` and `token` in its config. Calls return the Unified App execution envelope, including `status` and `output`; check `status` before consuming output. These calls execute synchronously and do not retry automatically.

MCP advertises the attachment through the existing `execute` tool with its authored input schema:

```json
{
  "name": "execute",
  "arguments": {
    "operation": "unified_app:customer_lookup",
    "input": { "name": "Jane" }
  }
}
```

For REST, POST the same `operation` and `input` to `/v1/apps/<consumer-version-id>/executions` using the consumer token. Execution records belong to the hosted Unified App and retain the consumer token ID for attribution. Reading historical results requires the Unified App's normal result-access authority; an attachment does not grant access to unrelated execution history.

References do not follow traffic changes. The selected Unified App version must be receiving traffic during plan, apply, and execution. After promotion, publish a new consumer version selecting the new target, or promote the pinned target back. At most 16 attachments are allowed. Aliases use lowercase letters, digits, and underscores, start with a letter, and cannot be reserved language names or collide with another alias's `_sync` method. Unified Apps may declare the same `unified_apps` references.

## Call another Unified App

Add `unified_apps: { child: { name: Child, version: "1.0.0" } }` to a `kind: unified_app` config, then call `await fused.callApp("child", input)` from `@fused/unified-app` or `@fused/operations`. Check the returned `status` before reading `output`. Provider services are optional when the app has a hosted dependency.

The same app-use permissions, exact-version checks and token alias grants apply. Each child uses its own bucket, schemas, data and execution record. Pass connected-user references explicitly in its input. Calls are synchronous, are not automatically retried, and replay uses recorded child responses without executing the child again.

Recursive family calls are rejected. A root execution allows at most 32 nested calls and eight app levels including itself. Each waiting parent retains a worker slot; configure enough `engine.unified_apps.max_concurrency` for the chain. A child fails immediately when no slot is available. Parent cancellation and deadlines also bound child work.

## Private execution diagnostics

In the Unified App's **Requests** tab, open an execution and expand **Private diagnostics**. This shows the original app input, captured output, detailed exceptions, stack traces, and recorded provider-operation requests and responses, including failed provider bodies. New Engine-compiled versions include inline source maps for TypeScript line numbers; older bundles retain their available JavaScript frames. An output that fails validation is retained privately even though it is not returned as a successful public result.

Access requires `app.unified_app.diagnostics.read`, granted through a custom role on the app family or workspace. Ordinary app read/manage access does not include this permission. The built-in Owner role retains all permissions. Opening Requests also requires the existing `app.unified_app.read` and workspace `audit.read` permissions. Engine checks ownership and the diagnostic grant on every management API read; execution tokens do not authorize this endpoint:

```text
GET /apps/{app_id}/executions/{execution_id}/diagnostics
```

Detailed exceptions and provider evidence are encrypted on the retained result and expire with it (currently 24 hours after completion). Diagnostics are never embedded in ordinary activity, public errors, logs, or trace attributes. App inputs and validated outputs use the existing result storage and limits. Captured strings are bounded to 64 KiB and the combined diagnostic record to 1 MiB; the UI labels truncated or incomplete evidence. Provider requests are the operation inputs supplied by the app, not credential-injected HTTP headers. Missing historical evidence is reported as unavailable and never reconstructed by executing the app again.

Live and rerun executions publish one logical receipt through the existing execution-event worker, including failures before any provider call. Unified App analytics count those logical runs; service/provider usage continues to count physical calls.

### User-visible execution traces

The Requests inspector shows real worker phase timings (preparation, initialization, input validation, TypeScript execution, output validation). These measurements also emit OTEL spans through the Engine-owned provider and are stored on the canonical execution receipt, so the UI does not query an external trace backend or depend on sampling. Stages that did not run are omitted. Provider calls remain separately inspectable; awaited provider time is included in TypeScript duration. Ordinary app/activity permissions cover timing metadata, while raw bodies and stack traces require the private-diagnostics grant. This is automatic runtime instrumentation, not an authored custom-span API.
