# CLI setup and operation

This guide covers Engine authentication, configuration precedence, safe
automation, and the operational commands intentionally omitted from the main
README.

## Connect to an Engine

The CLI needs an Engine URL and credential. A small workspace can use its
`FUSED_LICENSE_KEY` as the bootstrap Owner credential. Workspaces with multiple
people should use individually attributable CLI credentials.

```bash
fused-cli --engine-url "http://localhost:8081" login
```

The browser flow creates the resulting `fsk_` key locally; the Engine stores
only its hash and binds it to the authenticated subject. Use `--no-browser` to
print the approval URL for an interactive remote session. Automation should
use `--key`, `FUSED_API_KEY`, or `FUSED_LICENSE_KEY` instead.

Inspect or revoke the saved login without revealing its credential:

```bash
fused-cli whoami
fused-cli logout
```

`logout` removes the saved credential and expiry metadata while preserving the
Engine URL. It does not unset credential environment variables.

You can also configure values directly:

```bash
fused-cli config set engine-url http://localhost:8081
fused-cli config set api-key "$FUSED_API_KEY"
fused-cli config list
fused-cli workspace services list
```

If you do not have an Engine yet, use a release from the
[Fused Engine repository](https://github.com/Usefused/engine/releases).

## Configuration precedence

The CLI resolves configuration in this order:

1. Command-line flags: `--key` and `--engine-url`.
2. Saved login/config values.
3. `FUSED_API_KEY`.
4. `FUSED_LICENSE_KEY`.

A saved login wins over ambient credential variables so the attributable user
identity remains active after login.

## Automation-safe execution

Engine requests time out after one minute by default. Override this with
`--timeout`; use `--request-id` for an audit correlation ID. SIGINT and SIGTERM
cancel outstanding requests.

Use `--no-input` in scripts and agent runs. `CI=true` enables the same
non-interactive behaviour and disables release update checks;
`FUSED_NO_UPDATE_CHECK=1` disables only the update check.

Read-only commands accept `--json`. Paginated output contains `items`, `total`,
`limit`, and `offset`. A command using `--json` writes a structured error to
stderr on failure and exits non-zero.

## Credentials and buckets

```bash
fused-cli bucket list
printf '%s' "$GITHUB_TOKEN" | fused-cli secret set github --value-stdin
```

Use `fused-cli service show <slug> --json` to inspect a service's available
authentication schemes before setting credentials. OAuth/OIDC app
registrations use `fused-cli connect set`; they are separate from static
secrets and workspace configuration.

## Teams, people, and shared access

Solo workspaces do not need RBAC setup. Organisation workspaces can inspect and
manage people, ownership, and access with:

```bash
fused-cli team list
fused-cli user list
fused-cli team eligible-owners
fused-cli workspace access bucket grant company-credentials
```

SDKs, MCP servers, and webhook registrations belong to the authenticated person
by default. Pass `plan --owner-team <team-slug>` only when a team should own the
resource. Workspace-wide use is separate from ownership and does not grant
secret, configuration, or token management.

See the [command reference](COMMANDS.md) for the complete team, user, bucket,
secret, connect, and access command surface.
