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

// DraftPromptExecutionApp asks Registry to author TypeScript against exact operation contracts.
func (c *Client) DraftPromptExecutionApp(goal string, selections []PromptOperationSelection) (string, error) {
	encoded, err := json.Marshal(selections)
	// The bounded exact selection document is the only model grounding sent by the CLI.
	if err != nil {
		return "", err
	}
	var response struct {
		Draft string `json:"draftPromptExecutionApp"`
	}
	err = c.GraphQL(`query DraftPromptExecutionApp($q: String!, $selections: String!) {
		draftPromptExecutionApp(q: $q, selections: $selections)
	}`, map[string]any{"q": goal, "selections": string(encoded)}, &response)
	// Missing or oversized code cannot become a silent no-op deployment.
	if err != nil {
		return "", err
	}
	if response.Draft == "" || len(response.Draft) > 128*1024 {
		return "", fmt.Errorf("Execution App draft was empty or oversized")
	}
	return response.Draft, nil
}
