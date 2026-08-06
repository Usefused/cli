---
name: fused-bucket
description: "Use when the user wants to manage Fused bucket credentials using fused-cli -- storing static secrets or values, registering or checking a service's OAuth/OIDC app (client_id/client_secret/redirect_uri), starting an OAuth/OIDC connect session for a user, or listing/selecting a connected user's provider resources. Trigger on 'bucket', 'secret', 'fused-cli secret', 'fused-cli value', 'fused-cli connect', 'OAuth connect', 'register OAuth app', 'check connect config', 'connection resources', or 'connected resource'. For the auth_type/connect config field shapes themselves, or how a resource's fields get bound into a request, read fused-config."
---

# Buckets, secrets, and connections

A bucket is the credential container a workspace service, SDK, or MCP app
points at. It owns runtime credential material keyed by service --
services declare what's *enabled*, buckets declare what credentials a
selected app/runtime *uses*.

A workspace commonly has more than one bucket for the same service -- e.g.
a `staging` bucket and a `production` bucket each holding a different
Stripe API key, or a per-customer bucket in a multi-tenant setup. Bucket
read/value commands and workspace connect take bucket names (or full UUIDs as
an automation fallback); secret set/delete may omit `--bucket` to use the default.

Ownership and access are separate. A team can manage a bucket while a platform
administrator grants everyone bounded use with `fused-cli workspace access
bucket grant <bucket-name>`. That workspace share lets any eligible owning team
select the bucket for an SDK or MCP server, but does not let workspace members
read values, change secrets, or manage connections. Use `workspace access
bucket revoke <bucket-name>` to remove only that global use binding.

```yaml
buckets:
  <bucket-name>:
    service_config:
      <service-slug>:
        auth: {...}       # see fused-config for the auth_type shape
```

That `buckets:` block is real -- workspace.yaml can declaratively set
`service_config.<slug>.auth` and generic `secrets.<key>` ($ENV-only, no
literals) for a bucket, and `plan`/`apply` does read and apply it. What it
cannot do is **create the bucket itself**: apply resolves each
`buckets.<name>` key against an existing bucket by name, and if no bucket
with that name exists yet, apply fails outright with "bucket not found" --
it never creates one implicitly. **The only way to create a bucket is
`fused-cli bucket create <bucket-name>`** below. Always create the
bucket first if it might not exist yet, before declaring `buckets.<name>...`
in workspace.yaml or running any `--bucket <name>` command against it.

A service's OAuth/OIDC app registration (`connect`) is never a workspace.yaml
field -- it's set only via `fused-cli connect set <slug>` below, an immediate
admin action against its own endpoint, not something `plan`/`apply` sees or
touches.

Every command list below may be behind the CLI's actual flags/subcommands --
run `fused-cli <command> --help` (e.g. `fused-cli bucket --help`, `fused-cli
secret --help`, `fused-cli connection resources --help`) to confirm before
relying on one (see `fused-cli` skill).

## `${bucket...}` reference syntax across contexts

The same `${bucket...}` tag shape is used in three places with different
rules -- easy to conflate since they look alike:

| Context | Form | Bucket name | Can merge with surrounding text? |
|---|---|---|---|
| SDK/MCP `injections[].value` (`fused-sdk`/`fused-mcp`) | `${bucket.env\|values\|secrets.<key>}` | Always that SDK or MCP server's own `bucket:` -- cannot name another | Yes (e.g. `"Bearer ${bucket.secrets.KEY}"`) |
| `kind: webhook` `services.<slug>.secret` (`fused-webhook`) | `${bucket.<name>.env\|secret.<key>}` or `${bucket.env\|secret.<key>}` (default bucket) | Explicit (or defaults to `default`) -- webhook verification has no app/dispatch context to fall back on | No -- must be the entire field value |
| Connection profile `${resource.*}` (`fused-config`) | `${resource.provider_resource_id\|base_url\|metadata.<key>}` | N/A -- not a bucket reference at all, resolves against the selected connection's resource | No -- must be the entire field value |

`fused-cli connect set <slug>` (below) takes `client_id`/`client_secret`/
`redirect_uri` as literal values only -- it's an immediate admin action, not
a declarative field, so there is no `${bucket...}`/`$ENV` reference form for
it to resolve.

Using the wrong form in the wrong place is rejected with an explicit error
naming the unsupported reference -- e.g. a webhook-style named-bucket
reference (`${bucket.<name>.secrets.<key>}`) inside an SDK/MCP injection
value at dispatch time.

## Bucket commands

```shell
fused-cli bucket list                    # NAME, ID, secret count, value count
fused-cli bucket create <bucket-name>
fused-cli bucket delete <bucket-name>
fused-cli bucket show <bucket-name-or-id>             # + created_at
fused-cli bucket services <bucket-name-or-id>         # per-service breakdown: secrets/values/connect-configs/connected-user counts
fused-cli bucket secrets <bucket-name-or-id>          # metadata only; never values
fused-cli bucket values <bucket-name-or-id>
fused-cli bucket connections <bucket-name-or-id> [--service <service-slug>] [--user <end-user-reference>]
fused-cli bucket sdks <bucket-name-or-id>
```

Don't confuse `bucket connections <bucket-name-or-id>` (every end user who has connected
to *any* service through this bucket, and whether their token is
healthy/refreshing/failing) with `connection resources` below (for *one*
already-connected user, which provider tenants their token can reach) --
they're different scopes of the word "connection."

## Permissions and team access

Bucket operations use separate permissions by lifecycle:

- Bucket metadata/list/show needs `bucket.read`; reading values needs
  `bucket.values.read`; secret listings need `credentials.metadata.read`; and
  connection listings need `connection.read`.
- Creating a bucket needs workspace `bucket.manage`. Changing bucket values
  needs `bucket.manage`; changing secrets needs `credentials.manage`.
- `connect set` needs `credentials.manage` and `service.consume`.
- Starting a user connect session needs `connection.manage`, `bucket.use`, and
  `service.consume`. Changing or rediscovering connection resources needs
  `connection.manage`.
- Publishing a Registry connection-profile baseline needs `service.manage` and
  `credentials.manage`; setting/resetting a workspace profile needs
  `service.manage`.

Use the narrow team grants for the bucket and service involved:

```shell
fused-cli team access bucket grant <team> <bucket> use|manage
fused-cli team access service grant <team> <service> use|manage
fused-cli workspace access bucket grant <bucket>   # only for intended workspace-wide use
```

Bucket `use` permits selection without secret administration; bucket `manage`
includes value, credential, and connection management. Service `use` supplies
`service.read` and `service.consume`. On denial, stop the blocked action,
preserve any config or connection details already prepared, and tell the user
the missing permission and resource. Never self-grant, switch credentials,
broaden scope, or retry with guessed authority. Do not run access-changing
commands unless explicitly requested and authorised. Read the `fused-cli`
skill's `reference/access-management.md` for the complete role matrix.

## Static secrets and values

```shell
fused-cli secret list --bucket <bucket-name-or-id>
# single-value scheme (api_key, bearer, oauth/oidc static token):
printf '%s' "$TOKEN" | fused-cli secret set <service-slug> --value-stdin [--bucket <bucket-name-or-id>] [--type <scheme>] [--expires-at <RFC3339>]
# multi-field scheme (basic, mtls): send ONE ';'-joined value over stdin:
printf '%s' 'username=x;password=y' | fused-cli secret set <service-slug> --value-stdin [--bucket <bucket-name-or-id>] --type basic
printf '%s' 'cert=...;key=...' | fused-cli secret set <service-slug> --value-stdin [--bucket <bucket-name-or-id>] --type mtls
fused-cli secret set <service-slug> --interactive [--bucket <bucket-name-or-id>]
fused-cli secret delete <service-slug> <key-name> [--bucket <bucket-name-or-id>]
fused-cli value set <bucket-name-or-id> <service-slug> <location> <key-name> <value>
fused-cli value list <bucket-name-or-id>
fused-cli value delete <bucket-name-or-id> <service-slug> <key-name>
```

**There is no `--username`/`--password`/`--cert`/`--key` flag, and `basic`/
`mtls` are not two separate secrets.** The stdin value is *itself* the whole
credential: for these schemes it is a `key=value;key=value` string -- not
comma-separated, not JSON, and not two sequential `set` calls. Credential
values in argv are rejected so shell history and process listings cannot
retain them.

`secret set` is an **upsert with no separate apply step** -- unlike
workspace/SDK/MCP config, writing a secret takes effect for the *next*
request immediately, there's nothing to `plan`/`apply` afterward. That also
means re-running `set` with a new value is how you rotate a credential;
there's no versioning or grace period, the old value is simply gone.

If a service declares more than one auth scheme (e.g. both `api_key` and
`oauth` as alternatives), a bare `set` only auto-picks the scheme when
there's exactly one -- otherwise pass `--type <scheme-name>` or use `-i` to
pick interactively. They are always stored as two separate secret keys
(`<name>_username`/`<name>_password`, or `<name>_cert`/`<name>_key`) once
parsed out of that one `;`-delimited value, and a client cert/key pair is
validated (matching pair, not expired) before it's ever stored.

`--expires-at` is optional and purely advisory metadata (`secret list` and
`bucket secrets <bucket-name-or-id>` flag an expired one) -- nothing auto-rotates or
blocks requests when a secret passes its expiry.

`value` stores arbitrary bucket-scoped values, including literal binding
values a connection profile references (see `fused-config`).

Prefer bucket secrets/values over local `_env`/`$VAR` handoffs for anything
committed to source control -- a bucket secret is resolved server-side by
the Engine, not read off the machine running `apply`. This covers `auth`
static credentials and binding literals. It does *not* cover `connect`'s
`client_id`/`client_secret` -- those have their own dedicated command below,
not `secret set` (a static `auth` credential and a `connect` app registration
used to be reachable through the same `secret set` command and the same
derived key, which meant the two could silently overwrite each other if both
were ever configured for the same service+bucket; `connect set` writes to
its own storage instead).

## Registering a service's OAuth/OIDC app (connect)

```shell
printf '%s' 'client_id=...;client_secret=...;redirect_uri=https://...' | fused-cli connect set <service-slug> --bucket <bucket-name-or-id> --value-stdin [--type oauth|oidc]
fused-cli connect set <service-slug> --bucket <bucket-name-or-id> --interactive
```

This registers (or rotates) the app credentials a service's interactive
OAuth/OIDC flow uses -- distinct from any one end user connecting, see the
next section for that. Like `secret set`/`value set`, this is an **immediate
admin action**: no workspace.yaml block, no plan/apply, takes effect on save.
`--type` disambiguates only when a service declares both `oauth` and `oidc`;
otherwise the sole supported scheme is picked automatically.

Every field is required the first time (there is nothing to fall back to),
but afterward **omitting a field leaves it unchanged** -- rotating just
`redirect_uri` does not require resupplying `client_id`/`client_secret`. A
key present but blank (`'client_secret='`, or an empty interactive answer) is
different from a key never mentioned: the former is rejected as an attempt
to blank out a credential, the latter means "leave as-is." This works even
though the admin API never returns decrypted values back to a caller --
Engine merges an omitted field in from the existing encrypted row itself, not
from anything the CLI resent.

```shell
fused-cli connect get <service-slug> --bucket <bucket-name-or-id>
```

Reads back whatever the last `set` saved -- `auth_type`, `enabled`,
`redirect_uri` in plaintext, plus `has_client_id`/`has_client_secret` as
booleans (never the actual `client_id`/`client_secret`, same as `set`'s
response). This is the only way to check registration state on demand:
`bucket services <bucket-name-or-id>` shows just a connect-config count, and
workspace.yaml/`workspace sync` never reflect this at all -- app
registration was deliberately taken out of the declarative surface entirely
(see above), so there is nothing for `plan`/`apply`/`sync` to show. `get`
fails with a clear error, not a raw 404, when nothing has been registered
yet for that bucket+service.

## Starting an OAuth/OIDC connection

```shell
fused-cli workspace service connect <service-slug> --bucket <bucket-name-or-id> --user-ref <end-user-reference> [--scope read:x --scope write:y]
```

Omitting `--scope` requests the service's declared scope catalogue. OIDC
subsets must include `openid`. This is the same validation the SDK's
`sdk.auth.startConnectSession(...)` call uses.

## Managing a connected user's resources

One OAuth token can front several provider tenants (sites, shops, portals,
accounts). After a successful connect, Engine discovers or accepts
submitted resources and picks a default automatically when there's exactly
one:

```shell
fused-cli connection resources list <connection-id>
fused-cli connection resources set-default <connection-id> <resource-id>
fused-cli connection resources rediscover <connection-id>
```

With several resources and no default set, a call must pass an explicit
`X-Fused-Resource-ID` / `resourceId` or it fails with a structured ambiguity
error. `rediscover` removes resources the provider no longer returns; if the
default disappears, selection falls back to sole/default/explicit rules
rather than routing to a stale tenant. How a selected resource's fields
actually get injected into a request (`${resource.base_url}`,
`${resource.metadata.*}`) is documented in `fused-config`.

## Registry-level connection profile baseline

`fused-cli connection-profile set <service-ref> --version <v> --auth-type <type> --file <path>`
publishes an immutable, versioned profile at the Registry level
(provider-owner or curator only) -- this is distinct from a workspace's own
bucket credentials and is covered in `fused-config`.
