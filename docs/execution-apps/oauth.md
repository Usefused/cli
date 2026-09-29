# OAuth in Execution Apps

An Execution App uses the selected workspace operation's connection through
its configured bucket. Connect each end user to the service before invoking
an operation on that user's behalf. The app source receives a stable user
reference, never the provider's access or refresh token.

## Connect a user

Choose an OAuth application for the exact service and auth scheme. If a Fused
Managed App is available, select it explicitly in the Execution App's service
config:

```yaml
services:
  crm:
    version: "2026-01-01"
    operations: [getCustomer]
    auth:
      type: oauth
      name: crmOAuth
      ref: "${fused.bucket.auth.crm.crmOAuth}"
```

Check availability with `fused-cli workspace managed-auth status`. If you use
your own provider app instead, store its client pair in the same bucket:

```bash
printf '%s' 'client_id=...;client_secret=...' |
  fused-cli secret set crm --bucket default \
    --type oauth --auth-name crmOAuth --value-stdin
```

For your own provider app, start a connection for a stable end-user reference:

```bash
fused-cli workspace service connect crm \
  --bucket default --user-ref customer-42 \
  --type oauth --auth-name crmOAuth
```

For the Managed App option, include the same managed reference in the
standalone connection command. It does not infer the app config's `auth.ref`:

```bash
fused-cli workspace service connect crm \
  --bucket default --user-ref customer-42 \
  --type oauth --auth-name crmOAuth \
  --auth-ref '${fused.bucket.auth.crm.crmOAuth}'
```

The command starts the service's consent flow. Replace the service and scheme
with the exact names configured in your workspace. If the service has just one
compatible OAuth/OIDC scheme, `--type` and `--auth-name` may be omitted
together. To inspect connection status without reading credentials:

```bash
fused-cli bucket connections default --service crm --user customer-42
```

The [CLI command reference](../COMMANDS.md) covers bucket and connection
commands, and the [config reference](../CONFIG_AS_CODE.md) covers `auth.ref`.
Engine refreshes eligible connected tokens; keep the same user reference when
reconnecting.

## Route app calls to that connection

Select the operation in the app's `services` config. Inside `execute`, bind a
reference to calls through a helper:

```ts
const crm = fused.forUserRef(input.userRef);
const customer = await crm.fetch({
  service: "crm",
  operation: "getCustomer",
  input: { id: input.customerId },
  selector: { authType: "oauth", authName: "crmOAuth" },
});
```

`fused.forUserRef(ref)` uses one reference for every service called through
that helper. When services use different references, bind each service:

```ts
const connected = fused.forServiceUserRefs({
  crm: input.crmUserRef,
  billing: input.billingUserRef,
});
const customer = await connected.fetch({
  service: "crm",
  operation: "getCustomer",
  input: { id: input.customerId },
});
```

Declare these user-reference fields in the app's Zod `input` schema, and
connect each reference in the same bucket as the app. The helpers reject a
conflicting `selector.endUserRef` on an individual call. They do not create a
connection or grant new provider permissions. A direct `fused.fetch` call can
instead set `selector.endUserRef` explicitly.

For a selected operation with an Engine pagination policy, a bound call can
also specify `pagination: { maxPages: 2 }`. The value must be a positive
integer below that operation's configured page limit; without it, the
operation's automatic pagination policy applies.

See the [`buildExecutionApp` guide](../EXECUTION_APPS.md) for the function's
contract and the [complete example](example.md) for the config and apply
commands.
