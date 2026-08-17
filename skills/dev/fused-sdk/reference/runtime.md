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

## Shared gRPC channel

Every generated SDK opens exactly one gRPC channel to the Engine when `FusedSDK`
is instantiated. All services in the SDK share that channel over one HTTP/2
connection, so there is no per-service connection overhead.

The channel target is resolved in this order (first non-empty value wins):

1. `engine_url` / `engineUrl` passed directly to `FusedSDK(...)`.
2. `FUSED_ENGINE_GRPC_URL` environment variable.
3. `FUSED_ENGINE_URL` environment variable.
4. Default: `http://127.0.0.1:50051`.

Local development binds REST to port 8081 and gRPC to port 8082 by default.
Point the SDK at the gRPC port; the REST port responds with HTTP 405.

### Python

```python
import os
from src import FusedSDK

sdk = FusedSDK({
    "engine_url": os.environ.get("FUSED_ENGINE_GRPC_URL"),
    "token": os.environ["FUSED_SDK_TOKEN"],
})

async with sdk:
    result = await sdk.async_jira.issues.list()
```

### TypeScript

```typescript
import { FusedSDK } from 'your-sdk-package';

const sdk = new FusedSDK({
  engineUrl: process.env.FUSED_ENGINE_GRPC_URL,
  token: process.env.FUSED_SDK_TOKEN,
});

process.on('SIGTERM', () => sdk.close());
```
