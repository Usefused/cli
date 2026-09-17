// Auto-generated Fused Admin client. Do not edit manually.
// Powered by Fused — https://usefused.com
//
// A minimal management client for a Fused Engine. It authenticates with an
// OAuth access token (obtained via the fused-auth client) and talks to the
// Engine's management GraphQL and workspace REST endpoints to list MCP servers,
// deploy a new MCP server, and mint an execution token. It has no runtime
// dependencies beyond `fetch`.

export interface FusedAdminConfig {
  /** Your Fused Engine's public origin, e.g. "https://engine.example.com". */
  engineUrl: string;
  /** The OAuth access token (Bearer) issued by the fused-auth client. */
  accessToken: string;
}

export interface MCPServerTransportUrls {
  streamable_http: string;
  sse: string;
  versioned_streamable_http: string;
  versioned_sse: string;
}

export interface MCPServer {
  /** The immutable app (version) id; the family id is resolved separately. */
  id: string;
  name: string;
  version: string;
  config_key?: string;
  default_transport?: string;
  transport_urls?: MCPServerTransportUrls;
  stable?: boolean;
  stable_version_id?: string;
  execution_token?: string;
  active?: boolean;
  deactivated_at?: string;
  created_at?: string;
}

export interface MCPServerList {
  items: MCPServer[];
  total: number;
}

export interface AppTokenBinding {
  service_slug: string;
  auth_name: string;
  end_user_ref: string;
  resource_id?: string;
}

export interface AppTokenPayload {
  /** A short label shown by the app token list command. */
  name: string;
  /** Allowed operations; omit to inherit the default full-access policy. */
  allow?: string[];
  /** Positive whole-second lifetime; omit for the Engine default. */
  expires_in?: number;
  /** Optional fixed connected-user binding mode (e.g. "fixed"). */
  binding_mode?: string;
  /** Optional fixed per-service connected-user bindings. */
  bindings?: AppTokenBinding[];
}

export interface AppToken {
  /** The plaintext execution credential; returned exactly once. */
  token: string;
  name: string;
  allow: string[];
  expires_at: string;
  binding_mode: string;
  binding_count: number;
  created_at: string;
}

/** Raised when Fused returns a GraphQL error envelope or a non-2xx status. */
export class FusedAdminError extends Error {
  readonly status?: number;
  readonly errors?: unknown[];

  constructor(message: string, status?: number, errors?: unknown[]) {
    super(message);
    this.name = "FusedAdminError";
    this.status = status;
    this.errors = errors;
  }
}

/** The GraphQL response envelope used by every Engine query/mutation. */
interface GraphQLEnvelope<T> {
  data?: T;
  errors?: { message: string }[];
}

/** The minimal, stable MCPServer selection shared by list and deploy.
 * execution_token is intentionally omitted: it is a credential, and selecting
 * it marks the query as a sensitive read, whose audit preflight fails closed
 * for delegated OAuth tokens. Tokens are minted via generate_token instead.
 */
const MCPServerSelection = `
  id
  name
  version
  config_key
  default_transport
  transport_urls {
    streamable_http
    sse
    versioned_streamable_http
    versioned_sse
  }
  stable
  stable_version_id
  active
  deactivated_at
  created_at
`;

export class FusedAdminClient {
  private readonly engineUrl: string;
  private readonly accessToken: string;

  constructor(config: FusedAdminConfig) {
    // An absolute origin keeps endpoint joins unambiguous and prevents a
    // relative value from producing a malformed GraphQL or REST URL.
    if (!/^https?:\/\//.test(config.engineUrl)) {
      throw new Error("FusedAdminClient engineUrl must be an absolute URL");
    }
    if (!config.accessToken) {
      throw new Error("FusedAdminClient accessToken is required");
    }
    this.engineUrl = config.engineUrl.replace(/\/+$/, "");
    this.accessToken = config.accessToken;
  }

  /** Lists the workspace's MCP servers with simple pagination. */
  async listServers(limit = 10, offset = 0): Promise<MCPServerList> {
    const query = `query ListMcpServers($limit: Int, $offset: Int) {
      mcpServers(limit: $limit, offset: $offset) {
        items { ${MCPServerSelection} }
        total
      }
    }`;
    const data = await this.graphql<{ mcpServers: MCPServerList }>(query, { limit, offset });
    return data.mcpServers;
  }

  /** Deploys a new MCP server from a declarative `kind: mcp` config object. */
  async deployServer(config: Record<string, unknown>, ownerTeam?: string): Promise<MCPServer> {
    const query = `mutation DeployMcpServer($config: EngineJSON!, $ownerTeam: String) {
      deployMcpServer(config: $config, owner_team: $ownerTeam) { ${MCPServerSelection} }
    }`;
    const data = await this.graphql<{ deployMcpServer: MCPServer }>(query, {
      config,
      ownerTeam: ownerTeam ?? null,
    });
    return data.deployMcpServer;
  }

  /** Resolves an MCP server name (or id) to its authoritative app family id. */
  async resolveMCPFamilyReference(reference: string): Promise<string> {
    const query = `query ResolveAppFamilyReference($reference: String!, $kind: String!) {
      appFamilyReference(reference: $reference, kind: $kind) { id kind }
    }`;
    const data = await this.graphql<{ appFamilyReference: { id: string; kind: string } | null }>(
      query,
      { reference, kind: "mcp" }
    );
    // A null or empty id is not authority to issue a credential.
    if (!data.appFamilyReference?.id) {
      throw new FusedAdminError(`No MCP server matches reference "${reference}"`);
    }
    return data.appFamilyReference.id;
  }

  /** Mints a named execution token for an MCP server by name or id. */
  async generateToken(reference: string, payload: AppTokenPayload): Promise<AppToken> {
    const familyId = await this.resolveMCPFamilyReference(reference);
    const query = new URLSearchParams({ app_family_id: familyId });
    return this.rest<AppToken>(`/workspace/app-tokens?${query.toString()}`, {
      method: "POST",
      body: JSON.stringify(payload),
    });
  }

  /** Sends one management GraphQL request and unwraps its `data` payload. */
  private async graphql<T>(query: string, variables?: Record<string, unknown>): Promise<T> {
    const response = await fetch(`${this.engineUrl}/engine/graphql`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${this.accessToken}`,
      },
      body: JSON.stringify({ query, variables: variables ?? {} }),
    });
    const envelope = (await response.json().catch(() => ({}))) as GraphQLEnvelope<T>;
    if (!response.ok || envelope.errors?.length) {
      const message =
        envelope.errors?.map((error) => error.message).join("; ") ||
        `GraphQL request failed (${response.status})`;
      throw new FusedAdminError(message, response.status, envelope.errors);
    }
    if (envelope.data === undefined) {
      throw new FusedAdminError("Engine returned no data", response.status);
    }
    return envelope.data;
  }

  /** Sends one authenticated JSON request to the workspace REST API. */
  private async rest<T>(path: string, init: { method: string; body?: string }): Promise<T> {
    const response = await fetch(`${this.engineUrl}${path}`, {
      method: init.method,
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${this.accessToken}`,
      },
      body: init.body,
    });
    if (!response.ok) {
      const payload = (await response.json().catch(() => ({}))) as Record<string, unknown>;
      const message =
        (payload.message as string) ||
        (payload.error as string) ||
        `Request failed (${response.status})`;
      throw new FusedAdminError(message, response.status);
    }
    return (await response.json()) as T;
  }
}
