package api

import (
	"encoding/json"
	"fmt"
)

// PromptOperationSelection pins a classified operation for Registry-owned composition grounding.
type PromptOperationSelection struct {
	Service   string `json:"service"`
	ServiceID string `json:"service_id"`
	Version   string `json:"version"`
	Operation string `json:"operation"`
}

// DraftPromptUnifiedOperation uses the existing Registry model transport without sending local config or credentials.
func (c *Client) DraftPromptUnifiedOperation(goal string, selections []PromptOperationSelection) (string, error) {
	encoded, err := json.Marshal(selections)
	// Serialization must succeed before issuing a model request.
	if err != nil {
		return "", err
	}
	var response struct {
		Draft string `json:"draftPromptUnifiedOperation"`
	}
	err = c.GraphQL(`query DraftPromptUnifiedOperation($q: String!, $selections: String!) {
		draftPromptUnifiedOperation(q: $q, selections: $selections)
	}`, map[string]any{"q": goal, "selections": string(encoded)}, &response)
	// Empty drafts are failures, never implicit permission to publish only the physical calls.
	if err != nil {
		return "", err
	}
	if response.Draft == "" || len(response.Draft) > 128*1024 {
		return "", fmt.Errorf("prompt composition returned an empty or oversized draft")
	}
	return response.Draft, nil
}
