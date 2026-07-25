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

## Bucket commands

```shell
fused-cli bucket list                    # NAME, ID, secret count, value count
fused-cli bucket <name> create
fused-cli bucket <name> show             # + created_at
fused-cli bucket <name> services         # per-service breakdown: secrets/values/connect-configs/connected-user counts
fused-cli bucket <name> secrets          # metadata only -- service, key name, credential type, expiry; never the value itself
fused-cli bucket <name> values           # service, key name, location
fused-cli bucket <name> connections [--service <slug>] [--user <ref>]  # this bucket's end-user OAuth connections: user ref, auth type, refresh state, last failure
fused-cli bucket <name> sdks             # which SDK/MCP artifacts (and whether they're active) point at this bucket
```

Don't confuse `bucket <name> connections` (every end user who has connected
to *any* service through this bucket, and whether their token is
healthy/refreshing/failing) with `connection resources` below (for *one*
already-connected user, which provider tenants their token can reach) --
they're different scopes of the word "connection."

## Static secrets and values

```shell
fused-cli secret list --list-bucket <bucket>
fused-cli secret <service-slug> set <value> [--bucket <name>] [--type <scheme>] [--expires-at <RFC3339>] [-i]
fused-cli secret <service-slug> remove <key-name> [--remove-bucket <name>]
fused-cli value <bucket-id> set <service-slug> <location> <key-name> <value>
fused-cli value <bucket-id> list
fused-cli value <bucket-id> remove <service-slug> <key-name>
```

`secret set` is an **upsert with no separate apply step** -- unlike
workspace/SDK/MCP config, writing a secret takes effect for the *next*
request immediately, there's nothing to `plan`/`apply` afterward. That also
means re-running `set` with a new value is how you rotate a credential;
there's no versioning or grace period, the old value is simply gone.

If a service declares more than one auth scheme (e.g. both `api_key` and
`oauth` as alternatives), a bare `set` only auto-picks the scheme when
there's exactly one -- otherwise pass `--type <scheme-name>` or use `-i` to
pick interactively. `basic` and `mtls` schemes always require `-i`
regardless of how many schemes exist, because they need two distinct
values (username+password, or cert+key) that don't fit in one positional
argument -- they're written as two separate secret keys (`<name>_username`/
`<name>_password`, or `<name>_cert`/`<name>_key`), and a client cert/key
pair is validated (matching pair, not expired) before it's ever stored.

`--expires-at` is optional and purely advisory metadata (`secret list` and
`bucket <name> secrets` flag an expired one) -- nothing auto-rotates or
blocks requests when a secret passes its expiry.

`value` stores arbitrary bucket-scoped values, including literal binding
values a connection profile references (see `fused-config`).

Prefer bucket secrets/values over local `_env`/`$VAR` handoffs for anything
committed to source control -- a bucket secret is resolved server-side by
the Engine, not read off the machine running `apply`. This covers `auth`
static credentials and binding literals; it does not cover OAuth `connect`
app registration (`client_id`/`client_secret`), which has no bucket-secret
path today and genuinely needs `client_id_env`/`client_secret_env` (see
`fused-config`).

## Starting an OAuth/OIDC connection

```shell
fused-cli workspace service <slug> connect --bucket <name> --user-ref <ref> [--scope read:x --scope write:y]
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
