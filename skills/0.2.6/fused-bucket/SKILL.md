---
name: fused-bucket
description: "Use when the user wants to manage Fused bucket credentials using fused-cli -- storing static secrets or values, registering or checking a service's OAuth/OIDC app (client_id/client_secret), starting an OAuth/OIDC connect session for a user, or listing/selecting a connected user's provider resources. Trigger on 'bucket', 'secret', 'fused-cli connect', 'fused-cli bucket value', 'OAuth connect', 'register OAuth app', 'check connect config', 'connection resources', or 'connected resource'. For the auth_type/connect config field shapes themselves, or how a resource's fields get bound into a request, read fused-config."
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

Create the bucket with `fused-cli bucket create <bucket-name>` before configuring
credentials. Store service credentials with `fused-cli connect <service-slug>
--bucket <bucket-name>`; credential setup takes effect immediately.

Every command list below may be behind the CLI's actual flags/subcommands --
run `fused-cli <command> --help` (e.g. `fused-cli bucket --help`, `fused-cli
connect --help`, `fused-cli connection resources --help`) to confirm before
relying on one (see `fused-cli` skill).

## `${bucket...}` reference syntax across contexts

The same `${bucket...}` tag shape is used in three places with different
rules -- easy to conflate since they look alike:

| Context | Form | Bucket name | Can merge with surrounding text? |
|---|---|---|---|
| SDK/MCP `injections[].value` (`fused-sdk`/`fused-mcp`) | `${bucket.env\|values\|secrets.<key>}` | Always the artifact's own `bucket:` -- cannot name another | Yes (e.g. `"Bearer ${bucket.secrets.KEY}"`) |
| `kind: webhook` `services.<slug>.secret` (`fused-webhook`) | `${bucket.<name>.env\|secret.<key>}` or `${bucket.env\|secret.<key>}` (default bucket) | Explicit (or defaults to `default`) -- webhook verification has no artifact/dispatch context to fall back on | No -- must be the entire field value |
| Connection profile `${resource.*}` (`fused-config`) | `${resource.provider_resource_id\|base_url\|metadata.<key>}` | N/A -- not a bucket reference at all, resolves against the selected connection's resource | No -- must be the entire field value |

`fused-cli connect <service-slug>` accepts literal credential assignments.
The shell can expand environment variables; the CLI does not resolve bucket
references in those assignments. Engine derives the OAuth callback URL.

Using the wrong form in the wrong place is rejected with an explicit error
naming the unsupported reference -- e.g. a webhook-style named-bucket
reference (`${bucket.<name>.secrets.<key>}`) inside an SDK/MCP injection
value at dispatch time.

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
fused-cli bucket secret list <bucket-name-or-id>
fused-cli connect <service-slug> --bucket <bucket-name-or-id>
fused-cli connect <service-slug> "<scheme>=$TOKEN" --bucket <bucket-name-or-id> --type bearer --auth-name <scheme> --no-input
fused-cli connect <service-slug> "username=$USERNAME, password=$PASSWORD" --bucket <bucket-name-or-id> --type basic --no-input
fused-cli connect <service-slug> "cert=$CERT_PEM, key=$KEY_PEM" --bucket <bucket-name-or-id> --type mtls --no-input
fused-cli bucket credential delete <service-slug> <key-name> --bucket <bucket-name-or-id>
fused-cli bucket value set <bucket-name-or-id> <service-slug> <location> <key-name> <value>
fused-cli bucket value list <bucket-name-or-id>
fused-cli bucket value delete <bucket-name-or-id> <service-slug> <key-name>
```

In a terminal, `connect` selects the authentication method and prompts for masked
credential values. For automation, pass one comma-separated assignment argument
and `--no-input`. Single values use the auth scheme name; pairs use the field
names shown above. Use terminal prompts for values containing commas.
`--type` selects the authentication family; `--auth-name` selects the exact
scheme. `--bucket` defaults to `default`.

Supplying values replaces the selected credentials immediately, with no
plan/apply step. Supply both fields together for paired credentials.
`--expires-at <RFC3339>` adds advisory expiry metadata.

Generic secrets use `fused-cli bucket secret set <name> --bucket <bucket>` for
masked entry, or `fused-cli bucket secret set "name=$VALUE" --bucket <bucket>
--no-input` for automation. Bucket values store non-secret binding values.

## Storing a service's OAuth/OIDC application credentials

```shell
fused-cli connect <service-slug> "client_id=$CLIENT_ID, client_secret=$CLIENT_SECRET" --bucket <bucket-name-or-id> --type oauth --auth-name <scheme> --no-input
fused-cli connect <service-slug> --bucket <bucket-name-or-id> --type oidc --auth-name <scheme>
```

Supply exactly `client_id` and `client_secret` together, including when rotating
the pair. Partial updates and `redirect_uri` are rejected. Engine derives the
callback from its public URL. Application credentials are separate from each
connected user's tokens. Inspect credential metadata with `fused-cli bucket
services <bucket-name-or-id>`; saved values are never returned.

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
