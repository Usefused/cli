# Engine execution REST API

Use this reference when the user wants to call an SDK operation through the
Engine HTTP API without `fused-cli sdk invoke` or a generated language client.
The Engine call remains SDK-scoped: it uses an immutable app ID and that SDK
family's execution token, never a provider credential or the CLI control key.

## Resolve the exact contract

1. Resolve the intended immutable SDK version:

   ```shell
   fused-cli sdk show <sdk-name@version-or-version-id> --json
   ```

2. Export its Engine-owned OpenAPI document:

   ```shell
   fused-cli sdk openapi <sdk-name@version-or-version-id> \
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
Idempotency-Key: {STABLE_KEY}  # required for Unified; optional for physical
```

The bearer value must be an SDK execution token created for the app family:

```shell
fused-cli sdk token generate <sdk-name-or-id> <token-name> --json
```

Capture the returned token once in a secret input or environment variable.
Never substitute `FUSED_API_KEY`, a License Key, an OAuth access token, an API
key from a bucket, or any other provider credential. Never put the token in the
URL, request body, logs, or committed files.

The app ID in the path is one exact immutable SDK version. A valid family token
cannot authorize a path belonging to a different SDK family.

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

## Unified operation request

A Unified request has `operation`, its declared input, an explicit nonempty
target list, and service-keyed selectors:

```json
{
  "operation": "research.createJiraTicket",
  "input": {
    "query": "Fused OAuth architecture",
    "issueSummary": "Review the OAuth findings"
  },
  "targets": [
    "jira_projects",
    "jira_issue_types",
    "nimble",
    "jira"
  ],
  "selectors": {
    "jira": {
      "end_user_ref": "customer-123"
    }
  },
  "target_pagination": {
    "jira_projects": {
      "max_pages": 3
    }
  }
}
```

Targets are binding keys selected for this call. Selectors are keyed by the
configured service selector namespace, not by provider IDs or response keys.
One service selector is reused by every selected binding that executes that
service. Do not add selector entries for binding aliases unless the exported
contract declares them as service keys.

`target_pagination` is keyed by selected binding target, not by the selector
service namespace. Each value accepts exactly `{ "max_pages": 3 }` with a
positive integer. Every requested value must be strictly below that target's
effective pagination-policy maximum; equality is rejected. Omit targets that
do not need a tighter bound. Unknown targets, non-paginated targets, and
provider continuation fields fail at the Engine boundary.

Unified execution requires exactly one bounded, stable `Idempotency-Key`:

```shell
curl --fail-with-body --silent --show-error \
  --request POST \
  --url "$FUSED_ENGINE_URL/v1/apps/$FUSED_APP_ID/executions" \
  --header "Authorization: Bearer $FUSED_SDK_TOKEN" \
  --header "Content-Type: application/json" \
  --header "Idempotency-Key: customer-123-research-ticket-42" \
  --data-binary @unified-request.json
```

Reuse that key for retries of the same logical request. Use a new key only for
a genuinely new operation; otherwise a mutating dependency may run twice.

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

A Unified Operation with root `output` returns that exact configured JSON value
on success. It has no `kind`, `data`, `results`, or `rollbacks` wrapper. For
example, the request above may return:

```json
{
  "issueId": "10068",
  "issueKey": "SCRUM-5",
  "issueTypeId": "10001",
  "projectKey": "SCRUM"
}
```

Validate this body against the operation's exported response schema. Do not
look for per-target statuses after a transformed success; Engine has already
mapped and validated the complete graph result.

When the Unified Operation has no root output, success instead uses the
all-settled wrapper with `kind: "unified"`, a `results` array containing one
entry per selected target, and optional `rollbacks`. Check every target's
`status` and `error_code`; do not infer whole-graph success from HTTP 200 alone.
An `auth_action` means the selected user needs a bounded connect, reconnect, or
resource-selection action before dispatch can continue.

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
- the token is an SDK family execution token and appears only in the header;
- the operation and input match the exported OpenAPI branch;
- physical calls use only `selector`, while Unified calls use `targets` and
  service-keyed `selectors`;
- physical `pagination` and Unified `target_pagination` contain only strict
  maximum-page bounds below the effective policy maximum;
- Unified and retried mutations use one stable idempotency key;
- every physical status was checked; for Unified, validate the configured root
  output or inspect every target result when root output is absent;
- no provider credential, access token, authorization URL, or other secret was
  printed or committed; ordinary provider results remain available for the
  user-requested inspection.
