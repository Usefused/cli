# fused-auth

An OAuth 2.0 client for Fused's authorization server. It authenticates a user
into Fused directly over HTTPS and exchanges codes for delegated access tokens.

```ts
import { FusedAuthClient } from "fused-auth";

const client = new FusedAuthClient({
  issuer: "https://engine.example.com",
  clientId: "foc_...",
  clientSecret: "fos_...",   // omit for public clients
  redirectUri: "https://app.example.com/oauth/callback",
});

const { url, state, codeVerifier } = await client.authorizeUrl({
  scopes: ["service.consume"],
});
// 1. Redirect the user's browser to `url`.
// 2. On the callback, exchange the code:
const tokens = await client.exchangeCode(code, codeVerifier);
const refreshed = await client.refresh(tokens.refresh_token);
await client.revoke(refreshed.access_token);
```

Build with `npm install && npm run build`.
