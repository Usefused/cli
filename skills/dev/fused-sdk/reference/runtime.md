# SDK runtime reference

## Timeout contract

Generated clients expose `ExecutionTimeoutError` in TypeScript and Python.
`timeoutMs` / `timeout_ms` defaults to 30 seconds and bounds `Connect`, buffered
execution, and the wait for an SSE stream's first event. It is a caller-side
deadline; it does not configure the service owner's Engine policy.

For long-lived SSE operations, configure `streamIdleTimeoutMs` /
`stream_idle_timeout_ms` for the gap between events and
`maxStreamDurationMs` / `max_stream_duration_ms` for an optional total stream
cap. If neither stream setting is supplied, an established stream may remain
open until the provider, Engine, consumer, or Engine execution policy closes
it. Stopping iteration cancels the underlying gRPC call.

Service owners set the independent Engine cap in workspace configuration:

```yaml
services:
  jira:
    execution_policy:
      timeout_ms: 45000  # 1..86400000; omitted means no Engine cap
```

An exact service-version execution policy overrides the service-level timeout.
If the Engine policy expires, the client receives `ExecutionTimeoutError` with
code `execution_timeout` and the enforced `timeout_ms`. If the caller deadline
expires first, the client still receives the same typed class with its own
configured budget; Engine telemetry must not misattribute it to policy expiry.

## Pagination invocation bounds

Generated physical calls may tighten the Engine-owned page limit without
copying pagination policy into application code:

```typescript
const result = await sdk.YourService.yourListMethod({
  pageSize: 100,
  fused: {
    endUserRef: 'customer-123',
    pagination: { maxPages: 5 },
  },
});
// Every generated method returns { ok, status, data, error }.
if (result.ok) use(result.data);
```

```python
result = await sdk.AsyncYourService.your_list_method(
    {"pageSize": 100},
    options={"fused": {
        "end_user_ref": "customer-123",
        "pagination": {"max_pages": 5},
    }},
)
# Every generated method returns {"ok", "status", "data", "error"}.
if result["ok"]:
    use(result["data"])
```

The call still produces one aggregate response after one logical Engine
execution. The requested bound must be strictly below the effective service
policy maximum; equality is rejected. It cannot carry continuation tokens,
paths, origins, templates, or next URLs.

Generated Unified calls keep pagination separate from routing selectors:

```typescript
await sdk.unified.search.run(input, {
  targets: ['gmail', 'drive'],
  selectors: { gmail: { endUserRef: 'customer-123' } },
  pagination: { gmail: { maxPages: 5 }, drive: { maxPages: 3 } },
});
```

Python uses the equivalent keyword-only
`pagination={"gmail": {"max_pages": 5}}`. Unknown targets, targets without an
effective pagination policy, zero, and values equal to or above the effective
limit fail at the Engine boundary. Invocation bounds never belong in
`sdk.yaml`.

## Shared gRPC channel

Every generated SDK opens exactly one gRPC channel to the Engine when `FusedSDK`
is instantiated. All services in the SDK share that channel over one HTTP/2
connection, so there is no per-service connection overhead.

The channel target is resolved in this order (first non-empty value wins):

1. `grpc_url` / `grpcUrl` passed directly to `FusedSDK(...)`.
2. `FUSED_ENGINE_GRPC_URL` environment variable.
3. `FUSED_ENGINE_URL` environment variable.
4. Default: `http://127.0.0.1:50051`.

Engine binds REST to `8081` (`--port`) and gRPC to `50051` (`--grpc-port`).
Point the SDK at the gRPC port; the REST port responds with HTTP 405.

Step 3 is the trap: `FUSED_ENGINE_URL` is the *HTTP* variable `fused-cli` uses.
If it is set and no `grpc_url` is passed, the SDK speaks gRPC at the REST port
and gets that 405. Set `FUSED_ENGINE_GRPC_URL` explicitly rather than relying on
the fallback. Running two Engines side by side (as the e2e harness does) offsets
both ports together -- `8082` with `50052` -- so the HTTP/gRPC split still holds.

### Python

```python
import os
from src import FusedSDK

sdk = FusedSDK({
    "grpc_url": os.environ.get("FUSED_ENGINE_GRPC_URL"),
    "token": os.environ["FUSED_SDK_TOKEN"],
})

# One client carries both surfaces: sdk.Jira is synchronous,
# sdk.AsyncJira is not. Resources and methods are snake_case.
async with sdk:
    result = await sdk.AsyncJira.issues.list()
```

### TypeScript

```typescript
import { FusedSDK } from 'your-sdk-package';

const sdk = new FusedSDK({
  grpcUrl: process.env.FUSED_ENGINE_GRPC_URL,
  token: process.env.FUSED_SDK_TOKEN,
});

process.on('SIGTERM', () => sdk.close());
```
