package cmd

import (
	"fmt"
	"strings"
)

// parseInlineKeyValuePairs parses the shared semicolon-delimited credential
// shape and rejects ambiguous or accidentally empty assignments locally.
func parseInlineKeyValuePairs(value string) (map[string]string, error) {
	return parseInlineKeyValuePairsAllowingEmpty(value)
}

// parseInlineKeyValuePairsAllowingEmpty admits only explicitly named empty
// fields needed by reviewed contracts such as Basic password mode `empty`.
func parseInlineKeyValuePairsAllowingEmpty(value string, allowedEmptyKeys ...string) (map[string]string, error) {
	pairs := make(map[string]string)
	allowedEmpty := make(map[string]struct{}, len(allowedEmptyKeys))
	for _, key := range allowedEmptyKeys {
		allowedEmpty[strings.ToLower(strings.TrimSpace(key))] = struct{}{}
	}
	// An empty aggregate value is reserved for callers' interactive-input path.
	if value == "" {
		return pairs, nil
	}
	// Validate segments in source order so the diagnostic points to the first actionable mistake.
	for index, part := range strings.Split(value, ";") {
		separator := strings.Index(part, "=")
		// Requiring an explicit separator prevents mistyped segments from being silently discarded.
		if separator < 0 {
			return nil, fmt.Errorf("invalid inline key=value input: segment %d must contain '='", index+1)
		}
		key := strings.ToLower(strings.TrimSpace(part[:separator]))
		// Empty keys cannot identify a credential field and usually indicate a shell typo.
		if key == "" {
			return nil, fmt.Errorf("invalid inline key=value input: segment %d has an empty key", index+1)
		}
		parsedValue := strings.TrimSpace(part[separator+1:])
		// Empty values commonly come from unset shell variables and must fail before any request is sent.
		if parsedValue == "" {
			// Only a contract-owned exception may distinguish a deliberate empty value from an unset variable.
			if _, allowed := allowedEmpty[key]; !allowed {
				return nil, fmt.Errorf("invalid inline key=value input: key %q has an empty value; ensure referenced shell variables are set or use --interactive", key)
			}
		}
		// Duplicate keys are ambiguous because accepting the last value can hide an earlier mistake.
		if _, exists := pairs[key]; exists {
			return nil, fmt.Errorf("invalid inline key=value input: duplicate key %q", key)
		}
		pairs[key] = parsedValue
	}
	return pairs, nil
}
