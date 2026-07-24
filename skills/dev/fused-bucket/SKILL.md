---
name: fused-bucket
description: "Use when the user wants to manage Fused bucket credentials using fused-cli -- storing static secrets or values, starting an OAuth/OIDC connect session for a user, or listing/selecting a connected user's provider resources. Trigger on 'bucket', 'secret', 'fused-cli secret', 'fused-cli value', 'OAuth connect', 'connection resources', or 'connected resource'. For the auth_type/connect config field shapes themselves, or how a resource's fields get bound into a request, read fused-config."
---

# Buckets, secrets, and connections

A bucket is the credential container a workspace service, SDK, or MCP
artifact points at. It owns runtime credential material keyed by service --
services declare what's *enabled*, buckets declare what credentials a
selected artifact/runtime *uses*.

```yaml
buckets:
  <bucket-name>:
    service_config:
      <service-slug>:
        auth: {...}       # see fused-config for the auth_type shape
        connect: {...}    # see fused-config for the connect shape
```

## Bucket commands

```shell
fused-cli bucket list
fused-cli bucket <name> create
fused-cli bucket <name> show
fused-cli bucket <name> services
fused-cli bucket <name> secrets
fused-cli bucket <name> values
fused-cli bucket <name> connections
fused-cli bucket <name> sdks
```

## Static secrets and values

```shell
fused-cli secret list
fused-cli secret <service-slug> set
fused-cli secret <service-slug> remove
fused-cli value <bucket-id> set
fused-cli value <bucket-id> list
fused-cli value <bucket-id> remove
```

`secret` stores the credential material a service's `auth` config
references (token, api_key, username/password, cert/key). `value` stores
arbitrary bucket-scoped values, including literal binding values a
connection profile references (see `fused-config`). Prefer these over local
`_env`/`$VAR` handoffs for anything committed to source control -- a bucket
secret is resolved server-side by the Engine, not read off the machine
running `apply`.

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
