package api

import (
	"encoding/json"
	"fmt"
)

// PromptOperationSelection pins a classified operation or event for Registry-owned composition grounding.
type PromptOperationSelection struct {
	Service   string `json:"service"`
	ServiceID string `json:"service_id"`
	Version   string `json:"version"`
	Operation string `json:"operation,omitempty"`
	Event     string `json:"event,omitempty"`
}

// DraftPromptUnifiedApp asks Registry to author TypeScript against exact operation and event contracts.
func (c *Client) DraftPromptUnifiedApp(goal string, selections []PromptOperationSelection) (string, error) {
	encoded, err := json.Marshal(selections)
	// The bounded exact selection document is the only model grounding sent by the CLI.
	if err != nil {
		return "", err
	}
	var response struct {
		Draft string `json:"draftPromptUnifiedApp"`
	}
	err = c.GraphQL(`query DraftPromptUnifiedApp($q: String!, $selections: String!) {
		draftPromptUnifiedApp(q: $q, selections: $selections)
	}`, map[string]any{"q": goal, "selections": string(encoded)}, &response)
	// Missing or oversized code cannot become a silent no-op deployment.
	if err != nil {
		return "", err
	}
	if response.Draft == "" || len(response.Draft) > 128*1024 {
		return "", fmt.Errorf("Unified App draft was empty or oversized")
	}
	return response.Draft, nil
}
