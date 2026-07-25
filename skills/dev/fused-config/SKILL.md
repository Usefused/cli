---
name: fused-config
description: "Use this skill when the task involves Fused config that isn't owned by one concept alone -- execution policy (rate limits, retries, pagination, a base_url override for a wrong/missing spec URL, event_extraction_path, incoming_webhook_config, and whether it's published to the Registry vs. only enforced locally in this workspace) nested inside a workspace service, or connection profiles (auth type, OAuth/OIDC resource discovery, dynamic request bindings) whether declared in a workspace file, a bucket, or directly in an OpenAPI/Postman spec via x-fused-connect. Trigger on 'execution policy', 'rate limit'/'retry config', 'pagination', 'base_url override', 'webhook verification'/'incoming_webhook_config', 'local override', 'connection profile', 'resource_discovery', 'binding', '${resource...}', or 'x-fused-connect'. For the service allowlist, SDK/MCP artifact selection, or bucket/secret storage themselves, read fused-workspace/fused-sdk/fused-mcp/fused-bucket instead."
---

# Cross-cutting runtime config: execution policy & connection profiles

These two configs aren't owned by workspace, SDK, MCP, or bucket alone --
they nest inside all of them (a workspace service's `execution_policy`, a
bucket's `service_config.<slug>.connect`, an SDK/MCP service's `auth`/
`connect` scoping) and, for connection profiles, can also be declared
directly in the source OpenAPI/Postman document at import time.

**For exact flags and subcommand syntax, always run `fused-cli <command>
--help` (or `fused-cli --readme` for the full CLI reference) rather than
guessing** — these files only cover the *shape* of each config domain so you
know which command/field to reach for. Flags drift faster than these files.

## Which reference file to read

Read only the file(s) relevant to the task at hand.

| Read this file | When the task involves |
|---|---|
| `reference/execution-policies.md` | Rate limits, retries, pagination, a base_url override, outbound webhook verification, per-version policy overrides, and the local-enforcement-vs-Registry-publish distinction |
| `reference/connection-profiles.md` | Auth type, OAuth/OIDC resource discovery, dynamic request bindings, profile ownership/provenance |
| `reference/openapi-postman.md` | Declaring the same connection profile directly inside an OpenAPI or Postman source document instead of workspace config |

For the service allowlist, SDK/MCP selection, or bucket/secret commands
themselves, read `fused-workspace`, `fused-sdk`, `fused-mcp`, or
`fused-bucket` -- this skill only covers the config that nests inside them.

## Where credentials belong

Never put a real credential value inline in a config file being committed to
source control. For a static `auth` credential (token/api_key/basic/mtls),
prefer a bucket secret (`fused-cli secret <service-slug> set` -- see
`fused-bucket`) over a local `_env`/`$VAR` handoff: a bucket secret is
resolved by the Engine itself, so it works the same whether `apply` runs
from a laptop or CI. Binding literal values have the same bucket-secret
alternative via `fused-cli value`. `client_id_env`/`client_secret_env` and
bare `$VAR` still work for those and are resolved locally at apply time, but
treat them as a fallback, not the default recommendation.

One real exception: OAuth `connect` app registration (`client_id`/
`client_secret`) has no bucket-secret path today -- `client_id_env`/
`client_secret_env` genuinely is the only way to keep those two fields out
of a committed file (see `reference/connection-profiles.md`).
