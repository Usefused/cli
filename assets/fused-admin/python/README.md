# fused-admin

Management client for a Fused Engine. It lists and deploys MCP servers and mints
execution tokens using an OAuth access token issued by the `fused-auth` client.
It uses only the Python standard library.

## Setup

```bash
pip install -e .
```

## Usage

```python
from fused.fused_admin import FusedAdminClient, FusedAdminConfig, AppTokenPayload

client = FusedAdminClient(FusedAdminConfig(
    engine_url="https://engine.example.com",
    access_token="<oauth access token from fused-auth>",
))

# List the workspace's MCP servers.
result = client.list_servers(limit=20, offset=0)
print(result.total, result.items)

# Deploy a new MCP server from a declarative `kind: mcp` config dict.
server = client.deploy_server({"name": "studio-mcp"})

# Mint a one-time execution token for it (by name or id).
app_token = client.generate_token(server.name, AppTokenPayload(name="studio"))
print(app_token.token)
```

> `deploy_server` accepts the full declarative `kind: mcp` config dict; the
> Engine validates it exactly like `fused-cli` does.
