package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const maxSDKBundleBytes = 2 << 20
const maxSDKBundleManifestBytes = 1 << 20

// SDKBundleAttachResult contains only the control receipt, never compiled code or credentials.
type SDKBundleAttachResult struct {
	Status string `json:"status"`
	AppID  string `json:"app_id"`
}

// AttachSDKBundle sends one compiled artifact to the exact immutable App version control route.
func (c *Client) AttachSDKBundle(appID, sourceHash string, script []byte, manifest json.RawMessage) (*SDKBundleAttachResult, error) {
	// Local bounds avoid an oversized control request before authentication or network work.
	if err := validateSDKBundleAttachInput(appID, sourceHash, script, manifest); err != nil {
		return nil, err
	}
	var declaration map[string]json.RawMessage
	// The Engine owns semantic admission, while the client rejects a malformed manifest document early.
	if err := json.Unmarshal(manifest, &declaration); err != nil || declaration == nil {
		return nil, errors.New("SDK bundle manifest must be a JSON object")
	}
	return c.postSDKBundle(appID, sourceHash, script, manifest)
}

// AttachExecutionBundle names the distinct hosted App kind while retaining the exact version-scoped route.
func (c *Client) AttachExecutionBundle(appID, sourceHash string, script []byte, manifest json.RawMessage) (*SDKBundleAttachResult, error) {
	return c.AttachSDKBundle(appID, sourceHash, script, manifest)
}

// validateSDKBundleAttachInput rejects malformed identities and oversized bytes before network work.
func validateSDKBundleAttachInput(appID, sourceHash string, script []byte, manifest json.RawMessage) error {
	parsedID, err := uuid.Parse(appID)
	// Canonical path identity avoids accepting alternate UUID spellings with different route behavior.
	if err != nil || parsedID.String() != appID {
		return errors.New("invalid App version ID")
	}
	// The applied plan receipt uses canonical sha256; a caller label is not deployment authority.
	if !validSDKBundleSourceHash(sourceHash) {
		return errors.New("invalid SDK source hash")
	}
	// Bound script and manifest independently to match the Engine's control route.
	if !validSDKBundleSizes(script, manifest) {
		return errors.New("invalid SDK bundle attachment")
	}
	return nil
}

// validSDKBundleSourceHash recognizes only the canonical plan receipt digest spelling.
func validSDKBundleSourceHash(sourceHash string) bool {
	// The exact prefix and size rule exclude caller labels before hex decoding.
	if !strings.HasPrefix(sourceHash, "sha256:") || len(sourceHash) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(sourceHash, "sha256:"))
	// Uppercase hex has another spelling for the same bytes and cannot be a canonical source identity.
	return err == nil && strings.ToLower(sourceHash) == sourceHash
}

// validSDKBundleSizes applies the same independent artifact limits as Engine admission.
func validSDKBundleSizes(script []byte, manifest json.RawMessage) bool {
	// Both artifacts must fit independently because Engine stores code and declaration separately.
	return len(script) > 0 && len(script) <= maxSDKBundleBytes && len(manifest) > 0 && len(manifest) <= maxSDKBundleManifestBytes
}

// postSDKBundle sends bounded admitted material through the saved management credential.
func (c *Client) postSDKBundle(appID, sourceHash string, script []byte, manifest json.RawMessage) (*SDKBundleAttachResult, error) {
	body, err := json.Marshal(struct {
		SourceHash string          `json:"source_hash"`
		BundleJS   string          `json:"bundle_js"`
		Manifest   json.RawMessage `json:"manifest"`
	}{SourceHash: sourceHash, BundleJS: string(script), Manifest: manifest})
	if err != nil {
		return nil, errors.New("encode SDK bundle attachment")
	}
	// JSON escaping can expand bounded files beyond the Engine's complete request limit.
	if len(body) > 3<<20 {
		return nil, errors.New("encoded SDK bundle attachment is too large")
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/apps/"+appID+"/bundle", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("create SDK bundle request")
	}
	req.Header.Set("Content-Type", "application/json")
	// The bundle route is a management mutation, authenticated by the saved CLI credential.
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Engine error projection is bounded and must not print submitted code or credentials.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("attach SDK bundle failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, readBoundedHTTPErrorBody(resp.Body)))
	}
	var result SDKBundleAttachResult
	// A malformed success is not proof the immutable artifact was accepted.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&result); err != nil || result.Status != "attached" || result.AppID != appID {
		return nil, errors.New("Fused returned an invalid SDK bundle receipt")
	}
	return &result, nil
}
