# Engine execution REST API

Use this reference when the user wants to call an SDK operation through the
Engine HTTP API without `fused-cli sdk invoke` or a generated language client.
The Engine call remains SDK-scoped: it uses an immutable app ID and that SDK's
execution token, never a provider credential or the CLI control key.

## Resolve the exact contract

1. Resolve the intended immutable app version. For a generated SDK, inspect it
   with:

   ```shell
   fused-cli sdk show <sdk-name@version-or-version-id> --json
   ```

2. Export its Engine-owned OpenAPI document. Use `sdk openapi` for a generated
   SDK:

   ```shell
   fused-cli sdk openapi <sdk-name@version-or-version-id> \
     --out engine-execution.openapi.yaml \
     --format yaml \
     --json
   ```

   For a direct REST API created with `fused-cli init --api`, or a `kind: sdk`
   configuration with `generate: false`, use its public API command instead:

   ```shell
   fused-cli api openapi <api-name@version-or-version-id> \
     --out engine-execution.openapi.yaml \
     --format yaml \
     --json
   ```

3. Read the matching request branch under
   `POST /v1/apps/{app_id}/executions`. Use its exact `operation` value, input
   schema, selectors, and app ID. Do not reconstruct provider paths or call a
   provider URL directly.

The exported document is the request authority for that SDK version. If the
local example and exported schema differ, follow the exported schema.

## Authentication and endpoint

Send exactly these request properties:

```text
POST {ENGINE_URL}/v1/apps/{APP_ID}/executions
Authorization: Bearer {SDK_EXECUTION_TOKEN}
Content-Type: application/json
Idempotency-Key: {STABLE_KEY}  # optional for physical calls
```

The bearer value must be an execution token created for that SDK:

```shell
fused-cli sdk token generate <sdk-name-or-id> <token-name> --expires-in 4h --json
```

Capture the returned token once in a secret input or environment variable.
Use `--expires-in` when access is only required for a bounded test window; the
token still authorizes the SDK's full operation surface until expiry.
Never substitute `FUSED_API_KEY`, a License Key, an OAuth access token, an API
key from a bucket, or any other provider credential. Never put the token in the
URL, request body, logs, or committed files.

The app ID in the path is one exact immutable SDK version. A valid SDK token can
authorize active or deprecated versions of that same SDK, but cannot cross into
a different SDK.

## Physical operation request

A physical request has `operation`, an object-valued `input`, and at most one
`selector`:

```json
{
  "operation": "listProjects",
  "input": {
    "maxResults": 50
  },
  "selector": {
    "end_user_ref": "customer-123"
  },
  "pagination": {
    "max_pages": 5
  }
}
```

The optional physical selector supports only:

```json
{
  "environment": "production",
  "end_user_ref": "customer-123",
  "auth_type": "oauth",
  "auth_name": "JiraOAuth",
  "resource_id": "00000000-0000-0000-0000-000000000000"
}
```

Include only the fields needed to disambiguate the stored runtime state. If the
SDK selection and connected user resolve one auth scheme and one resource,
`end_user_ref` alone is sufficient. Do not invent `auth_name` or `resource_id`
merely because the selector supports them.

Physical requests must not contain `targets` or `selectors`. Their `input`
must be a JSON object even when it is empty. Optional `pagination` accepts
exactly one positive integer field, `max_pages`, and only for an operation with
an effective Engine pagination policy. It may tighten that policy for this
invocation; it cannot contain provider cursors, offsets, URLs, paths, or
templates. The requested value must be strictly below the effective policy
maximum, so equality is rejected.

Example call:

```shell
curl --fail-with-body --silent --show-error \
  --request POST \
  --url "$FUSED_ENGINE_URL/v1/apps/$FUSED_APP_ID/executions" \
  --header "Authorization: Bearer $FUSED_SDK_TOKEN" \
  --header "Content-Type: application/json" \
  --data-binary @physical-request.json
```

For a mutating physical operation, provide one stable `Idempotency-Key` across
retries when the exported operation contract supports safe idempotency. Never
blindly replay a timed-out provider mutation with a newly generated key.

## Attached Unified App request

Use the immutable SDK version and its execution token to invoke an approved
`unified_apps` attachment. The attachment alias selects the hosted capability;
its authored schema defines `input`:

```json
{
  "operation": "unified_app:customer_lookup",
  "input": { "email": "customer@example.com" }
}
```

Use only an alias admitted by this SDK's configuration. The example input must
be replaced with values matching that app's authored schema. Engine validates
the consumer's access before running the app. Inspect the returned execution
`status` and `output`; failures do not authorize an automatic retry.

## Interpret the response

A successful physical wrapper has this shape:

```json
{
  "app_id": "00000000-0000-0000-0000-000000000000",
  "operation": "listProjects",
  "kind": "physical",
  "status_code": 200,
  "results": [{}]
}
```

The HTTP wrapper can be successful while `status_code` records the provider
status. Inspect both the Engine HTTP status and the physical status field.

Request-level failures use:

```json
{
  "error": {
    "code": "operation_not_found",
    "message": "operation is not defined for this app",
    "details": {}
  }
}
```

Branch on the stable `error.code` and HTTP status. Do not retry authentication,
selector, invalid-request, operation-not-found, or ambiguity failures without
changing the relevant input or authorization state. Preserve the Engine trace
or receipt identifier when returned, but never log request authorization or
connected-provider credentials.

## Completion check

Before reporting the call complete, verify:

- the path app ID matches the intended immutable SDK version;
- the token is an SDK execution token and appears only in the header;
- the operation and input match the exported OpenAPI branch;
- physical calls use only `selector`;
- `pagination` contains only a strict maximum-page bound below the effective
  policy maximum;
- retried mutations use one stable idempotency key;
- the physical status was checked;
- no provider credential, access token, authorization URL, or other secret was
  printed or committed; ordinary provider results remain available for the
  user-requested inspection.
