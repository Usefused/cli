package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// UnifiedAppScheduleInput contains only manager-authored fields and an optimistic revision.
type UnifiedAppScheduleInput struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Cron     string          `json:"cron"`
	Timezone string          `json:"timezone"`
	RunAt    *string         `json:"run_at"`
	Enabled  bool            `json:"enabled"`
	Revision int64           `json:"revision"`
}

// UnifiedAppSchedule exposes private saved input only on an explicit JSON listing.
type UnifiedAppSchedule struct {
	UnifiedAppScheduleInput
	NextRunAt       *string `json:"next_run_at"`
	LastExecutionID *string `json:"last_execution_id"`
	LastAppID       *string `json:"last_app_id"`
	LastStatus      string  `json:"last_status"`
}

// ResolveUnifiedAppScheduleTarget resolves a named family and uses a retained version only as its management route.
func (c *Client) ResolveUnifiedAppScheduleTarget(name string) (string, error) {
	family, err := c.resolveAppFamilyReference(name, "unified_app")
	// Denials and ambiguous names must not fall back to another adapter.
	if err != nil {
		return "", err
	}
	var result struct {
		Versions []struct {
			ID string `json:"app_id"`
		} `json:"appVersions"`
	}
	err = c.EngineGraphQL(`query ScheduleAppVersions($family: String!) { appVersions(app_family_id: $family) { app_id } }`, map[string]interface{}{"family": family}, &result)
	// The selected route grants family management; it does not pin future execution to this version.
	if err != nil {
		return "", err
	}
	if len(result.Versions) == 0 {
		return "", errors.New("Unified App has no retained version; deploy a version before managing schedules")
	}
	return result.Versions[0].ID, nil
}

// ListUnifiedAppSchedules reads saved configurations and bounded last-run summaries without executing anything.
func (c *Client) ListUnifiedAppSchedules(appID string) ([]UnifiedAppSchedule, error) {
	var result struct {
		Schedules []UnifiedAppSchedule `json:"schedules"`
	}
	err := c.scheduleRequest(http.MethodGet, appID, "", nil, &result)
	return result.Schedules, err
}

// SaveUnifiedAppSchedule sends one create or optimistic edit and never automatically retries uncertain writes.
func (c *Client) SaveUnifiedAppSchedule(appID string, item UnifiedAppScheduleInput) error {
	body, err := json.Marshal(item)
	// Invalid JSON input must not reach the credentialed transport.
	if err != nil {
		return err
	}
	var result struct {
		ID    string `json:"id"`
		Saved bool   `json:"saved"`
	}
	if err := c.scheduleRequest(http.MethodPut, appID, "", body, &result); err != nil {
		return err
	}
	// A malformed response cannot be reported as confirmed persistence.
	if !result.Saved || result.ID != item.ID {
		return errors.New("invalid schedule save receipt; list schedules before retrying")
	}
	return nil
}

// DeleteUnifiedAppSchedule protects a concurrently edited or dispatched schedule with its reviewed revision.
func (c *Client) DeleteUnifiedAppSchedule(appID, id string, revision int64) error {
	// A schedule ID is a path segment, never a caller-supplied URL.
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("schedule ID must be a UUID")
	}
	var result struct {
		Deleted bool `json:"deleted"`
	}
	err := c.scheduleRequest(http.MethodDelete, appID, "/"+id+"?revision="+strconv.FormatInt(revision, 10), nil, &result)
	if err != nil {
		return err
	}
	if !result.Deleted {
		return errors.New("invalid schedule delete receipt")
	}
	return nil
}

// scheduleRequest uses the same management credential boundary as app lifecycle commands.
func (c *Client) scheduleRequest(method, appID, suffix string, body []byte, out any) error {
	// Exact route identity prevents names and untrusted path syntax from entering the URL.
	if _, err := uuid.Parse(appID); err != nil {
		return errors.New("invalid Unified App version ID")
	}
	request, err := http.NewRequest(method, c.BaseURL+"/apps/"+appID+"/schedules"+suffix, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-api-key", c.APIKey)
	response, err := c.doRequest(request)
	// Uncertain mutations are returned to the caller without another side-effectful request.
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("schedule request failed: %w", newHTTPError(response.StatusCode, readBoundedHTTPErrorBody(response.Body)))
	}
	return json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(out)
}
