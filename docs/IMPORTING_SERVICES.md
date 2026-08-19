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

# Human-readable documentation discovery.
fused-cli import docs \
  --url https://docs.example.com/api \
  --name "Docs API" \
  --slug docs-api \
  --version 1.0
```

Postman collections commonly omit `info.version`. If `--version` is also
omitted, planning stops with `import_version_required`, writes no receipt, and
suggests rerunning the same command with the provider's actual version. The CLI
never invents a version or retries the plan implicitly. Use `--json` to retain
the stable code, validation category, retryability, and remediation for CI or
agent workflows.

`import docs` discovers endpoints from human-readable documentation. It imports
all discovered endpoints by default; use `--review` or `--select` to narrow the
result.

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
