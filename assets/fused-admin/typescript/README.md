# fused-admin

Management client for a Fused Engine. It lists and deploys MCP servers and mints
execution tokens using an OAuth access token issued by the `fused-auth` client.

## Setup

```bash
npm install
npm run build
```

## Usage

```ts
import { FusedAdminClient } from "fused-admin";

const client = new FusedAdminClient({
  engineUrl: "https://engine.example.com",
  accessToken: "<oauth access token from fused-auth>",
});

// List the workspace's MCP servers.
const { items, total } = await client.listServers(20, 0);

// Deploy a new MCP server from a declarative `kind: mcp` config object.
const server = await client.deployServer({ name: "studio-mcp", /* ... */ });

// Mint a one-time execution token for it (by name or id).
const appToken = await client.generateToken(server.name, { name: "studio" });
console.log(appToken.token);
```

> `deployServer` accepts the full declarative `kind: mcp` config object; the
> Engine validates it exactly like `fused-cli` does.
