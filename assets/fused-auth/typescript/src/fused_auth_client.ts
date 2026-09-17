// Auto-generated Fused Auth client. Do not edit manually.
// Powered by Fused — https://usefused.com
//
// A direct OAuth 2.0 client for Fused's own authorization server. Unlike a
// generated service SDK (which proxies calls through the Engine over gRPC),
// this client talks to your Fused Engine's public OAuth endpoints over HTTPS so
// an application can authenticate a user into Fused and obtain delegated access
// tokens. It has no runtime dependencies beyond `fetch` and WebCrypto.

export interface FusedAuthConfig {
  /** Your Fused Engine's public origin, e.g. "https://engine.example.com". */
  issuer: string;
  /** The registered OAuth client id (`foc_...`). */
  clientId: string;
  /** Confidential client secret (`fos_...`). Omit for public (PKCE) clients. */
  clientSecret?: string;
  /** The exact redirect URI registered on the OAuth client. */
  redirectUri: string;
}

export interface AuthorizeOptions {
  /** Scopes to request, e.g. ["service.consume", "bucket.read"]. */
  scopes: string[];
  /** Optional opaque state echoed back in the redirect. */
  state?: string;
}

/** The artifacts an application needs to start a browser authorization. */
export interface AuthorizeRequest {
  /** Navigate the user's browser here to show Fused's consent screen. */
  url: string;
  /** The state that will be echoed back in the redirect. */
  state: string;
  /** Keep this to exchange the code; it is not stored by Fused. */
  codeVerifier: string;
}

export interface TokenResponse {
  access_token: string;
  refresh_token?: string;
  token_type: "Bearer";
  expires_in: number;
  scope: string;
}

export interface OAuthErrorPayload {
  error: string;
  error_description?: string;
}

export interface OAuthMetadata {
  issuer: string;
  authorization_endpoint: string;
  token_endpoint: string;
  revocation_endpoint?: string;
  response_types_supported?: string[];
  grant_types_supported?: string[];
  code_challenge_methods_supported?: string[];
  token_endpoint_auth_methods_supported?: string[];
  scopes_supported?: string[];
}

/** Raised when Fused returns an OAuth 2.0 error envelope. */
export class FusedAuthError extends Error {
  readonly error: string;
  readonly errorDescription?: string;

  constructor(payload: OAuthErrorPayload, status: number) {
    super(payload.error_description || payload.error);
    this.name = "FusedAuthError";
    this.error = payload.error;
    this.errorDescription = payload.error_description;
  }
}

/** Generates a URL-safe random string from WebCrypto randomness. */
function randomString(length: number): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~";
  const bytes = new Uint8Array(length);
  globalThis.crypto.getRandomValues(bytes);
  let result = "";
  for (const byte of bytes) {
    result += alphabet[byte % alphabet.length];
  }
  return result;
}

/** Encodes bytes as unpadded base64url, the encoding PKCE requires. */
function base64UrlEncode(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

/** Derives the S256 code challenge from a verifier via SHA-256. */
async function pkceChallenge(verifier: string): Promise<string> {
  const digest = await globalThis.crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(verifier)
  );
  return base64UrlEncode(new Uint8Array(digest));
}

export class FusedAuthClient {
  private readonly issuer: string;
  private readonly clientId: string;
  private readonly clientSecret?: string;
  private readonly redirectUri: string;

  constructor(config: FusedAuthConfig) {
    // An absolute issuer keeps endpoint joins unambiguous and prevents a
    // relative value from producing a malformed token URL.
    if (!/^https?:\/\//.test(config.issuer)) {
      throw new Error("FusedAuthClient issuer must be an absolute URL");
    }
    this.issuer = config.issuer.replace(/\/+$/, "");
    this.clientId = config.clientId;
    this.clientSecret = config.clientSecret;
    this.redirectUri = config.redirectUri;
  }

  /** Returns the RFC 8414 discovery document for the configured issuer. */
  async metadata(): Promise<OAuthMetadata> {
    const response = await fetch(`${this.issuer}/.well-known/oauth-authorization-server`);
    return (await response.json()) as OAuthMetadata;
  }

  /**
   * Builds the authorize URL plus a fresh PKCE pair. The caller navigates the
   * user's browser to `url`; Fused renders the consent screen and redirects
   * back to the registered redirect URI with `code` and `state`.
   */
  async authorizeUrl(options: AuthorizeOptions): Promise<AuthorizeRequest> {
    const state = options.state ?? randomString(32);
    const codeVerifier = randomString(64);
    const codeChallenge = await pkceChallenge(codeVerifier);
    const params = new URLSearchParams({
      response_type: "code",
      client_id: this.clientId,
      redirect_uri: this.redirectUri,
      scope: options.scopes.join(" "),
      state,
      code_challenge: codeChallenge,
      code_challenge_method: "S256",
    });
    return { url: `${this.issuer}/oauth/authorize?${params.toString()}`, state, codeVerifier };
  }

  /** Exchanges an authorization code (and its PKCE verifier) for tokens. */
  async exchangeCode(code: string, codeVerifier: string): Promise<TokenResponse> {
    const form = new URLSearchParams({
      grant_type: "authorization_code",
      code,
      redirect_uri: this.redirectUri,
      code_verifier: codeVerifier,
    });
    return this.tokenRequest(form);
  }

  /** Refreshes an access token with a previously issued refresh token. */
  async refresh(refreshToken: string): Promise<TokenResponse> {
    const form = new URLSearchParams({
      grant_type: "refresh_token",
      refresh_token: refreshToken,
    });
    return this.tokenRequest(form);
  }

  /** Revokes an access or refresh token (RFC 7009). */
  async revoke(token: string): Promise<void> {
    const form = new URLSearchParams({ token });
    await this.request("/oauth/revoke", form, false);
  }

  /** Sends one token-endpoint request and decodes its JSON response. */
  private async tokenRequest(form: URLSearchParams): Promise<TokenResponse> {
    return this.request("/oauth/token", form, true);
  }

  /**
   * Performs one form-urlencoded POST to the OAuth server. Client
   * authentication uses HTTP Basic when a secret is configured, otherwise the
   * client id travels in the body for public clients.
   */
  private async request<T>(path: string, form: URLSearchParams, expectJson: boolean): Promise<T> {
    const headers: Record<string, string> = { "Content-Type": "application/x-www-form-urlencoded" };
    if (this.clientSecret) {
      headers["Authorization"] = "Basic " + btoa(`${this.clientId}:${this.clientSecret}`);
    } else if (!form.has("client_id")) {
      form.set("client_id", this.clientId);
    }
    const response = await fetch(`${this.issuer}${path}`, {
      method: "POST",
      headers,
      body: form.toString(),
    });
    if (!response.ok) {
      const payload = (await response.json().catch(() => ({}))) as OAuthErrorPayload;
      throw new FusedAuthError(payload, response.status);
    }
    if (!expectJson) return undefined as unknown as T;
    return (await response.json()) as T;
  }
}

export default FusedAuthClient;
