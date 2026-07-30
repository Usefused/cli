package cmd

import "strings"

// parseInlineKeyValuePairs splits a single positional CLI argument shaped
// like "key1=val1;key2=val2" into a lowercase-keyed map. This is the one
// shared shape every multi-field secret-ish command uses (basic auth's
// username/password, mTLS's cert/key, connect's client_id/client_secret/
// redirect_uri) instead of a JSON blob or repeated flags, so it lives here
// once rather than being re-split/trimmed/lowercased in each command file.
func parseInlineKeyValuePairs(value string) map[string]string {
	pairs := make(map[string]string)
	if value == "" {
		return pairs
	}
	for _, part := range strings.Split(value, ";") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		pairs[key] = strings.TrimSpace(kv[1])
	}
	return pairs
}
