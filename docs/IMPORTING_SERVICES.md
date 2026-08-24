# Importing services

Use `import plan` and `import apply` for a machine-readable API description.
The Registry detects supported formats automatically: OpenAPI 3, Swagger 2,
Google Discovery, AsyncAPI, Postman Collection, WSDL, GraphQL SDL, and
introspectable GraphQL endpoints.

## Basic reviewed import

```bash
fused-cli import plan ./openapi.json \
  --name "Internal Billing API" \
  --slug billing-api \
  --target endpoints

fused-cli import apply
```

The plan is reviewable and the apply step commits the exact planned source.
`--target` accepts `all`, `endpoints`, or `webhooks` and defaults to
`endpoints`.

Plan and apply allow 20 minutes by default for large reviewed specifications.
Use `--timeout 30m` (or another explicit duration) when the deployment needs a
larger bound. If apply times out, its outcome is unknown because the Registry
may have committed before the Engine proxy lost the response. Do not replay the
same one-shot receipt automatically. Check `workspace services list -q <slug>`
and `service show <slug>`; if Registry committed but activation is absent, add
the exact version to workspace configuration and use normal workspace
plan/apply.

## URLs and other source types

```bash
# Remote specification; versionless formats need --version.
fused-cli import plan \
  --url https://developer.example.com/asyncapi.yaml \
  --name "Events API" \
  --slug events-api \
  --version 2026-08

# Google Discovery document.
fused-cli import plan \
  --url https://www.googleapis.com/discovery/v1/apis/drive/v3/rest \
  --name "Google Drive" \
  --slug google-drive

# Specification-first provider discovery with documentation fallback.
fused-cli import discover \
  --url https://docs.example.com/api \
  --name "Docs API" \
  --slug docs-api \
  --version 1.0

fused-cli import apply
```

Postman collections commonly omit `info.version`. If `--version` is also
omitted, planning stops with `import_version_required`, writes no receipt, and
suggests rerunning the same command with the provider's actual version. The CLI
never invents a version or retries the plan implicitly. Use `--json` to retain
the stable code, validation category, retryability, and remediation for CI or
agent workflows.

`import discover` first resolves and validates a machine-readable source. If no
unique valid source exists, Registry crawls a bounded same-site documentation
frontier and presents exact operations for review. An interactive terminal
shows the operation selector, opens the Engine's browser review when the draft
is ready, and waits while the browser submits the review. Pass `--no-browser`
to print the same review URL and wait without opening it, which is useful over
SSH or when the browser is on another device.

For flag-driven automation, use global `--no-input` and pass `--all` or repeat
`--select METHOD:/path`. Also repeat `--accept-proposal <id>` for the exact
optional enrichments to accept, or pass `--reject-enrichment`. This path makes
the decisions through typed CLI actions and neither opens nor waits for browser
review. `CI=true` enables the same non-interactive behavior.

The command stops at `plan_ready` and atomically writes
`.fused/.state/import.plan.json`. That single default path always represents
the most recently completed import plan, so the next command that writes to
that path replaces its existing receipt. Use `--receipt-out <path>` when
several plans must remain available; the chosen path is also atomically
replaced if it exists. Pass that file to `fused-cli import apply --receipt
<path>`. With no `--receipt`, `fused-cli import apply` reads the default
receipt. Discovery never creates a service or changes workspace activation;
only the separate apply command commits the reviewed plan.

Resume an interrupted interactive run with `fused-cli import discover
--session <session-id>`. A `--no-input` resume still needs the explicit
selection and enrichment flags for any decisions the session has not passed.
Resume reloads Registry's authoritative snapshot and rejects new service
identity, crawl, or worker inputs.

Google Discovery adapter v2 automatically publishes the credential-free OAuth
settings required for durable delegated access: `access_type=offline`,
`prompt=consent`, and `refresh_token_required=true`. Consumer Engines receive
those settings with a public Gmail or Drive service; SDK YAML should declare
only the required scopes and routing selectors. Do not copy these authorization
parameters into SDK configuration or application code.

Import receipts are pinned to their reviewed adapter version. A stored
`google-discovery/v1` receipt cannot be applied with v2 behavior; run `import
plan` again and apply the new receipt. Re-import an already published v1-backed
service with the same slug and provider version to create the corrected internal
revision, then reconnect existing delegated users once so their connections are
pinned to that revision and contain refresh material.

## Provider overlays

```bash
fused-cli import plan ./openapi.json \
  --overlay ./billing.overlay.yaml \
  --name "Billing API" \
  --slug billing-api
```

The overlay must be local. The CLI sends it unchanged; Registry owns parsing,
validation, canonicalization, and merging. Registry returns one combined
`review_hash`, and the receipt binds apply to the exact reviewed source and
overlay.

For direct apply, pass both `--plan-id` and `--review-hash`. Source and overlay
hashes are informational and are not apply guards.

## Diagnostics and strict mode

Import planning reports structured diagnostics whenever provider metadata
cannot be represented exactly.

- Use `--json` to consume diagnostic objects unchanged.
- Use `--strict` to reject warning or error diagnostics.
- Info diagnostics do not fail strict mode.

Treat diagnostic codes and disposition as the stable automation contract.
Diagnostic messages are written for people. The structured fields can include
source format/version, a bounded source pointer, operation/service scope,
disposition, required capability, and provenance.

## Execution compatibility

Every imported service version carries an execution-contract envelope.
`contract_version` identifies the wire shape and `required_capabilities`
declares behaviours an Engine must support. Registry publication and Engine
snapshot materialization fail closed if the target Engine cannot execute the
contract. Additive documentation fields do not require a capability.

The CLI transports this contract without deciding compatibility. It also
preserves reviewed webhook signatures, post-auth discovery, media-upload
workflows, multi-spec catalogues, custom HTTP methods, named servers, OAuth
metadata, and media encodings without implementing those behaviours locally.

## Identity and workspace activation

`--slug` is required and resolves within the caller's Registry account. A
matching slug updates that service; an unknown slug creates it. Importing the
same provider version creates a new internal revision, while a different
provider version creates that version.

The plan reports SDK and workspace usage of a changed version without blocking
apply. After a successful apply, Engine best-effort registers the service in
its workspace. If that registration fails, the Registry import remains valid;
use `fused-cli workspace service add <slug>` explicitly.

See the [command reference](COMMANDS.md) for every import flag and receipt
option.
