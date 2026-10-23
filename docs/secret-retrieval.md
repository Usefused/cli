# Retrieve opted-in secrets

API keys and generic bucket secrets default to `exportable: false`. Use `bucket secret exportable` for an existing API key, or set
`--exportable` when writing a generic secret, to permit callers with the separate
`credentials.reveal` permission to retrieve it. Replacing a value without this
flag resets retrieval to disabled. OAuth, bearer, Basic, and mTLS credentials
cannot opt in.

```sh
fused-cli bucket secret set "MY_KEY=$MY_KEY" --bucket default --exportable
fused-cli connect <service> --bucket default --type api_key
fused-cli bucket secret list default --json
fused-cli bucket secret exportable default <secret-id> --enabled=true
fused-cli bucket secret get MY_KEY --bucket default
fused-cli bucket secret reveal default <secret-id>
fused-cli bucket secret reveal default <secret-id> --json
fused-cli bucket secret exportable default <secret-id> --enabled=false
```

Use the opaque ID from `bucket secret list`, which returns only metadata.
`reveal` deliberately prints plaintext with no added newline; `--json` emits a
`value` field. Write and list commands never reveal values. Policy updates
require `credentials.manage` on the bucket and do not require reveal authority.
Management and bucket-use grants do not imply reveal. Owner retains all
permissions; other users need an explicit reveal grant.

The Engine audits retrieval before releasing the value. It checks current
exportability on every request. Disabling retrieval does not invalidate copies
already retrieved; rotate the key at its provider to invalidate those copies.
