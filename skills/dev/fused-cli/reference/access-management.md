# Access management

Use these commands for workspace RBAC and workspace-wide resource sharing. They
call the Engine immediately; they do not edit `.fused/` files. Mutations are
idempotent, and the CLI emits an applied-change OTEL audit event only when the
Engine reports that state changed.

## Choose the credential

- For a solo/bootstrap workspace, `FUSED_LICENSE_KEY` can act as the Owner
  credential.
- For attributable human or service access, set the personal key in
  `FUSED_API_KEY`; it takes precedence over `FUSED_LICENSE_KEY`.
- Never place a personal key in command output, config examples, logs, or OTEL.
  `user credential issue` is the one command that prints a raw key, once.

## Discover IDs before mutating

```shell
fused-cli team list --search platform
fused-cli team show <team-slug>
fused-cli user list --search ada@example.com
fused-cli user show <email>
```

List commands query and paginate on the Engine. Use `--limit` and `--offset`;
do not fetch every page just to filter locally.

## Manage people and membership

```shell
fused-cli user create ada@example.com --name "Ada Lovelace"
fused-cli team member add <team-slug> ada@example.com --role member
fused-cli team member add <team-slug> manager@example.com --role manager
fused-cli team member list <team-slug>
fused-cli team member remove <team-slug> ada@example.com

fused-cli user update ada@example.com --name "Ada L."
fused-cli user suspend ada@example.com
fused-cli user reactivate ada@example.com
```

Creating or adding a person does not send an invitation. Issue a personal
credential when they need to authenticate:

```shell
fused-cli user credential issue ada@example.com --name laptop
fused-cli user credential revoke ada@example.com laptop
```

Capture the issued key directly into the intended secret store. It is shown
once and must not be copied into chat, source control, or telemetry.

## Manage team access

```shell
fused-cli team create "Platform" --slug platform
fused-cli team access workspace set platform builder
fused-cli team access workspace clear platform

fused-cli team access service grant platform github use
fused-cli team access service revoke platform github use
fused-cli team access bucket grant platform company-credentials manage
fused-cli team access bucket revoke platform company-credentials manage
fused-cli team access artifact grant platform support-sdk use
fused-cli team access artifact revoke platform support-sdk use
```

Use only the canonical values exposed by command help:

- Workspace: `owner`, `admin`, `builder`, `viewer`
- Service/bucket: `use`, `manage`
- SDK or MCP server permission scope: `read`, `use`, `manage`
- Membership: `member`, `manager`

Before archiving a team, remove or transfer its bindings and owned SDKs, MCP
servers, and webhook registrations.

## Share a resource across the workspace

Use this only for a bucket or SDK/MCP permission scope intended for everyone in
the local workspace. The CLI calls the latter `artifact` because that is the
Engine's shared RBAC resource type; SDK and MCP lifecycle commands remain
separate. There is no team ID and no selectable access level: workspace access
always means bounded `use`, while the owning person or team keeps management of
configuration, secrets, and tokens.

```shell
fused-cli workspace access list [--resource bucket|artifact]
fused-cli workspace access bucket grant company-credentials
fused-cli workspace access bucket revoke company-credentials
fused-cli workspace access artifact grant support-sdk
fused-cli workspace access artifact revoke support-sdk
```

A workspace-shared bucket is eligible when any team builds an SDK or deploys an MCP server, so a
platform team can maintain one company credential container without granting
every application team `bucket.manage`. A workspace-shared SDK/MCP scope is
visible and usable workspace-wide, but runtime calls still authenticate with
the SDK or MCP server's own token; sharing does not print, mint, or distribute
that token. If an SDK and MCP server share the same `name@version`, use the full
UUID shown by `sdk list` or `mcp list` for these generic access commands.

Use `team access ...` instead when only selected teams need the resource or
when a team needs management rights.

## Choose an owner for SDKs, MCP servers, and webhooks

SDKs, MCP servers, and webhook registrations belong to the authenticated person
by default. Choose a team when that team should own and manage the resource;
workspace-wide use is a separate binding and never changes the owner:

```shell
fused-cli team eligible-owners
fused-cli team build-access platform --resource service
fused-cli sdk plan --owner-team <team-slug>
fused-cli mcp plan --owner-team <team-slug>
fused-cli webhook plan --owner-team <team-slug>
```

Use the kind-specific `plan --owner-team <team-slug>` only when every newly
created resource in that run should have the same team owner. Omit it for
personal ownership. Plan output lists required
permissions in human-readable output and as `required_permissions` with
`--json`; inspect those requirements before apply rather than discovering an
authorization failure during mutation.
