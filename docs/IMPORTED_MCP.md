# Imported MCP capabilities from the CLI

## Imported service MCP catalogs

Imported capabilities are supported only by `kind: mcp`. The service version must
already be enabled in the workspace. Catalog import uses Engine's owner checks;
only the service owner with service-management and catalog-import permission can
discover or apply. Saved catalogs are private to the importing actor. Bearer
connections use an existing bucket secret; never pass the provider token to CLI.

```sh
fused-cli workspace service mcp discover demo-crm --version 1.0.0 \
  --url https://mcp.deepwiki.com/mcp --json
# Review the preview, then use its exact id before it expires.
fused-cli workspace service mcp apply demo-crm --version 1.0.0 \
  --draft-id <preview-id>
fused-cli workspace service mcp show demo-crm --version 1.0.0 \
  --type tools --query structure
```

`discover` also previews refreshes and URL changes; it never replaces saved state.
For authenticated discovery, add both `--bucket <existing-bucket>` and
`--secret <generic-secret-name>`. Use that same bucket for the app's service.
`show --json` preserves complete input/output schemas and native definitions.
Types are `tools`, `prompts`, `resources`, and `resource_templates`; text and type
filters intersect and never change selection authority.

Use the same `init` and `extend` lifecycle as endpoint selection:

```sh
fused-cli init repository-docs --mcp \
  --description 'Browse public repository documentation through Demo CRM.' \
  --service demo-crm@1.0.0 --mcp-tool demo-crm=read_wiki_structure
fused-cli extend repository-docs --mcp-tool demo-crm=read_wiki_contents
```

These commands write the config, plan, and apply it. Extension preserves earlier
selections and infers the next minor version for a changed stable SemVer app;
repeating the same selection keeps the version. Use `-f <path>` for an explicit
config, `--no-apply` to save desired state and available receipts for review, or
`--no-token` to skip issuing an execution token. Existing service versions are
inherited on extend, so there is no need to repeat `--service`.

In a terminal, omit selection flags to search and select endpoints, tools,
prompts, resources, and resource templates in one picker. No imported capability
is preselected. Services with no imported capabilities retain the endpoint picker.

For automation or precise selection, repeat any of:

- `--mcp-tool '<service>=<name>'`
- `--mcp-prompt '<service>=<name>'`
- `--mcp-resource '<service>=<URI>'`
- `--mcp-resource-template '<service>=<URI-template>'`

Values retain commas and equals signs. Physical `--operation` and `--select-all`
flags can be combined with these flags. `--mcp-all <service>` freezes all currently
saved capabilities into explicit lists; future catalog additions are excluded.
It cannot be combined with individual imported choices for that service.
Imported flags require standalone `--mcp` on init or an existing `kind: mcp`
config on extend; SDK, REST, and combined apps reject them.

`--mcp-revision '<service>=<approved-revision-id>'` guards against a catalog
changing after review. Extending an existing imported selection onto a different
catalog revision requires this flag. Retained names must still exist in the
reviewed revision; extension never silently removes earlier grants.

An imported-only app needs no physical operation selection:

```yaml
apiVersion: fused/v1
kind: mcp
name: repository-docs
version: 1.0.0
description: Browse public repository documentation through Demo CRM.
bucket: default
services:
  demo-crm:
    version: 1.0.0
    mcp:
      revision_id: <approved-revision-id>
      tools: [read_wiki_structure]
      # prompts: [exact-prompt-name]
      # resources: ["docs://guide"]
      # resource_templates: ["docs://{+path}"]
```

```sh
fused-cli mcp validate -f mcp.yaml
fused-cli mcp plan -f mcp.yaml
fused-cli mcp apply -f mcp.yaml
fused-cli mcp operations repository-docs@1.0.0 --json
```

Engine checks exact revision membership and pins definitions in the immutable app
version. Refreshing a catalog does not change deployed apps. Imported tools use
Fused `search_docs` and `execute`; prompts and resources remain native MCP
capabilities. Restricted execution tokens use the exact capability identities
returned by `mcp operations`. Provider connections and credentials remain in
Engine; CLI never executes provider tools during import or selection.
