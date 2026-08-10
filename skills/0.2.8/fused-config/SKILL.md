---
name: fused-config
description: "Use this skill when the task involves Fused config that isn't owned by one concept alone -- execution policy (rate limits, retries, pagination, a base_url override for a wrong/missing spec URL, event_extraction_path, incoming_webhook_config, and whether it's published to the Registry vs. only enforced locally in this workspace) nested inside a workspace service, or connection profiles (auth type, OAuth/OIDC resource discovery, dynamic request bindings) whether declared in a workspace file, a bucket, or directly in an OpenAPI/Postman spec via x-fused-connect. Trigger on 'execution policy', 'rate limit'/'retry config', 'pagination', 'base_url override', 'webhook verification'/'incoming_webhook_config', 'local override', 'connection profile', 'resource_discovery', 'binding', '${resource...}', or 'x-fused-connect'. For SDK package or MCP server selection, or bucket/secret storage, read fused-workspace/fused-sdk/fused-mcp/fused-bucket instead."
---

# Cross-cutting runtime config: execution policy & connection profiles

These two configs aren't owned by workspace, SDK, MCP, or bucket alone --
they nest inside all of them (a workspace service's `execution_policy`, a
bucket's `service_config.<slug>.connect`, an SDK/MCP service's `auth`/
`connect` scoping) and, for connection profiles, can also be declared
directly in the source OpenAPI/Postman document at import time.

A provider document may retain a protocol-relative server such as
`//tenant.example.com`; Registry preserves it as source metadata, while Engine
requires an absolute workspace `base_url` override or trusted forced
`${resource.base_url}` binding before execution. Engine never guesses the
missing scheme.

Provider documentation and similarly named vendor extensions are evidence,
not executable policy. During curation, prefer standard OpenAPI security and
server fields, then translate only verified behavior that OpenAPI cannot
express into the exact `x-fused-rate-limit`, `x-fused-retry`,
`x-fused-pagination`, or `x-fused-connect` contract. Registry deliberately
keeps keys such as `x-retry` or `x-provider-pagination` inert so ambiguous
provider metadata cannot silently change Engine behavior.

Keep a structured evidence manifest beside each curated service. For OAuth,
rate-limit, retry, pagination, and routing decisions, retain the official URL,
page heading or source-spec pointer, curated target, documented facts, and the
mapping decision. Mark omitted executable policies explicitly and distinguish
provider limits from Fused-selected safety bounds; a bare link in research
notes is not enough to reproduce or review the configuration.

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
For what happens to every *other* workspace (a separate Engine deployment --
Engine is single-workspace-per-deployment, so "other workspace" always means
"someone else's Engine," never a second workspace inside this one) when you
publish one of these with `public: true` (or publish a connection profile
baseline), read `fused-notifications`.

## Where credentials belong

Never put a real credential value inline in a config file being committed to
source control. For a static `auth` credential (token/api_key/basic/mtls),
use a bucket secret (`fused-cli secret set <service-slug>` -- see
`fused-bucket`): it's resolved by the Engine itself, so it works the same
whether `apply` runs from a laptop or CI. Binding literal values have the
same bucket-secret path via `fused-cli value`.

OAuth `connect` app registration's `client_secret` uses this same
bucket-secret path -- `${bucket.secret.<key>}` -- resolved by the Engine at
apply time against this connect config's own bucket, same as `auth` (see
`reference/connection-profiles.md`).

## Permissions and team access

Follow the lifecycle of the command that owns the config:

- A local workspace execution-policy or profile change needs `service.manage`
  to plan and `workspace.update` plus `service.manage` to apply. Bucket changes
  also need `bucket.manage`; secret material needs `credentials.manage`.
- Publishing a Registry connection-profile baseline needs `service.manage` and
  `credentials.manage`. Publishing owner-only service policy remains owner-only
  even when the caller otherwise has workspace access.
- Importing `x-fused-connect` through OpenAPI/Postman needs `catalogue.import`;
  reading the import session needs `catalogue.read`.
- `connect set` needs `credentials.manage` and `service.consume`; starting a
  connect session needs `connection.manage`, `bucket.use`, and
  `service.consume`.

The narrow remediation commands are usually:

```shell
fused-cli team access service grant <team> <service> use|manage
fused-cli team access bucket grant <team> <bucket> use|manage
fused-cli team access workspace set <team> admin   # only for a missing workspace-level permission
```

On denial, stop the blocked action, preserve the config/source and plan, and
tell the user the missing permission and resource. Never self-grant, switch
credentials, broaden scope, or retry with guessed authority. Do not run access
commands unless explicitly requested and authorised. Read the `fused-cli`
skill's `reference/access-management.md` for the full matrix, then use
`fused-workspace` or `fused-bucket` for the owning command's exact workflow.
