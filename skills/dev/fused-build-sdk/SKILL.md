---
name: fused-build-sdk
description: "Build and set up a typed Fused SDK directly from a business goal inside a coding agent such as Codex, Claude Code, Cursor, Windsurf, or Antigravity. Use when the user asks their IDE agent to build, create, generate, configure, or wire a Fused SDK and has not supplied a complete kind: sdk config. This is the local-agent path: never invoke fused-cli sdk prompt, because that command starts a separate Fused agent."
---

# Build a Fused SDK in an IDE agent

Turn the user's goal into a validated SDK config, then plan and, when
authorised, apply and download it. Run the workflow yourself with deterministic
CLI commands. Do not delegate the work to another agent.

## Agent boundary

Never run `fused-cli sdk prompt` from an IDE-agent task. That command is the
alternative entry point for a user who wants the Fused-hosted agent to perform
the workflow. Calling it here would run two agents, duplicate discovery, spend
extra tokens, and make ownership of edits unclear.

Use `fused-cli sdk --help` and its concrete subcommands instead. Do not call an
AI, chat, prompt, or intent endpoint as a fallback.

## Keep context small

Maintain one compact working-facts record in the task and update it as facts
are learned:

- CLI version, Engine URL/identity, and connectivity
- SDK name, exact version, and language
- config paths already present in the repository
- selected service slugs, versions, and operation IDs
- bucket and personal or team ownership
- unresolved secret entry, OAuth consent, permission, or production approval

Reuse these facts; do not repeat successful checks. Read local config before
remote discovery. Inspect only the `--help` branch needed for a command. Do not
load every sibling skill up front:

- use `fused-workspace` only when service activation is required;
- use `fused-bucket` only when bucket credentials or OAuth are unresolved;
- use `fused-sdk` for detailed config, immutable-version, token, or runtime
  questions;
- use `fused-cli` and `reference/access-management.md` only for setup or access
  remediation.

## 1. Establish local and Engine context

Search narrowly for existing Fused files and project language markers. Prefer
an existing SDK config and reuse facts already supplied by the user.

```shell
rg --files -g '.fused/**' -g 'fused*.yaml' -g 'package.json' -g 'tsconfig.json' -g 'pyproject.toml' -g 'requirements*.txt' -g 'go.mod'
fused-cli --version
fused-cli config list
fused-cli whoami
```

`config list` masks the saved API key. Never inspect `.env` contents, print
environment values, or put credentials in a config, command argument, or
transcript. If setup is incomplete, use `fused-cli`; otherwise continue.

Infer TypeScript, Python, or Go from unambiguous project markers and state the
assumption. Ask only when language, provider, bucket, ownership, or a
production-changing choice would materially alter the result. Use `1.0.0`
only for a genuinely new SDK. Existing versions are immutable; changed scope
requires a new explicit version.

## 2. Resolve services and operations

Search visible workspace services first:

```shell
fused-cli workspace services list --q "<provider or product>"
fused-cli workspace service versions <slug>
fused-cli workspace service operations <slug> --version <version> --q "<capability>"
```

Only if there is no suitable visible workspace service, query the Registry:

```shell
fused-cli service search --q "<provider, product, or capability>"
fused-cli service versions <slug>
fused-cli service operations <slug> --version <version> --q "<capability>"
```

Never guess a slug, version, or operation ID. Resolve ambiguity with the user
when candidates have materially different providers or behavior. Prefer a
stable public version. Select only operations required by the stated goal;
never use `select_all: true` unless the user explicitly wants the full API.

If Registry fallback found a service that is not enabled, create or update the
local workspace config, validate it, and plan it using `fused-workspace`.
Apply activation only when completing setup is authorised and no production
or permission warning requires user action.

## 3. Resolve bucket, authentication, and ownership

List buckets once and reuse a clearly appropriate existing bucket:

```shell
fused-cli bucket list
```

Use `fused-bucket` when credentials, OAuth/OIDC, or connections are unresolved.
Secrets must enter through stdin or an interactive prompt. Pause for browser
consent or secret entry; never fabricate or expose credential material.

For requested team ownership, preflight before SDK planning:

```shell
fused-cli team eligible-owners
fused-cli team build-access <team> --resource service
fused-cli team build-access <team> --resource bucket
```

Do not silently replace a requested team with personal ownership.

## 4. Author the smallest SDK config

Create or patch `.fused/sdks/<sdk-name>.yaml`:

```yaml
apiVersion: fused/v1
kind: sdk
name: <sdk-name>
version: "<exact-version>"
language: <typescript|python|go>
bucket: <bucket-name>
services:
  <service-slug>:
    version: "<exact-service-version>"
    operations:
      - <exact-operation-id>
```

Preserve unrelated user edits. Add `auth`, narrowed `connect.scopes`,
injections, or webhook attachment only when discovery proves they are needed;
use `fused-sdk` for those shapes. Provider credentials never belong here.

## 5. Validate, plan, and complete

Use the exact file when more than one config could be selected:

```shell
fused-cli sdk validate -f .fused/sdks/<sdk-name>.yaml
fused-cli sdk plan -f .fused/sdks/<sdk-name>.yaml
fused-cli sdk apply -f .fused/sdks/<sdk-name>.yaml --download
```

Review the plan before apply. Apply only when the user's request includes
completing setup and the plan introduces no unresolved production, ownership,
credential, or permission decision. Never use `sdk prompt` to recover from a
validation or plan error; fix the local config or report the blocker.

## Permissions and team access

Planning a new SDK requires `app.create`, `service.read`, and `bucket.read`;
an update requires `app.manage` plus dependency reads. Apply also requires
`service.consume` and `bucket.use`. Download requires `app.read`; SDK token
management requires `app.tokens.manage`.

On denial, stop after the first failed action, preserve the config and plan,
and report the exact missing permission and resource. Never self-grant, switch
credentials, broaden access, or retry with guessed authority. Use the
`fused-cli` skill's `reference/access-management.md` only to identify the
narrowest remediation for an authorised administrator.

## Completion contract

Report the SDK name/version/language, config path, selected service versions
and operation IDs, bucket, ownership, validation/plan/apply result, and
downloaded package path. List only remaining user actions such as secret entry
or OAuth consent. If incomplete, name the one concrete blocker and the next
deterministic command; do not hand the task to another agent.
