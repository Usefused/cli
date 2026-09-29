# OAuth in Unified Apps

A Unified App uses a connection in its configured bucket. The TypeScript
receives a stable user reference, not provider tokens.

## 1. Connect the user

First, configure the service's OAuth application in the bucket. Then start
consent for the user the app will act for:

```bash
fused-cli workspace service connect crm \
  --bucket default --user-ref customer-42 \
  --type oauth --auth-name crmOAuth
```

Use the service and auth scheme selected in your app config. After the user
completes consent, check the connection:

```bash
fused-cli bucket connections default --service crm --user customer-42
```

## 2. Use the reference in TypeScript

Select `getCustomer` for `crm` in the app config. Declare `userRef` and
`customerId` in the app's Zod input schema, then call the selected operation
inside `execute`:

```ts
const customer = await fused.forUserRef(input.userRef).fetch({
  service: "crm",
  operation: "getCustomer",
  input: { id: input.customerId },
});
```

For the connection above, the request's `userRef` is `customer-42`. The helper
does not create a connection; it routes the call through the one already
stored in the app's bucket. For different user references per service, use
`fused.forServiceUserRefs({ crm: input.crmUserRef, billing: input.billingUserRef })`.

## OAuth application setup

If your workspace uses its own provider application, store its client pair
before running `workspace service connect`:

```bash
printf '%s' 'client_id=...;client_secret=...' |
  fused-cli secret set crm --bucket default \
    --type oauth --auth-name crmOAuth --value-stdin
```

If your service supports a Fused Managed App, set the service's `auth.ref` to
`"${fused.bucket.auth.crm.crmOAuth}"` in the app config and add
`--auth-ref '${fused.bucket.auth.crm.crmOAuth}'` to the connection command.
The standalone connection command does not infer the app's `auth.ref`. See the
[config reference](../CONFIG_AS_CODE.md) for the auth field shape.

For the complete config and deployment commands, see the
[Unified App example](example.md).
