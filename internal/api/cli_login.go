package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var ErrCLILoginPending = errors.New("CLI login is pending")

type CLILoginStartRequest struct {
	CredentialHash   string `json:"credential_hash"`
	CredentialPrefix string `json:"credential_prefix"`
}

type CLILoginStartResponse struct {
	TransactionID string    `json:"transaction_id"`
	PollToken     string    `json:"poll_token"`
	BrowserToken  string    `json:"browser_token"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type CLILoginPollResponse struct {
	Status       string    `json:"status"`
	CredentialID string    `json:"credential_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (c *Client) StartCLILogin(input CLILoginStartRequest) (CLILoginStartResponse, error) {
	var response CLILoginStartResponse
	err := c.cliLoginRequest(http.MethodPost, "/auth/cli/start", input, http.StatusCreated, &response)
	return response, err
}

func (c *Client) PollCLILogin(transactionID, pollToken string) (CLILoginPollResponse, error) {
	var response CLILoginPollResponse
	status, err := c.cliLoginRequestStatus(http.MethodPost, "/auth/cli/poll", map[string]string{
		"transaction_id": transactionID, "token": pollToken,
	}, &response)
	if err != nil {
		return CLILoginPollResponse{}, err
	}
	if status == http.StatusAccepted && response.Status == "pending" {
		return CLILoginPollResponse{}, ErrCLILoginPending
	}
	if status != http.StatusOK || response.Status != "authenticated" || response.CredentialID == "" || response.ExpiresAt.IsZero() {
		return CLILoginPollResponse{}, errors.New("Engine returned an invalid CLI login response")
	}
	return response, nil
}

func (c *Client) cliLoginRequest(method, path string, input any, expectedStatus int, output any) error {
	status, err := c.cliLoginRequestStatus(method, path, input, output)
	if err != nil {
		return err
	}
	if status != expectedStatus {
		return fmt.Errorf("CLI login request failed (HTTP %d)", status)
	}
	return nil
}

// cliLoginRequestStatus performs one unauthenticated enrollment request and
// routes failures through the same current control-plane decoder as every command.
func (c *Client) cliLoginRequestStatus(method, path string, input, output any) (int, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.doRequest(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// Failed managed-login responses use the current bounded Engine envelope.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return resp.StatusCode, fmt.Errorf("CLI login request failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, body))
	}
	// Success payloads are bounded independently from the smaller error envelope.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(output); err != nil {
		return resp.StatusCode, errors.New("Engine returned an invalid CLI login response")
	}
	return resp.StatusCode, nil
}
