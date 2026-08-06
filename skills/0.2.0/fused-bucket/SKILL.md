---
name: fused-bucket
description: "Use when the user wants to manage Fused bucket credentials using fused-cli -- storing static secrets or values, starting an OAuth/OIDC connect session for a user, or listing/selecting a connected user's provider resources. Trigger on 'bucket', 'secret', 'fused-cli secret', 'fused-cli value', 'OAuth connect', 'connection resources', or 'connected resource'. For the auth_type/connect config field shapes themselves, or how a resource's fields get bound into a request, read fused-config."
---

# Buckets, secrets, and connections

A bucket is the credential container a workspace service, SDK, or MCP
artifact points at. It owns runtime credential material keyed by service --
services declare what's *enabled*, buckets declare what credentials a
selected artifact/runtime *uses*.

A workspace commonly has more than one bucket for the same service -- e.g.
a `staging` bucket and a `production` bucket each holding a different
Stripe API key, or a per-customer bucket in a multi-tenant setup. Every
secret/value/connect command below takes an explicit bucket (by name or
ID), so nothing is implicitly workspace-wide.

```yaml
buckets:
  <bucket-name>:
    service_config:
      <service-slug>:
        auth: {...}       # see fused-config for the auth_type shape
        connect: {...}    # see fused-config for the connect shape
```

Every command list below may be behind the CLI's actual flags/subcommands --
run `fused-cli <command> --help` (e.g. `fused-cli bucket --help`, `fused-cli
secret --help`, `fused-cli connection resources --help`) to confirm before
relying on one (see `fused-cli` skill).

## `${bucket...}` reference syntax across contexts

The same `${bucket...}` tag shape is used in four places with different
rules -- easy to conflate since they look alike:

| Context | Form | Bucket name | Can merge with surrounding text? |
|---|---|---|---|
| SDK/MCP `injections[].value` (`fused-sdk`/`fused-mcp`) | `${bucket.env\|values\|secrets.<key>}` | Always the artifact's own `bucket:` -- cannot name another | Yes (e.g. `"Bearer ${bucket.secrets.KEY}"`) |
| `kind: webhook` `services.<slug>.secret` (`fused-webhook`) | `${bucket.<name>.env\|secret.<key>}` or `${bucket.env\|secret.<key>}` (default bucket) | Explicit (or defaults to `default`) -- webhook verification has no artifact/dispatch context to fall back on | No -- must be the entire field value |
| `connect.client_secret`/`client_id` (`fused-config`) | `${bucket.secret.<key>}` only | Always the connect config's own bucket -- naming another is rejected, unlike `kind: webhook` | No -- must be the entire field value |
| Connection profile `${resource.*}` (`fused-config`) | `${resource.provider_resource_id\|base_url\|metadata.<key>}` | N/A -- not a bucket reference at all, resolves against the selected connection's resource | No -- must be the entire field value |

Using the wrong form in the wrong place is rejected with an explicit error
naming the unsupported reference -- e.g. a webhook-style named-bucket
reference (`${bucket.<name>.secrets.<key>}`) inside an SDK/MCP injection
value at dispatch time, or the same named form inside `connect.client_secret`
at apply time.

## Bucket commands

```shell
fused-cli bucket list                    # NAME, ID, secret count, value count
fused-cli bucket create <bucket-name>
fused-cli bucket show <bucket-name-or-id>
fused-cli bucket services <bucket-name-or-id>
fused-cli bucket secrets <bucket-name-or-id>
fused-cli bucket values <bucket-name-or-id>
fused-cli bucket connections <bucket-name-or-id> [--service <service-slug>] [--user <end-user-reference>]
fused-cli bucket sdks <bucket-name-or-id>
```

Don't confuse `bucket connections <bucket-name-or-id>` (every end user who has connected
to *any* service through this bucket, and whether their token is
healthy/refreshing/failing) with `connection resources` below (for *one*
already-connected user, which provider tenants their token can reach) --
they're different scopes of the word "connection."

## Static secrets and values

```shell
fused-cli secret list --bucket <bucket-name-or-id>
# single-value scheme (api_key, bearer, oauth/oidc static token):
printf '%s' '<value>' | fused-cli secret set <service-slug> --value-stdin [--bucket <bucket-name-or-id>] [--type <auth-method>] [--expires-at <RFC3339>]
# multi-field scheme (basic, mtls): pack fields into ONE value, joined by ';' -- never separate flags, never separate `set` calls:
printf '%s' 'username=x;password=y' | fused-cli secret set <service-slug> --value-stdin [--bucket <bucket-name-or-id>] --type basic
printf '%s' 'cert=...;key=...' | fused-cli secret set <service-slug> --value-stdin [--bucket <bucket-name-or-id>] --type mtls
fused-cli secret delete <service-slug> <key-name> [--bucket <bucket-name-or-id>]
fused-cli value set <bucket-name-or-id> <service-slug> <location> <key-name> <value>
fused-cli value list <bucket-name-or-id>
fused-cli value delete <bucket-name-or-id> <service-slug> <key-name>
```

**There is no `--username`/`--password`/`--cert`/`--key` flag, and `basic`/
`mtls` are not two separate secrets.** The value sent through stdin is the
whole credential: for these two schemes it's a
`key=value;key=value` string -- semicolon-separated, quoted so the shell
doesn't split it on the `;` -- not comma-separated, not JSON, not two
sequential `set` calls. `--interactive` is the only alternative
way to supply both fields at once.

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
`bucket secrets <bucket-name-or-id>` flags an expired one) -- nothing auto-rotates or
blocks requests when a secret passes its expiry.

`value` stores arbitrary bucket-scoped values, including literal binding
values a connection profile references (see `fused-config`).

Prefer bucket secrets/values over local `_env`/`$VAR` handoffs for anything
committed to source control -- a bucket secret is resolved server-side by
the Engine, not read off the machine running `apply`. This covers `auth`
static credentials, binding literals, and OAuth `connect` app registration's
`client_secret` (`${bucket.secret.<key>}`, resolved against the connect
config's own bucket -- see `fused-config`).

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
