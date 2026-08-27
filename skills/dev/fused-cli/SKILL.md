---
name: fused-cli
description: "Set up and operate fused-cli: install or authenticate it, inspect identity, log out, configure Engine access, discover/import Registry services, manage teams/people/RBAC/workspace access/personal credentials, select an owner team or domain skill, start an Engine, or diagnose connection failures. Trigger on 'fused-cli', 'whoami', 'logout', 'engine-url', 'api-key', 'find a service', 'import discover', 'team access', 'workspace access', 'workspace role', 'add user', 'personal credential', 'owner-team', 'required permissions', 'FUSED_LICENSE_KEY', 'create an MCP', or no running Engine. For an SDK requested inside a coding agent, use fused-sdk and never route through sdk prompt."
---

# fused-cli

`fused-cli` is the config-as-code interface to a Fused Engine: YAML/JSON files
under `.fused/` (or `-f <path>`) declare desired state, `plan` previews the
diff against the Engine, `apply` pushes it.

## Standing up an Engine (before you have one to connect to)

Everything below this section assumes an Engine is already running somewhere
reachable. If `fused-cli` is failing with a connection error, or the user
hasn't stood up an Engine yet at all, that's a different problem -- here's
how to actually start one.

Prerequisites are **PostgreSQL 16+** and a `FUSED_LICENSE_KEY` issued by
Fused -- get the key from the dashboard/account team at
[usefused.com](https://usefused.com) if you don't have one. Engine accepts one
standard Postgres DSN and creates or upgrades its own tables through it during
startup.

```shell
# Native binary. `latest/download` always resolves to the newest release; swap
# it for `download/<tag>` when a build must be repeatable.
OS=$(uname -s)
ARCH=$(uname -m | sed 's/aarch64/arm64/')
ARCHIVE="fused_${OS}_${ARCH}.tar.gz"
BASE="https://github.com/Usefused/engine/releases/latest/download"

curl -LO "${BASE}/${ARCHIVE}"
curl -LO "${BASE}/checksums.txt"
sha256sum --check --ignore-missing checksums.txt
tar -xzf "${ARCHIVE}"
mv fused-engine /usr/local/bin/

export FUSED_DATABASE_URL="postgres://fused:password@localhost:5432/fused?sslmode=disable"
export FUSED_LICENSE_KEY="<license key from the Fused dashboard>"
fused-engine start
```

Or via Docker. Two variants are published: `ghcr.io/usefused/engine:latest`
carries the embedded Admin UI, and `ghcr.io/usefused/engine:headless` is the
API/runtime without it. Both are moving tags; every release also publishes
`:<tag>` and `:<tag>-headless`, and production should pin one.

```shell
docker pull ghcr.io/usefused/engine:latest
docker run \
  -e FUSED_LICENSE_KEY="<license key>" \
  -e FUSED_DATABASE_URL="postgres://..." \
  -e FUSED_ENCRYPTION_KEY="$(openssl rand -base64 32)" \
  -p 8081:8081 -p 50051:50051 \
  ghcr.io/usefused/engine:latest
```

All three environment variables are required -- a container given only the
license key will not come up. `FUSED_ENCRYPTION_KEY` is the 32-byte AES key
protecting every at-rest secret; the value committed in `engine.yaml.example`
is publicly known and must never be used outside local throwaway dev. A
`ghcr.io` pull failing with `unauthorized` means the package isn't public yet
-- `docker login ghcr.io` with a token carrying `read:packages`.

Defaults: HTTP API + dashboard on `:8081`, SDK gRPC on `:50051`, plus an
embedded NATS server on `:4222` when no external `NATS_URL` is set (external
NATS is required to horizontally replicate Engine). Override ports via
`--port`/`--grpc-port`/`--webhook-port` flags, environment variables, or
`engine.yaml` (`--config` flag, default path `engine.yaml`) -- flags win over
env vars win over the config file. The license key itself resolves
`--license-key`, then `FUSED_LICENSE_KEY` in a local `.env`, then
`engine.license_key` in `engine.yaml`, then an inherited `FUSED_LICENSE_KEY`
process variable; `FUSED_API_KEY` is never a license source. The Engine
refuses to boot without a valid key and has no offline mode that bypasses the
Registry handshake.

Once it's up, point `fused-cli` at it (see "First-time setup" below), and
check `curl http://<engine-host>:8081/health` if you need to confirm it's
actually reachable.

## First-time setup

Every command that talks to an Engine needs two things. The Engine URL resolves
as flag -> environment -> local config -> error. The credential resolves as
flag -> saved login/local config -> `FUSED_API_KEY` -> `FUSED_LICENSE_KEY` ->
error.

- Engine URL: `--engine-url` flag, `FUSED_ENGINE_URL` env var, or
  `fused-cli config set engine-url <url>`
- Credential: `--key` flag, a credential saved by `fused-cli login` or
  `fused-cli config set api-key <key>`, `FUSED_API_KEY` personal/service
  fallback, or `FUSED_LICENSE_KEY` bootstrap Owner fallback.

```shell
fused-cli --engine-url https://engine.example.com login
fused-cli whoami
fused-cli logout
fused-cli config get engine-url
fused-cli config list
fused-cli config reset      # deletes the local config file entirely
```

`login` normally opens the selected Engine's sign-in page. Choose managed Fused
Auth (email, social, or enterprise SSO) or enter an existing Engine API key in
that page. The CLI locally generates the resulting `fsk_` credential, while the
Engine stores only its hash and binds it to the authenticated Engine subject;
the browser never receives the generated credential. Use `--no-browser` to
print the URL for a remote/headless session. `login --no-input` is valid because
browser approval needs no terminal prompt; combine it with `--no-browser` to
approve from another device. Unattended automation should continue using
`--key` or `FUSED_API_KEY`; do not leave a login poll waiting for human approval
in CI.

The local config stores `engine-url`, `api-key`, non-settable provenance
metadata, and expiry metadata for Engine-issued managed CLI credentials. A saved key
takes precedence over ambient credential variables; use `--key` for an
intentional one-command override.

Use `whoami` to inspect the effective credential selected by that precedence.
Agents must use `whoami --json` before a production mutation when more than one
of saved login, `FUSED_API_KEY`, or `FUSED_LICENSE_KEY` may be present; branch
on `local_credential_source` and the returned Engine identity instead of
guessing from the shell environment.
It prints only non-secret identity, account/workspace, source, authentication
method, and expiry data. Use `logout` to revoke the saved managed CLI login and
clear its local key/expiry metadata. Logout intentionally ignores `--key` and
credential environment overrides: revocation must target the saved credential
at its saved Engine. It preserves `engine-url`, retains local state when remote
revocation fails so the user can retry, and warns when `FUSED_API_KEY` or
`FUSED_LICENSE_KEY` will continue authenticating future commands. A manually
saved API key has no managed-login provenance and is left unchanged.

## Global flags

- `--key` / `--engine-url` -- override config/env for one invocation
- `-f, --file <path>` -- point at a specific config file, disabling `.fused/`
  directory discovery
- `--no-input` -- fail with remediation rather than opening a prompt;
  `CI=true` enables this automatically. Browser login is allowed because it
  polls an explicit approval URL without reading terminal input.
- `--timeout <duration>` -- bound Engine requests (default `1m`; reviewed
  `import plan` and `import apply` use `20m` unless this flag is set explicitly)
- `--request-id <request-id>` -- attach a non-secret audit correlation ID to every
  Engine request
- `--readme` -- print the full CLI reference and exit
- `--version`

SIGINT/SIGTERM cancel outstanding Engine requests. `CI=true` also disables the
release update check; set `FUSED_NO_UPDATE_CHECK=1` when only the update check
should be disabled. In non-interactive mode, replace prompt-oriented options
with explicit inputs (for example, use `import discover --select METHOD:/path`
and an explicit enrichment decision instead of opening selectors).

## Command surface drifts faster than these skills -- verify with `--help`

Every command list in this skill and its eight domain skills
(`fused-workspace`, `fused-sdk`, `fused-unified-operations`, `fused-mcp`,
`fused-webhook`, `fused-bucket`, `fused-config`, `fused-notifications`) reflects
the subcommands/flags that existed when that file was last
updated -- not a live source of truth. Before running any subcommand you
haven't just confirmed, run `fused-cli <command> --help` (e.g. `fused-cli
workspace service --help`, `fused-cli sdk token --help`) to see its actual
current flags and any subcommands these files don't list yet. `fused-cli
--readme` dumps the full reference for every command at once, useful when
you need the whole surface rather than one subcommand. Treat a command list
in any of these skills as a starting point for what's likely available, not
the final word on exact syntax.

Command grammar is action-first after each resource group because every action
is a real Cobra subcommand. For example, use `sdk service add <service-slug>`,
`sdk operation remove <service-slug> <operation-id...>`, `sdk token generate
<sdk-name-or-id> <token-name>`, and `mcp deactivate <mcp-name@version>`.
Running a group without an
action or passing an unexpected positional argument is an error; use the
group's `--help` output to choose the exact subcommand.

## Structured output for agents

Use `--json` whenever the confirmed command help exposes it. This includes
read/list/show/validate commands, plan commands, `sdk apply`, `sdk download`,
`sdk openapi`, `sdk token generate`, `sdk invoke`, and `sdk activity`. Do not scrape IDs, one-time tokens,
receipt fields, retry timing, or execution results from human output. A command
without `--json` must be treated as a human-only mutation unless its domain
skill documents a stable alternative.

`fused-cli sdk openapi <sdk-name@version-or-version-id>` resolves one exact
immutable SDK version with the ordinary control credential and `app.read`, then
GETs `/apps/{app_id}/openapi`. It always atomically writes YAML (or JSON with
`--format json`); `--operation` filters one exact physical or Unified name,
`--out` selects the file, and `--json` prints metadata rather than the document.
The export pins the real `POST /v1/apps/{app_id}/executions` route, whose Bearer
credential is the SDK-wide execution token—not the CLI control key. Output is
derived from at most 16 MiB of Engine JSON and its server URL is the configured
Engine. The CLI verifies the returned Version ID and real POST path before
replacement, including its matching `app_id` enum and request-branch count.
JSON metadata includes `operation_count` and `sha256:<64 lowercase hex>` of the
final rendered file bytes.

`fused-cli skill install` installs from the immutable `skills/<version>/`
snapshot shipped beside a release binary when available. This is intentionally
the first source so agent instructions work offline and stay aligned with the
installed CLI; network and embedded copies support older or source-built
installations.

## Build an SDK or MCP from a business goal

For an SDK requested inside a coding agent, use `fused-sdk`. It performs the
workflow locally and must never invoke `fused-cli sdk prompt`, which starts a
separate Fused agent. For MCP, or as a detailed manual reference, read
[reference/build-sdk-or-mcp.md](reference/build-sdk-or-mcp.md) and run its
workspace-first discovery workflow. Do not guess service slugs or operation
IDs.

## Permissions and team access

Use `team` and `user` for RBAC, and `workspace access` when a specific bucket
or SDK/MCP permission scope should be usable by everyone in the local workspace.
The access commands use the shared Engine `app` resource. Generic app-access
commands require the `SDK_ID` or `MCP_ID` shown by list commands; SDK-specific
commands may resolve an SDK name because the kind is already explicit. These
are immediate
access-management commands, not config-file fields. Read
[reference/access-management.md](reference/access-management.md) before
changing membership, roles, resource access, personal credentials, or resource
ownership. Prefer human names/slugs for kind-specific commands and use the
displayed `SDK_ID` or `MCP_ID` for generic app access. Use `team
eligible-owners` before planning a new SDK, MCP server, or webhook.

Permission-sensitive CLI flows must follow the denial protocol in that
reference: stop the blocked action, preserve drafts and plans, tell the user
the missing permission and resource, and name the narrowest relevant access
command for an authorised administrator. Never self-grant, switch credentials,
broaden a role, or retry with guessed authority. Reading access state requires
`access.read`; changing it requires `access.manage`, and assigning Owner also
requires `account.manage`.

## Every config file shares this shape

```yaml
apiVersion: fused/v1
kind: workspace   # or: sdk, mcp, webhook
```

`fused-cli plan` / `fused-cli apply` (no subcommand) operate across every
kind found under the target directory at once. `<kind> plan|apply` scopes to
one kind.

## Start or extend a config file

Use each resource's `init` command for a deterministic local draft; do not hand-build the
base fields or start `sdk prompt`:

```shell
fused-cli workspace init
fused-cli sdk init <name> --service '<service>=<version>' --operation '<service>=<operationId>'
fused-cli mcp init <name> --service '<service>=<version>' --select-all '<service>'
```

Result: the file is created under `.fused/` and reports its path. With no
service flags, the result is an editable empty skeleton. Use `-f <path>` for a
different target.

To add another selection, repeat the same shape with `--extend`. Result:
existing identity and selections remain, new entries are merged once, and an
already-present entry reports `unchanged`. A conflicting name, app version,
language, bucket, or service version stops before writing. Creation also stops
when the path already exists.

For SDK/MCP init or `--extend` with services, one batched Engine lookup adds
only missing required bucket-backed `server_variable` injections. Preserve
explicit injections and never duplicate workspace policy or native
`x-fused-connect` routing. With `--json`, read `generated_binding_count`; read
each service/variable/key from the written config and set its non-secret bucket
value. Values never appear in output. Read
`fused-config`'s `reference/openapi-postman.md` for the canonical example.

`--bucket` references an existing bucket only. Omitting it preserves Engine
default-bucket selection; this command never creates a bucket.

## How plan/apply staleness is caught

`plan` prints the Engine's complete plan summary and, with `--json`, includes
that summary and any notifications alongside the receipt fields. It hashes
the config file's content locally and sends that hash (not just a plan ID) to
the Engine, then writes a local receipt at
`.fused/.state/<config-key>.plan.json` (`config_key`, `plan_id`, that same
`source_hash`, and the normalized `engine_url`).

`apply` with no `--plan-id` preflights every selected config before applying
the first one. It rejects a receipt when its config hash changed, when it has
no `engine_url`, or when it targets a different Engine. Re-run `plan` against
the intended Engine to replace an invalid receipt; there is no legacy bypass
for an unbound or cross-Engine receipt. The all-config preflight prevents a
bad later receipt from being discovered only after an earlier config was
already applied. Passing an explicit `--plan-id` uses the current config hash
and active Engine directly, so use it only when the plan ID was captured from
that exact config and target. Each successfully applied resource also emits a
secret-safe OTEL audit event; a partial multi-config apply therefore retains
evidence for the resources that changed before a later failure. CLI-managed
config and receipt writes use validated same-directory atomic replacement and
preserve an existing file's permission mode.

`sdk apply --json` returns explicit apply, Registry-generation, and download
outcomes, including the one-time execution token when one was created. If a
stage fails, stderr remains the standard structured error envelope and
`error.details.stage` identifies `apply`, `generation`, or `download`; a
successful apply's SDK and Version IDs are included when a later stage failed.
`sdk download` is a distinct visible command, so prefer separate `sdk apply
--json` and `sdk download <name@version> --json` steps when automation needs
independent retry boundaries.

An active apply lease returns `plan_apply_in_progress` with HTTP `Retry-After`,
`error.details.retry_after_seconds`, and
`error.details.apply_lease_expires_at`. Respect that bounded delay; do not poll
aggressively or treat the 409 as a permanent lock. A changed revision instead
requires a new plan.

## Importing a provider API

Registry search and import are separate permissions: `service search --q`
requires `catalogue.read`; creating or updating a service with `import plan`,
`import apply`, or `import discover` requires `catalogue.import`; reading an import
session requires `catalogue.read`. A visible Registry result does not grant
`service.manage`, `service.consume`, or workspace activation. If import is
denied, preserve the source and plan, report the missing permission/resource,
and follow [reference/access-management.md](reference/access-management.md);
do not try another credential or grant access automatically.

For agent-readable discovery, pass `--json` to read-only inspection commands.
Start with `workspace services list --json`; use `service show --json` and
`service operations --json` when Registry-wide discovery is needed. Paginated
reads return `items`, `total`, `limit`, and `offset`. Operation lists remain a
deliberately bounded summary. Fetch one exact contract with `service operation
show <slug> <operation-name> --version <version> --json`, opting into request
and response bodies only when needed.

For a command using `--json`, treat a non-zero exit as a structured failure and
decode stderr as `{ "ok": false, "error": { ... } }`. Branch on `error.code`
and `error.retryable`; show `error.remediation` to the user and retain
`error.trace_id` for support. Engine errors may also include bounded reviewed
fields such as `error.details.server_detail`, dependency stage/status, or
apply-lease timing. Render those fields because this CLI talks to the user's
dedicated Engine, but never copy them into OTEL, analytics, or durable receipts.
Arbitrary proxy bodies and credential-shaped strings remain hidden.
Do not retry when `retryable` is false and do not parse the human error string
to recover fields already present in the object.

Import apply failures additionally carry the slim recovery contract
`code`, `phase`, `operation_id`, `commit_state`, and `recovery` inside
`error`. `commit_state` is `not_committed`, `committed`, or `unknown`; run the
exact `recovery` command instead of inferring safety from the HTTP status.
If Registry publication commits but Engine workspace activation fails, apply
returns `import_workspace_activation_failed` with `phase=workspace_activation`
and `commit_state=committed`. Treat the import as published, run the pinned
`workspace service add ... --service-id ... --version ... --apply` recovery,
and never replay the import merely because the composite command exited non-zero.
Engine authentication, authorization, and preflight-audit failures on import
routes use the same shape before Registry is reached. Authentication recovers
with `fused-cli login`; permission denial points an authorized workspace owner
to `fused-cli team access workspace set --help` instead of implying that
`whoami` can grant access. Transport timeout text never includes the raw Engine
URL; use the wrapped error only for local logs.

Use `import plan` / `import apply` when the source is already a machine-readable
specification. This path is reviewed and receipt-backed: `plan` parses/diffs,
then `apply` commits the exact planned source.

Treat a successful `import apply` as the completion signal for the contract
mutation. Allow endpoint semantic-search ranking to catch up asynchronously:
background enrichment retries transient failures, and a periodic repair sweep
recovers interrupted work. Do not re-run plan/apply or wait/poll merely for
that optional enrichment.

Large reviewed specifications receive a 20-minute plan/apply request budget by
default; an explicit global `--timeout` overrides it. Treat timeout, connection
reset, EOF, malformed/truncated 2xx, and an incomplete or identity-mismatched
success proof as non-retryable `import_apply_outcome_unknown`: the Registry
transaction may have committed even though the CLI never received an authoritative
response. A success proof must match the receipt operation and report
`applied`/`complete`/`committed` with complete service and version identities.
Run `fused-cli import status <operation-id>` using the plan ID from the receipt;
this is a read-only recovery command and never replays the mutation. A repeated
`import apply` with the exact same plan ID and review hash is also idempotent:
after commit it returns the atomically stored service/version result, while a
different review hash remains rejected. Do not create a fresh plan merely to
recover a lost response.

A `pending` status uses `commit_state=unknown` because a concurrent apply may
hold the plan lock while its writes are still invisible. It carries in-progress
poll guidance rather than a terminal `recovery` command; wait and read status
again without replaying apply. Terminal `failed` or
`IMPORT_RESULT_UNAVAILABLE` status includes a stable code and a non-looping
planning recovery command. A complete committed status alone prints the stored
service/version result.

Read import diagnostics by stable `code`, `disposition`, source format/version,
and JSON pointer. `captured` means the source meaning survived, `diagnosed`
means it was preserved or bounded but is not fully executable, and
`strict_error` means strict import must stop. Keep provenance as evidence; do
not infer execution support from human message text.

When a reviewed overlay supplies a provider fact absent from the source, keep
its non-secret research and acceptance evidence under the sibling FusedImport
repository's `acceptance/openapi-execution-contract/` area. Fused's
`contract-fixtures/` tree is reserved for provider-neutral executable contracts,
so future imports cannot quietly make production tests depend on one provider.
Record the source URL, stable section/anchor or quote summary, retrieval date,
reasoning, target contract field, and disposition. Do not claim a provider
baseline when its source specification is not checked in; record that absence
explicitly, and never place credentials, response bodies, or copied secrets in
the evidence folder.

Service versions and runtime snapshots carry `contract_version` plus
`required_capabilities`. The CLI is a transport mirror, not the compatibility
authority: never remove, default, or locally reinterpret these fields. An
unknown additive documentation field is harmless, but an unknown required
capability must remain visible so Registry/Engine can reject execution with
`execution_capability_required`. Re-importing or stripping the field is not a
remediation; use an Engine version that declares support or publish a contract
whose execution semantics are actually implemented.

Use `service versions` for bounded browsing and target resolution. Its GraphQL
selection contains only version identity, status, and the execution envelope;
do not add documentation, authentication, or policy objects to that list path.
Full version contracts belong only to workflows that actually round-trip them,
such as workspace synchronization, and their Registry reads must remain
set-based rather than issuing one singular resolver per service.

A 2xx apply through Engine also activates the exact imported version in that
workspace and persists the Registry slug used by later SDK/MCP config. If the
client receives a proxy timeout or other non-2xx response, do not infer
activation merely because Registry search can already see the committed row;
verify with `workspace services list -q <slug>` and use the normal workspace
plan/apply flow to activate it.

```shell
fused-cli import plan ./openapi.yaml --name "Billing API" --slug billing-api \
  --target endpoints
fused-cli import apply

# Attach a separately versioned webhook contract to one existing service
# version without rewriting the source artifact's info.version.
fused-cli import plan ./webhooks.openapi.yaml --name "Billing API" \
  --slug billing-api --target webhooks --destination-version 2026-08-19
fused-cli import apply

# Read-only recovery after a timeout or lost apply response. The plan ID is
# also the stable operation ID.
fused-cli import status <operation-id>

fused-cli import plan --url https://developer.example.com/asyncapi.yaml \
  --name "Events API" --slug events-api
fused-cli import apply

fused-cli import plan ./openapi.yaml --overlay ./provider.overlay.yaml \
  --name "Billing API" --slug billing-api
fused-cli import apply
```

`--overlay` accepts a local file only. The CLI transports its exact bytes as
`overlay_content`; never parse, normalize, merge, or hash the overlay in the
CLI or an agent wrapper. Registry is the sole owner of overlay validation and
canonicalization. It returns source and overlay hashes for audit information
and an opaque combined `review_hash` that authorizes apply. Registry also
returns an informational `source_bundle_hash` covering the exact root,
overlay, ordered captured dependencies, source format, and adapter version.
Apply replays that immutable bundle without fetching the provider again, so a
Registry restart or changed/unavailable source between plan and apply does not
change what was reviewed. Missing or corrupt bundle data fails closed; never
substitute a new download or treat `source_bundle_hash` as an apply credential.
Engine receives only the canonical Fused snapshot and never reads the import
bundle. The local receipt stores the review identity without storing the
overlay path or content, and apply
uses only `plan_id` plus `review_hash` without rereading either input. For a
direct apply, pass `--plan-id` with `--review-hash`; a source hash is not an
apply credential. Re-plan when a receipt lacks `review_hash`. JSON plan output
always includes `overlay_present`, including `false`, so audit consumers can
distinguish a reviewed no-overlay plan from a client that dropped the field.

Set `--target all|endpoints|webhooks` explicitly when the user's requested
contract scope is known; it defaults to `endpoints`. The plan persists that choice,
so apply uses the reviewed scope without another selection step. Never infer
`webhooks` merely because the source is AsyncAPI: use the user's intended
runtime contract. Re-importing one target on an existing provider version is
non-destructive to the other target: `endpoints` replaces endpoint rows and
endpoint/shared execution config while retaining webhook rows and verification
policy; `webhooks` replaces webhook rows and webhook policy while retaining
endpoint rows, authentication, routing, connection profiles, and core execution
policy. Use `all` only when one reviewed artifact authoritatively contains both.

For a webhook-only fragment whose `info.version` identifies the source artifact
rather than the destination service version, pass `--destination-version` with
`--target webhooks`. The destination must be an exact existing version of the
owned service; Registry rejects creation and cross-scope use. `--version` keeps
its existing meaning as a fallback when the source itself has no version. The
CLI sends these values separately, records only whether a destination was
present in OTEL, and rejects a successful response that does not echo the exact
destination so an older server cannot silently ignore the new field. The
destination must already use Registry's current execution-contract version;
partial attachment never relabels a retained older endpoint contract. Apply
preserves the destination's endpoint provenance, visibility/lifecycle status,
and endpoint surface, then rederives required capabilities from the final
retained-plus-webhook contract state.

Use service-level `public` as the pre-publication gate. Keep the owned service
`public: false` through private workspace validation; do not require its
version to be private, because versions default public and version-level
`public: true` cannot expose a private service. After validation passes, set
the service `public: true` and independently confirm the intended version is
not staged private. Never substitute version visibility for the service gate.

Supported spec inputs are OpenAPI 3.0/3.1, Swagger 2.0, Google Discovery REST
Descriptions, AsyncAPI, Postman Collection, WSDL, GraphQL SDL, and
introspectable GraphQL endpoints. Swagger 2 is converted through the shared
OpenAPI adapter. Google Discovery is mapped natively into the same canonical
endpoint contract: nested resources, JSON request/response schemas, Google
OAuth scopes, API-key shape, and pageToken/nextPageToken pagination are
preserved. Adapter v2 also publishes `access_type=offline`, `prompt=consent`,
and `refresh_token_required=true` as credential-free OAuth policy. A consumer
of a public Gmail or Drive service receives that durable-consent behavior
automatically; keep it out of SDK YAML and application code, which declare only
scopes and routing selectors. A receipt pinned to `google-discovery/v1` must be
discarded and planned again because apply cannot acquire v2 semantics after
review. Re-import an existing v1-backed service with the same slug and provider
version to publish a corrected internal revision, then reconnect existing
delegated users once against that revision. Reserved resource-name path
expansion preserves embedded `/` characters while escaping unsafe segment
characters. Media upload/download and resumable or multipart-related workflows
remain explicit plan diagnostics until their distinct wire protocols are
reviewed; do not treat those warnings as ordinary JSON upload support. Do not
pre-convert either format in the CLI or infer provider-specific fixes. `import plan`
reports the Registry-authoritative `source_format`, which apply re-detects and
must match before mutation. `--url` is still a spec URL here:
Registry first tries a bounded `GET`, then GraphQL introspection if the GET
response is not recognized as a spec. When the URL might be documentation,
use `import discover`; it owns specification-first resolution and the bounded
documentation fallback rather than overloading `import plan --url`.

For OpenAPI imports, path-level parameters are inherited by each operation and
an operation-level declaration of the same `(name, in)` pair wins.
HTTP `HEAD` operations are imported as first-class endpoints with their
parameters, security requirements, and stable operation identity preserved.
The same applies to `CONNECT`, `OPTIONS`, and `TRACE`. Effective server
selection follows operation, path, then root precedence, while path, query,
header, cookie, and whole-query parameters retain schema/content and
location/style-aware serialization metadata.
Request content retains every declared media representation in source order,
including exact vendor `+json`, form, multipart, text, XML, and binary media
types. When the source declares several executable representations without a
default, Registry selects one deterministically (`application/json`, vendor
`+json`, form, multipart, then schema-backed raw), emits
`multiple_request_media_types`, and persists that exact choice as
`default_media_type`. A reviewed import overlay can override the choice. Use
`--strict` when this inference must block publication instead. The alternatives
and complete Encoding Objects remain in the runtime contract.
Binary raw payloads and binary multipart parts cross the Fused boundary as
strict base64 strings and are decoded immediately before the provider request.
Raw bodies use the declared `body` payload argument; ambiguous extra body
arguments are rejected. There is no fallback that guesses JSON when request
content is absent or declares an unknown serialization.

One provider operation with a wire shape the runtime cannot reproduce is
omitted rather than weakening the whole imported service or publishing a
bodyless operation. Its `unsupported_oas_feature` diagnostic names the exact
operation and remains visible in `import plan --json`; `--strict` rejects the
plan. Executable and unknown `x-fused-*` extensions always remain exact and
strict. Very large sets of inert provider `x-*` metadata may be represented in
the plan by a deterministic count/hash summary so passive documentation does
not block otherwise executable operations.

Each imported schema carries canonical raw JSON, dialect, SHA-256 content hash,
one Registry-derived SDK/runtime projection, and bounded projection diagnostics.
Unions, composition, null, constraints, and all four `additionalProperties`
states stay exact in raw even when the compatibility projection is narrower.
CLI and SDK generators consume the projection and never reinterpret raw schema.
Responses retain exact/default/range status keys, descriptions, headers, every
media representation, examples, and links.

Imported authentication and routing metadata also remains provider-owned.
Operation security requirements are an ordered OR-of-AND structure: each
`security_requirements` entry is an alternative, while every named scheme
inside that entry must be satisfied together. An empty alternative explicitly
allows anonymous access. `service operations` renders that order instead of
collapsing it to one guessed auth method. `service show` exposes imported
server variables and a Basic scheme's `basic_password_mode` (`required`,
`optional`, or `empty`); these describe the provider contract and never carry
credentials. A missing server-variable default stays missing in CLI transport
rather than being filled locally.

OAuth2 schemes retain every named flow, refresh URL, and scope description;
legacy singular flow fields exist only when the source declared exactly one.
OAuth1 and HTTP challenge behavior is an explicit generic runtime strategy,
never a provider-name fallback. Ordered security alternatives may bind one
scheme to an exact declared server (for example an mTLS endpoint), while an
empty alternative remains anonymous. Workspace profiles choose `oauth2_flow`;
CLI never accepts tokens, signing keys, or challenge responses in that selector.

OpenAPI callbacks and 3.1 top-level webhooks retain their complete inbound
operation contracts, including callback parent/runtime expression. Links and
namespaced `x-*` values retain source provenance for review, but remain inert;
unknown `x-fused-*` execution keys are diagnosed and strict imports reject them.
Normalized tags, summaries, external docs, contact, license, and examples are
documentation metadata only and cannot select CLI or SDK execution behavior.

Phase 7 imports may carry structured inbound signature policies, post-auth
resource discovery, media-upload workflows, and reject-on-collision catalog
composition. Review these as credential-free execution contracts: signing
material appears only as a bucket `secret_ref`. CLI transports the normalized
shape but never resolves the reference, trusts inbound routing headers as the
callback URL, advances upload/discovery state, or composes specs; those actions
belong to the version-compatible Engine.

OpenAPI 3.2 preserves exact `QUERY` and custom method tokens, named servers,
`in: querystring` content, device-authorization metadata, response summaries,
tag hierarchy, and sequential or positional media encodings. Tag hierarchy is
documentation-only; CLI transports the other canonical fields but never
serializes a whole query string, frames a provider stream, or
interprets multipart positions. Registry resolves reusable media types; gated
`$self`, XML execution, external URI security names, and discriminator fallback
features must remain diagnostics rather than local approximations.
Device Authorization is not an executable flow: public admission must reject
it even when the source metadata was retained for a precise diagnostic.

`import plan` prints concise structured diagnostics when source information
cannot be represented exactly; `--json` returns the full diagnostic objects.
Use `--strict` when automation must reject any warning or error diagnostic
before a plan receipt is written. Informational diagnostics remain allowed in
strict mode. Correct the source or explicit import metadata rather than
silencing a diagnostic by guessing provider behavior.

Postman and other sources may omit a provider version. Supply the provider's
actual identity with `fused-cli import plan <path> --name <name> --slug <slug>
--version <version>`. If it is omitted, Registry returns the structured
`import_version_required` validation error; human output includes the
remediation and `--json` preserves its code/category/retryable fields. The CLI
must not prompt, invent a default, write a receipt, apply, or retry implicitly;
rerun `import plan` with the explicit version.

Use `import discover` when starting from a provider URL whose exact source
format is not already pinned. Registry validates machine-readable candidates
first and otherwise crawls a bounded same-site documentation frontier. It
shows exact operation and optional `x-fused-*` decisions, then stops at the
ordinary import-plan boundary. In an interactive terminal, operation selection
happens in the CLI, then the command opens the Engine's browser review and
waits for the user to finish it:

```shell
# Default: select operations, then review in the browser.
fused-cli import discover --url https://docs.example.com/api \
  --name "Docs API" --slug docs-api --version 1.0

# Remote shell: print the review URL and wait without opening it.
fused-cli import discover --url https://docs.example.com/api --no-browser \
  --name "Docs API" --slug docs-api --version 1.0

# Automation: make every decision with flags and do not use browser review.
fused-cli --no-input import discover --url https://docs.example.com/api \
  --name "Docs API" --slug docs-api --version 1.0 \
  --select GET:/users --reject-enrichment

fused-cli import apply
```

There is no implicit all-selection or service creation. Automation must pass
global `--no-input` (or set `CI=true`), choose `--all` or exact `--select`
flags, and accept offered proposal IDs with repeated `--accept-proposal` or
reject all optional enrichment with `--reject-enrichment`. This path sends
typed actions without opening or waiting for browser review. `--no-browser` is
different: it remains an interactive review, prints the URL instead of opening
it, and waits for the browser decision.

At `plan_ready`, `import discover` atomically writes
`.fused/.state/import.plan.json`. That single default file is the most recent
import plan, so the next command that writes that path replaces it. Use
`--receipt-out <path>` to retain another plan; that path is also atomically
replaced when it already exists. Apply the chosen file with `fused-cli import
apply --receipt <path>`. With no `--receipt`, the separate `fused-cli import
apply` command consumes the default receipt. Discovery itself does not mutate
Registry.

Resume interactively with `fused-cli import discover --session <session-id>`.
The session ID cannot be combined with service identity, source, worker, or
crawl flags. A `--no-input` resume still needs flags for each pending decision.

## End-to-end: wiring up an OAuth service from zero

Enable the service first:

```yaml
# .fused/workspace.yaml
apiVersion: fused/v1
kind: workspace
services:
  jira:
    versions:
      - version: "2026-07-01"
```

```shell
fused-cli workspace plan
fused-cli workspace apply
```

Then run `fused-cli bucket list`. Treat its results as visible through
`bucket.read`, not proven usable, and choose visible `default` or another
visible candidate. Run connect with that exact candidate; on `bucket.use`
denial, stop and report it instead of creating a fallback. Create only for an
explicit user request or stated enterprise, tenant, or environment isolation
requirement with workspace `bucket.manage`; creation does not grant
`bucket.use`, and self-granting is forbidden. Store the OAuth application pair
under the selected bucket, which will hold resulting connected-user tokens as
distinct records. The pair is an immediate secret mutation, not a
workspace.yaml field. This ordinary example uses `default`:

```shell
fused-cli bucket list
printf '%s' 'client_id=...;client_secret=...' | \
  fused-cli secret set jira --bucket default --type oauth --auth-name oauth2 --value-stdin
```

(Omit the value and add `-i` to be prompted per field instead.) Always provide
the complete pair atomically. Do not provide a redirect URI; Engine derives it
from its validated canonical public URL.

The `profile` block is omitted above because Registry has exactly one public
match for this version/auth_type -- see `fused-config` for when you'd need to
add one explicitly.

Start one user's OAuth session (see `fused-bucket`):

```shell
fused-cli workspace service connect jira --bucket default \
  --user-ref user_123 \
  --scope read:jira-work --scope write:jira-work --scope offline_access
```

For a standalone target that reuses another enabled service's application
registration, pass the independent selector
`--auth-ref '${bucket.auth.<source-service>.<source-auth-name>}'`. The standalone
initialization/debug command has no SDK or MCP identity selector and never
impersonates a generated runtime. Generated runtimes use the `auth.ref` in
their SDK/MCP app config.

If that user has more than one Jira site, confirm/set which one is default
(see `fused-bucket`):

```shell
fused-cli workspace connection resources list <connection-id> --json
fused-cli workspace connection resources set-default <connection-id> <resource-id>
```

Generate an SDK against that same bucket (see `fused-sdk`):

```yaml
# .fused/sdks/jira-sdk.yaml
apiVersion: fused/v1
kind: sdk
name: jira-sdk
version: "1.0.0"
language: typescript
bucket: default
services:
  jira:
    version: "2026-07-01"
    operations: [getIssue, createIssue]
    auth:
      type: "oauth"
      name: "oauth2"
      ref: "${bucket.auth.jira.oauth2}"
    connect: { scopes: ["read:jira-work", "write:jira-work", offline_access] }
```

```shell
fused-cli sdk plan
fused-cli sdk apply --download
```

Call it -- only Fused selectors ever cross the wire, never a raw provider
token or site URL:

```ts
const resources = await sdk.auth.listConnectionResources({ connectionId });
await sdk.Jira.issues.getIssue({
  issueIdOrKey: "OPS-1",
  fused: { endUserRef: "user_123", authType: "oauth", resourceId: resources[0].id },
});
```

Changing the file to `kind: mcp`, then running `fused-cli mcp plan` and
`fused-cli mcp apply` (see `fused-mcp`), deploys an Engine-hosted MCP server
against the same bucket rather than generating a package. The
workspace/bucket/connect steps above do not change.

## Which skill to read next

| Skill | Covers |
|---|---|
| `fused-workspace` | The service allowlist: enabling services/versions, execution policy, deprecations |
| `fused-sdk` | Generating a typed SDK package and constructing physical or Unified Engine execution API calls |
| `fused-unified-operations` | Defining one generated SDK operation across multiple services: mappings, dependencies, rollback, outputs, call-time targets, and connected-auth selectors |
| `fused-mcp` | Generating an Engine-hosted MCP server from selected operations (MCP cannot select webhooks) |
| `fused-webhook` | Registering inbound webhook ingress (`kind: webhook`) and attaching it to an SDK via `webhook_attachment` so that SDK receives delivery |
| `fused-bucket` | Credential containers: secrets, static values, registering a service's OAuth/OIDC app, starting an OAuth connect session, managing a connected user's resources |
| `fused-config` | Cross-cutting config owned by no single concept above: execution policy (rate limits/retries/pagination/outbound webhook verification, local-workspace-effect vs. Registry-publish), connection profiles (auth + dynamic request routing), and the OpenAPI/Postman `x-fused-connect` equivalent |
| `fused-notifications` | Reading, not authoring: what a `plan`/`apply` notification block means, `registry_*` vs. `workspace_*` types, severity, and how/where one gets marked read or dismissed (UI only, not `fused-cli`) |

Read only the skill(s) relevant to the task at hand -- don't load all eight
for a single-domain question.
