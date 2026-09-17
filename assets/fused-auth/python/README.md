# fused-auth

An OAuth 2.0 client for Fused's authorization server. It authenticates a user
into Fused directly over HTTPS and exchanges codes for delegated access tokens.

```py
from fused.fused_auth import FusedAuthClient, FusedAuthConfig

client = FusedAuthClient(FusedAuthConfig(
    issuer="https://engine.example.com",
    client_id="foc_...",
    client_secret="fos_...",   # omit for public clients
    redirect_uri="https://app.example.com/oauth/callback",
))

req = client.authorize_url(scopes=["service.consume"])
# 1. Redirect the user's browser to `req.url`.
# 2. On the callback, exchange the code:
tokens = client.exchange_code(code, req.code_verifier)
refreshed = client.refresh(tokens.refresh_token)
client.revoke(refreshed.access_token)
```

Install with `pip install .`.
