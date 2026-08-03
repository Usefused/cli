# Access management

Use these commands for workspace RBAC and workspace-wide resource sharing. They
call the Engine immediately; they do not edit `.fused/` files. Mutations are
idempotent, and the CLI emits an applied-change OTEL audit event only when the
Engine reports that state changed.

## Contents

- [Permission model](#permission-model)
- [Permissions by process](#permissions-by-process)
- [Choose the credential](#choose-the-credential)
- [Discover IDs before mutating](#discover-ids-before-mutating)
- [Manage people and membership](#manage-people-and-membership)
- [Manage team access](#manage-team-access)
- [Share a resource across the workspace](#share-a-resource-across-the-workspace)
- [Choose an owner for SDKs, MCP servers, and webhooks](#choose-an-owner-for-sdks-mcp-servers-and-webhooks)
- [When permission is denied](#when-permission-is-denied)

## Permission model

Built-in workspace roles are broad defaults:

- Owner has every permission.
- Admin has every operational permission except `account.manage` and
  `billing.manage`.
- Builder has `workspace.read`, `artifact.create`, `catalogue.read`,
  `account.read`, `billing.read`, and `notification.update`.
- Viewer has `workspace.read`, `catalogue.read`, `account.read`, and
  `billing.read`.

Resource roles add narrower access:

- Service `use` includes `service.read` and `service.consume`; `manage` also
  includes `service.manage`.
- Bucket `use` includes `bucket.read` and `bucket.use`; `manage` also includes
  `bucket.manage`, `bucket.values.read`, `credentials.metadata.read`,
  `credentials.manage`, `connection.read`, and `connection.manage`.
- Artifact roles are cumulative: `read`, `use`, then `manage`; `manage` also
  includes `artifact.tokens.manage`.

Workspace service lists and searches are access-filtered. If a service is
absent, do not claim it is disabled until an authorised caller confirms that;
the current caller may simply lack `service.read` for it. Registry search is a
separate discovery surface: it returns owned private services plus visible
public services, requires `catalogue.read`, and does not grant workspace
activation or consumption rights. Registry service search currently returns at
most 20 ranked matches and has no next-page flag.

## Permissions by process

| Process | Required permissions |
|---|---|
| Registry service search | `catalogue.read` |
| Registry import | `catalogue.import`; import-session reads use `catalogue.read` |
| Workspace service list/search | `service.read` for each returned service |
| Workspace service plan | `workspace.read` and `service.manage` for every changed service; `bucket.manage` for bucket changes |
| Workspace service apply | `workspace.update` plus the plan's `service.manage`/`bucket.manage` requirements; credential material can also require `credentials.manage` |
| New SDK/MCP/webhook plan | `artifact.create`, `service.read`, and `bucket.read` for selected dependencies |
| Existing SDK/MCP/webhook plan | `artifact.manage`, `service.read`, and `bucket.read` |
| SDK/MCP/webhook apply | `artifact.create` for new or `artifact.manage` for existing resources, plus `service.consume` and `bucket.use` for selected/referenced dependencies |
| SDK download / MCP list | `artifact.read` |
| SDK/MCP execution-token management | `artifact.tokens.manage` |
| MCP remove | `artifact.manage` |
| Bucket queries | `bucket.read`; values need `bucket.values.read`; secret metadata needs `credentials.metadata.read`; connections need `connection.read` |
| Bucket/value/secret mutations | Workspace `bucket.manage` to create; `bucket.manage` for values; `credentials.manage` for secrets |
| Connect app registration | `credentials.manage` and `service.consume` |
| Connect session / connection resources | `connection.manage`, `bucket.use`, and `service.consume` for a session; resource mutations use `connection.manage` |
| Registry connection-profile publication | `service.manage` and `credentials.manage` |
| Workspace connection-profile set/reset | `service.manage` |
| Notifications | `workspace.read` to read; `notification.update` to acknowledge or dismiss |
| Access inspection/mutation | `access.read` to inspect; `access.manage` to change; assigning Owner also requires `account.manage` |

Treat plan output as authoritative when it names a more specific resource. A
workspace role and a scoped resource grant are additive; for example,
`service.manage` alone does not supply the `workspace.update` needed to apply a
workspace activation.

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

Reading teams, users, and bindings requires `access.read`. Creating or changing
teams, users, memberships, credentials, roles, and grants requires
`access.manage`; assigning the workspace Owner role also requires
`account.manage`. Do not run access-changing commands just because they appear
as remediation here. Run them only when the user explicitly asks and the
current identity is authorised.

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

For a team-owned build, verify both ownership eligibility and dependency
access before planning:

```shell
fused-cli team eligible-owners
fused-cli team build-access <team-slug> --resource service
fused-cli team build-access <team-slug> --resource bucket
```

If the intended team is not eligible or lacks dependency access, keep the
requested ownership unchanged and report the team/resource to the user. Never
silently fall back to personal ownership or another team.

When a dependency is missing, an authorised administrator can grant the
narrowest matching scope rather than changing the team's workspace role:

```shell
fused-cli team access service grant <team-slug> <service> use
fused-cli team access bucket grant <team-slug> <bucket> use
fused-cli team access artifact grant <team-slug> <sdk-or-mcp> read|use|manage
```

## When permission is denied

Stop the blocked action and preserve any config draft, plan output, or receipt
already produced. Tell the user:

1. which action was blocked;
2. the missing permission named by Fused;
3. the service, bucket, artifact, workspace, or account resource it applies to;
4. the narrowest relevant team/workspace command an authorised administrator
   could use, when one exists.

Never self-grant, switch credentials, broaden a team to Admin/Owner, or retry
with guessed authority. Access changes are a separate user-authorised action;
if no resource-scoped grant exists (for example `workspace.update` or
`notification.update`), say that a suitable workspace role is required.
