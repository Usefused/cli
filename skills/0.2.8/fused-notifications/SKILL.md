---
name: fused-notifications
description: "Use when the user asks about Fused workspace notifications -- why `fused-cli plan`/`apply` printed a message, what a notification's severity or type means (registry_version_added/changed/deprecated/removed, registry_execution_policy_changed, registry_connection_profile_changed, workspace_service_removed, workspace_version_removed), why the same notification keeps appearing on every plan, why a notification disappeared, or how to mark one read/dismissed. Trigger on 'notification', 'workspace notification', 'plan warning', any `registry_*`/`workspace_*` type name, 'severity', 'breaking change', 'why did apply warn me', 'mark as read', 'dismiss notification', 'notification bell', or 'force_remove'. For the config that removal/execution_policy/connection_profile notifications are ABOUT, read fused-workspace or fused-config instead -- this skill only covers the notification surface itself."
---

# Workspace notifications

Two independent mechanisms feed one notification list. Neither is configured
via a `kind:` file -- there's nothing to `plan`/`apply` here; this skill is
about reading and understanding output, not authoring input.

"Workspace" throughout this doc means one Engine deployment's own config --
Engine is single-workspace-per-deployment (there's no multi-tenant concept
inside one Engine, no `workspace_id` column anywhere in its schema). So
"every other workspace" elsewhere in these skills always means "every other
Engine deployment," never a second workspace living inside this one.

## Two notification groups, two triggers

**`workspace_service_removed` / `workspace_version_removed`** -- a
decision-audit record of something *you* just did. Created only when
`workspace service delete <slug> --force` or `workspace service version
delete <slug> <v> --force` overrides the default removal blocker
(see `fused-workspace`) while an SDK/MCP config in this account still
references what got removed. You already know this happened -- the
notification exists so it's traceable later, not to inform you of something
new.

**`registry_version_added` / `registry_version_changed` /
`registry_version_deprecated` / `registry_version_removed` /
`registry_execution_policy_changed` / `registry_connection_profile_changed`**
-- proactive discoveries you did nothing to trigger. A background Engine
poller reads each activated service's changelog from the Registry every few
minutes and checks whether the change actually affects this workspace:

- A new/changed/deprecated/removed **version**: matched against which of
  your SDK/MCP configs actually select that service+version (and for
  `changed` specifically, which of *those* configs selected the exact
  endpoint that was added/removed/changed -- a config with an explicit
  operation list is never notified about an endpoint it never selected).
- An **execution policy** change: matched only if this workspace has *no*
  local execution-policy override for that service/version -- a local
  override already shadows whatever the Registry default just became, so
  the change is moot here.
- A **connection profile** change: matched only if this workspace is still
  on the Registry `baseline` layer for that service/version/auth_type -- a
  workspace-local override profile already supersedes it.

If none of your configs are affected, nothing is created -- these
notifications are never a blanket "service X changed" broadcast, only ones
Engine has determined apply to something you actually use.

## Severity: `breaking` or `non-breaking`

Informational only -- neither value blocks `plan`/`apply` the way a removal
blocker does. Meaning per type:

- `registry_version_added`, `registry_version_deprecated` -- always
  `non-breaking` (deprecated still works today; it's a heads-up that removal
  is coming eventually, not something broken now).
- `registry_version_removed` -- always `breaking`.
- `registry_version_changed` -- `breaking` if any endpoint you use was
  removed or had a breaking field change; `non-breaking` if the diff only
  added new endpoints or changed non-breaking fields on ones you use.
- `registry_execution_policy_changed`, `registry_connection_profile_changed`
  -- derived from the underlying diff's own severity, same as version above.
  In practice these two always resolve `non-breaking` today (rate limit/
  retry/pagination/profile-field changes aren't classified as breaking by
  the diff itself), but that's a property of what's diffed, not a hardcoded
  rule for these two types.

## Where you actually see them

There is no dedicated "list notifications" or "inbox" *CLI command*, and
that's deliberate, not a gap -- see "Read/dismiss" below for why. On the CLI,
the only surface is `fused-cli <kind> plan` (and `apply`), which prints a
block right after each config's plan/apply line:

```
Plan created for sdk:jira-sdk (Plan ID: ...)
Workspace notifications for sdk:jira-sdk
- breaking engine registry_version_changed: version 2026-07-01 changed endpoints, affecting 2 of your configs.
- non-breaking registry jira@2026-07-01: endpoint drift detected against the live provider spec.
```

That block merges two genuinely different things under one list:
`source: engine` entries are the Engine-local notifications this skill
covers (`workspace_*`/`registry_*`, stored in Engine's own database);
`source: registry` entries are live provider-API drift snapshots
(`registry/drift` -- a *different* mechanism entirely, fetched fresh from
the Registry on every `plan` call, not stored on the Engine side, not
governed by anything in this doc). The CLI does not visually separate them
beyond that `source` label -- read it if you need to know which mechanism
you're looking at.

Separately, the UI has a general bell/panel in the shared layout (every
pending + acknowledged notification, workspace-wide) and a contextual
banner on a service's/SDK's/MCP's own details page (filtered to just what's
relevant to that page). Both read from the same underlying data as the CLI
block above -- they're two views onto one notification list, not a second
system.

## Dedupe: prevents a second notification, not a repeat display

Both groups dedupe on their own key while a notification is still
`pending`: `workspace_*` on `(plan_id, action_id)`, `registry_*` on the
underlying Registry changelog row's own ID (so re-polling the same change,
including across an Engine crash/retry, never creates a duplicate). This
only stops a *second* row for the same cause -- it does not hide the first
one. See the next section for what that means in practice.

Dedupe only ever matches against `pending` rows. Once a row has been
acknowledged or dismissed (see below), it's no longer a dedupe match --
if the poller encounters that exact same underlying Registry changelog row
again (a genuinely unusual case, since cursors normally only move forward),
a fresh `pending` notification can be created for it. This is accepted, not
a bug: dedupe's job is preventing noise from a single poll cycle re-seeing
its own recent work, not tracking "have I ever told this workspace about
this row before" forever.

## Read/dismiss: UI only, not CLI

`WorkspaceNotificationStatus` has three values -- `pending` /
`acknowledged` / `dismissed` -- and, as of the notification UI shipping, all
three are actually reachable. The model is two-tier:

- **Mark read** (`pending` -> `acknowledged`): the notification stays
  visible, just de-emphasized. Reversible in the sense that nothing about
  the underlying condition changed, but there's no "mark unread" action.
- **Dismiss** (`acknowledged` or `pending` -> `dismissed`): the
  notification disappears for good. Terminal -- there is no "undismiss."
  Dismissing an already-dismissed row, or trying to move a dismissed row to
  any other status, is rejected.

Both transitions go through one GraphQL mutation,
`updateWorkspaceNotificationStatus(id, status)`, on the Engine's own GraphQL
schema (`/engine/graphql`) -- **not** through `fused-cli`. `id` here is the
notification's own row id, not the `engine:`/`registry:`-prefixed composite
id the merged `workspaceNotifications` inbox query exposes (strip that
prefix first). This is deliberate scoping, not an oversight: CLI
`plan`/`apply`'s auto-print is for "what should I know about *while I'm
taking an action*," which doesn't need a read/unread concept; browsing,
marking read, and dismissing are a UI job (the bell panel and contextual
banners), not something `fused-cli` grew a subcommand for.

Read/dismiss state is workspace-global, not per-user -- Engine resolves
requests to a single `accountID` per deployment with no separate human-user
identity model, so there's no "read by me but not by my teammate" concept.
Whoever acknowledges or dismisses a notification (via the UI) resolves it
for everyone hitting that same Engine deployment.

**This changes what you see in `plan`/`apply` output, too**: the CLI's
auto-print only ever queries `pending` rows, so acknowledging *or*
dismissing a notification via the UI also makes it stop appearing in
`plan`/`apply` from that point on -- not just dismissing. If a notification
you were expecting stopped showing up on `plan` and the underlying condition
hasn't actually changed, someone (possibly you, via the UI) already resolved
it.

The live drift side (`source: registry` above) is a related but separate
mechanism: the Registry has its own delete-drift-snapshot endpoint, but no
`fused-cli` command calls it, and it isn't part of the
`updateWorkspaceNotificationStatus` mutation above -- a drift snapshot's
`source: registry` id can't be acknowledged/dismissed the way an
`source: engine` notification can.

## Permissions and team access

Reading workspace notifications requires `workspace.read`. Acknowledging or
dismissing an Engine notification requires `notification.update`; there is no
notification-scoped team grant. Builder includes `notification.update`, while
Viewer can read workspace state but cannot update notification status.

If an update is denied, stop and leave the notification unchanged. Tell the
user the missing permission (`notification.update`) and workspace resource;
preserve the notification ID/status needed for an authorised user to finish.
Never self-grant, switch credentials, broaden scope, or retry with guessed
authority. An authorised administrator may assign a suitable workspace role
with `fused-cli team access workspace set <team> <role>`, but the agent must not
run that command unless the user explicitly requests the access change. Read
the `fused-cli` skill's `reference/access-management.md` for the complete role
matrix.
