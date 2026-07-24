---
name: fused-config
description: "Use this skill when the user wants to configure a Fused workspace using fused-cli — declaring or editing workspace service allowlists, connection policies (buckets, auth, OAuth/OIDC connect config), execution policies (rate limits, retries), or SDK/MCP artifact configs. Trigger on mentions of 'Fused workspace config', 'fused-cli', 'connection profile/policy', 'execution policy', 'rate limit'/'retry config' for a Fused service, or '.fused' config files (apiVersion: fused/v1). Do NOT use for unrelated API/SDK generation tools or generic YAML editing."
---

# Configuring Fused with fused-cli

Fused config files are YAML/JSON with `apiVersion: fused/v1` and one of three
`kind`s: `workspace`, `sdk`, `mcp`. `fused-cli` reads them from `.fused/` in
the current directory (or `-f <path>`), and every kind follows the same
lifecycle: edit the file, `fused-cli plan` (or `<kind> plan`) to preview, then
`fused-cli apply` (or `<kind> apply`) to push the change to the Engine.

Before editing a config, check for an existing `.fused/` directory in the
user's project — don't assume one is needed if there's no existing Fused
setup and the user hasn't said they want to start one.

**For exact flags and subcommand syntax, always run `fused-cli <command>
--help` (or `fused-cli --readme` for the full CLI reference) rather than
guessing** — this file only covers the *shape* of each config domain so you
know which command/field to reach for. Flags drift faster than this file.

## The four domains

**Workspace config** (`kind: workspace`, managed by `fused-cli workspace ...`)
is the allowlist: which services and versions are enabled, plus per-service
`buckets` (credential containers). Top-level shape:

```yaml
apiVersion: fused/v1
kind: workspace
services:
  <service-slug>:
    versions: ["v1"]
    execution_policy: {...}      # see Execution policy below
    runtime_config: {...}        # base_url, auth, connect, webhooks, pagination
    connection_profiles: [...]   # raw list, Engine-validated — see Connection policy
buckets:
  <bucket-name>:
    service_config:
      <service-slug>:
        auth: {...}
        connect: {...}
deprecations:
  - service_id: "..."
    effective_at: "YYYY-MM-DD"
```
Commands: `fused-cli workspace plan|apply`, `workspace services list`,
`workspace service <slug> [versions|operations|webhooks|add|connect|remove|deprecate|version]`.

**Connection policy** — how a service authenticates and how end-user OAuth
connect flows work. Two related but distinct things:
- `runtime_config.auth` / `bucket.service_config.<slug>.auth` (`AuthConfig`):
  `auth_type` plus one of `username`/`password`, `token`, `api_key`, or
  `cert`/`key`.
- `runtime_config.connect` / `bucket.service_config.<slug>.connect`
  (`ConnectConfig`): OAuth/OIDC app registration — `client_id(_env)`,
  `client_secret(_env)`, `redirect_uri`. Start an actual user connect session
  with `fused-cli workspace service <slug> connect --user-ref <ref>`.
- A Registry-level immutable connection profile baseline (distinct from the
  per-workspace override above) is published with
  `fused-cli connection-profile set <service-ref> --version <v> --auth-type <type> --file <path>`.
- Per-user connected resources: `fused-cli connection resources list|set-default|rediscover <connection-id>`.

**Execution policy** (`ExecutionPolicy`, nested under a service or a specific
`version_policies` entry) controls rate limiting and retries:

```yaml
execution_policy:
  public: false
  rate_limit:
    strategy: "fixed_window"       # engine-defined strategies
    requests_per_second: 10
    requests_per_minute: 300
  retry:
    strategy: "exponential_backoff"
    max_retries: 3
    backoff_ms: 500
```
Set `reset: true` to clear an inherited policy back to defaults on apply.

**SDK / MCP artifact config** (`kind: sdk` or `kind: mcp`, both share the
`ArtifactConfig` shape, managed by `fused-cli sdk ...` / `fused-cli mcp ...`):

```yaml
apiVersion: fused/v1
kind: sdk            # or: mcp
name: my-sdk
version: "1.0.0"
language: typescript # sdk only
bucket: default
services:
  <service-slug>:
    version: "v1"
    operations: ["listUsers", "createUser"]   # or select_all: true
    webhooks: ["repo-a"]
    auth:    { type: "api_key" }
    connect: { scopes: ["read:users"] }
```
Commands: `fused-cli sdk plan|apply|validate|download|sync|token`,
`fused-cli sdk service <slug> [add|remove]`, `sdk service <slug> operation
[add|remove]`, `sdk service <slug> webhook [add|remove]`; `fused-cli mcp
plan|apply|validate|list`.

## Workflow reminders

- `fused-cli plan` / `apply` (no subcommand) operate across **all** config
  kinds found under the target directory at once; `workspace|sdk|mcp
  plan|apply` scope to one kind.
- Applying a workspace config against a production Engine can activate or
  deactivate services workspace-wide — the CLI itself warns when the target
  Engine reports `environment=production`; call this out to the user before
  running `apply` if you see that warning.
- Secrets and buckets: `fused-cli secret <service-slug> set|remove`,
  `fused-cli bucket <name> create|show|services|secrets|values|connections|sdks`,
  `fused-cli value <bucket-id> set|list|remove`. Never put real credential
  values inline in a config file being committed to source control — use
  `_env` fields (`client_id_env`, `client_secret_env`) or bucket secrets.
- `fused-cli config set engine-url|api-key` must be configured (or
  `FUSED_ENGINE_URL`/`FUSED_API_KEY` set) before any command that talks to an
  Engine will work.
