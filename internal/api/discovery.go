package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	// DiscoveryProtocolVersion is the only Registry discovery protocol this CLI understands.
	DiscoveryProtocolVersion = 1
	// maxDiscoveryEventBytes mirrors the Registry metadata ceiling without admitting source bytes over SSE.
	maxDiscoveryEventBytes = 1 << 20
)

// DiscoveryState identifies the authoritative phase of a Registry discovery session.
type DiscoveryState string

const (
	DiscoveryStateResolveSource      DiscoveryState = "resolve_source"
	DiscoveryStateFetchSpec          DiscoveryState = "fetch_spec"
	DiscoveryStateCrawlDocs          DiscoveryState = "crawl_docs"
	DiscoveryStateDiscoverOperations DiscoveryState = "discover_operations"
	DiscoveryStateAwaitingSelection  DiscoveryState = "awaiting_selection"
	DiscoveryStateExtractContract    DiscoveryState = "extract_contract"
	DiscoveryStateEnrichContract     DiscoveryState = "enrich_contract"
	DiscoveryStateAwaitingReview     DiscoveryState = "awaiting_review"
	DiscoveryStatePlanReady          DiscoveryState = "plan_ready"
	DiscoveryStateError              DiscoveryState = "error"
	DiscoveryStateCancelled          DiscoveryState = "cancelled"
)

// DiscoveryAction identifies one exact user decision accepted by the session state machine.
type DiscoveryAction string

const (
	DiscoveryActionSelectOperations DiscoveryAction = "select_operations"
	DiscoveryActionAcceptEnrichment DiscoveryAction = "accept_enrichment"
	DiscoveryActionRejectEnrichment DiscoveryAction = "reject_enrichment"
	DiscoveryActionUpdateOverlay    DiscoveryAction = "update_overlay"
	DiscoveryActionRequestPlan      DiscoveryAction = "request_plan"
	DiscoveryActionCancel           DiscoveryAction = "cancel"
)

// DiscoveryEventType is the closed notification vocabulary for v1 session progress.
type DiscoveryEventType string

const (
	DiscoveryEventStateChanged         DiscoveryEventType = "state_changed"
	DiscoveryEventSourceCandidate      DiscoveryEventType = "source_candidate"
	DiscoveryEventSourceResolved       DiscoveryEventType = "source_resolved"
	DiscoveryEventCrawlProgress        DiscoveryEventType = "crawl_progress"
	DiscoveryEventOperationsDiscovered DiscoveryEventType = "operations_discovered"
	DiscoveryEventSelectionRequired    DiscoveryEventType = "selection_required"
	DiscoveryEventExtractionProgress   DiscoveryEventType = "extraction_progress"
	DiscoveryEventDraftReady           DiscoveryEventType = "draft_ready"
	DiscoveryEventEnrichmentProposed   DiscoveryEventType = "enrichment_proposed"
	DiscoveryEventReviewRequired       DiscoveryEventType = "review_required"
	DiscoveryEventPlanReady            DiscoveryEventType = "plan_ready"
	DiscoveryEventFailed               DiscoveryEventType = "failed"
	DiscoveryEventCancelled            DiscoveryEventType = "cancelled"
)

// DiscoveryCrawlRequest contains advisory crawl bounds that Registry clamps to operator ceilings.
type DiscoveryCrawlRequest struct {
	MaxPages int `json:"max_pages,omitempty"`
	MaxDepth int `json:"max_depth,omitempty"`
}

// DiscoveryStartRequest starts one auto, spec-only, or documentation-only discovery session.
type DiscoveryStartRequest struct {
	Name             string                `json:"name"`
	Slug             string                `json:"slug"`
	Version          string                `json:"version,omitempty"`
	SourceURL        string                `json:"source_url"`
	SourceMode       string                `json:"source_mode"`
	RequestedWorkers int                   `json:"requested_workers,omitempty"`
	Crawl            DiscoveryCrawlRequest `json:"crawl,omitempty"`
}

// DiscoverySnapshot is the complete resumable session identity returned by start and GET.
type DiscoverySnapshot struct {
	Version       int             `json:"version"`
	SessionID     string          `json:"session_id"`
	Revision      uint64          `json:"revision"`
	DraftRevision uint64          `json:"draft_revision,omitempty"`
	State         DiscoveryState  `json:"state"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

// DiscoveryEvent is the versioned progress envelope sent over SSE.
type DiscoveryEvent struct {
	Version   int                `json:"version"`
	SessionID string             `json:"session_id"`
	Revision  uint64             `json:"revision"`
	State     DiscoveryState     `json:"state"`
	Type      DiscoveryEventType `json:"type"`
	Payload   json.RawMessage    `json:"payload,omitempty"`
}

// DiscoveryOperation identifies one exact operation offered for selection.
type DiscoveryOperation struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Summary     string `json:"summary,omitempty"`
	Occurrences int    `json:"occurrences,omitempty"`
}

// DiscoveryDiagnostic is a bounded review message whose code and severity are automation-safe.
type DiscoveryDiagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// DiscoveryProposal summarizes one reviewable x-fused-* enrichment proposal.
type DiscoveryProposal struct {
	ID                   string                      `json:"id"`
	Extension            string                      `json:"extension"`
	Pointer              string                      `json:"pointer"`
	Scope                string                      `json:"scope"`
	Value                json.RawMessage             `json:"value"`
	Dependencies         []string                    `json:"dependencies,omitempty"`
	Rationale            string                      `json:"rationale"`
	Evidence             []DiscoveryProposalEvidence `json:"evidence"`
	Confidence           string                      `json:"confidence"`
	RequiresConfirmation bool                        `json:"requires_confirmation"`
}

// DiscoveryProposalEvidence binds one enrichment claim to an immutable admitted source.
type DiscoveryProposalEvidence struct {
	SourceID    string `json:"source_id"`
	ContentHash string `json:"content_hash"`
	SourceURL   string `json:"source_url"`
	Locator     string `json:"locator"`
	Fact        string `json:"fact"`
}

// DiscoveryContract identifies the immutable draft under review without exposing artifact internals.
type DiscoveryContract struct {
	DraftID       string `json:"draft_id"`
	DraftRevision uint64 `json:"draft_revision"`
	ReviewHash    string `json:"review_hash"`
}

// DiscoveryPlan identifies the exact ordinary import plan emitted after review.
type DiscoveryPlan struct {
	PlanID     string `json:"plan_id"`
	ReviewHash string `json:"review_hash"`
}

// DiscoveryPayload is the stable client projection used by operation selection and review.
type DiscoveryPayload struct {
	EffectiveWorkers int                   `json:"effective_workers,omitempty"`
	MaxPages         int                   `json:"max_pages,omitempty"`
	MaxDepth         int                   `json:"max_depth,omitempty"`
	MaxSelections    int                   `json:"max_selections,omitempty"`
	Operations       []DiscoveryOperation  `json:"operations,omitempty"`
	Proposals        []DiscoveryProposal   `json:"proposals,omitempty"`
	Diagnostics      []DiscoveryDiagnostic `json:"diagnostics,omitempty"`
	Contract         *DiscoveryContract    `json:"contract,omitempty"`
	Plan             *DiscoveryPlan        `json:"plan,omitempty"`
	FailureCode      string                `json:"failure_code,omitempty"`
}

// DiscoveryActionRequest binds a decision to the exact session and draft revisions reviewed by the caller.
type DiscoveryActionRequest struct {
	Version          int             `json:"version"`
	SessionID        string          `json:"session_id"`
	ExpectedRevision uint64          `json:"expected_revision"`
	DraftRevision    uint64          `json:"draft_revision,omitempty"`
	Action           DiscoveryAction `json:"action"`
	Payload          json.RawMessage `json:"payload,omitempty"`
}

// StartDiscovery creates a session and returns its first authoritative snapshot.
func (c *Client) StartDiscovery(ctx context.Context, request DiscoveryStartRequest) (*DiscoverySnapshot, error) {
	var snapshot DiscoverySnapshot
	if err := c.postDiscoveryJSON(ctx, "/integrations/start", request, http.StatusAccepted, &snapshot); err != nil {
		return nil, err
	}
	if err := validateDiscoverySnapshot(snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// GetDiscoverySession reloads the authoritative snapshot after stream reconnects or actions.
func (c *Client) GetDiscoverySession(ctx context.Context, sessionID string) (*DiscoverySnapshot, error) {
	path := "/integrations/session/" + url.PathEscape(sessionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	c.addDiscoveryAuth(request)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, discoveryHTTPError(path, response)
	}
	var snapshot DiscoverySnapshot
	if err := json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryEventBytes+1)).Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("decode discovery snapshot: %w", err)
	}
	if err := validateDiscoverySnapshot(snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// ApplyDiscoveryAction sends one typed decision and returns the committed next snapshot.
func (c *Client) ApplyDiscoveryAction(ctx context.Context, sessionID string, request DiscoveryActionRequest) (*DiscoverySnapshot, error) {
	path := "/integrations/session/" + url.PathEscape(sessionID) + "/actions"
	var snapshot DiscoverySnapshot
	if err := c.postDiscoveryJSON(ctx, path, request, http.StatusOK, &snapshot); err != nil {
		return nil, err
	}
	if err := validateDiscoverySnapshot(snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// StreamDiscovery consumes versioned progress envelopes; callers must reload a snapshot after disconnect.
func (c *Client) StreamDiscovery(ctx context.Context, sessionID string, onEvent func(DiscoveryEvent) error) error {
	if onEvent == nil {
		return errors.New("discovery stream callback is required")
	}
	path := "/integrations/session/" + url.PathEscape(sessionID) + "/stream"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	c.addDiscoveryAuth(request)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return discoveryHTTPError(path, response)
	}
	return readDiscoveryEvents(response.Body, sessionID, onEvent)
}

// DecodeDiscoveryPayload strictly decodes the bounded public payload used by UI and CLI decisions.
func DecodeDiscoveryPayload(raw json.RawMessage) (DiscoveryPayload, error) {
	if len(raw) == 0 {
		return DiscoveryPayload{}, nil
	}
	if len(raw) > maxDiscoveryEventBytes {
		return DiscoveryPayload{}, errors.New("discovery payload exceeds its metadata limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload DiscoveryPayload
	if err := decoder.Decode(&payload); err != nil {
		return DiscoveryPayload{}, fmt.Errorf("decode discovery payload: %w", err)
	}
	if err := requireDiscoveryEOF(decoder); err != nil {
		return DiscoveryPayload{}, err
	}
	return payload, nil
}

// postDiscoveryJSON executes one exact-status JSON mutation against the discovery boundary.
func (c *Client) postDiscoveryJSON(ctx context.Context, path string, requestBody any, successStatus int, output any) error {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	c.addDiscoveryAuth(request)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != successStatus {
		return discoveryHTTPError(path, response)
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, maxDiscoveryEventBytes+1)).Decode(output)
}

// addDiscoveryAuth applies the same API-key header as every other Registry client request.
func (c *Client) addDiscoveryAuth(request *http.Request) {
	if c.APIKey != "" {
		request.Header.Set("x-api-key", c.APIKey)
	}
}

// discoveryHTTPError converts a bounded Registry failure body into the CLI's shared HTTP error.
func discoveryHTTPError(path string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxDiscoveryEventBytes+1))
	return fmt.Errorf("%s failed (HTTP %d): %w", strings.TrimPrefix(path, "/"), response.StatusCode, newHTTPError(response.StatusCode, body))
}

// readDiscoveryEvents parses SSE frames while bounding each accumulated metadata event.
func readDiscoveryEvents(reader io.Reader, sessionID string, onEvent func(DiscoveryEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxDiscoveryEventBytes)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatchDiscoveryData(data, sessionID, onEvent); err != nil {
				return err
			}
			data = nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if payload, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimSpace(payload))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return dispatchDiscoveryData(data, sessionID, onEvent)
}

// dispatchDiscoveryData validates identity before exposing one event to presentation logic.
func dispatchDiscoveryData(data []string, sessionID string, onEvent func(DiscoveryEvent) error) error {
	raw := strings.TrimSpace(strings.Join(data, "\n"))
	if raw == "" {
		return nil
	}
	if len(raw) > maxDiscoveryEventBytes {
		return errors.New("discovery event exceeds its metadata limit")
	}
	var event DiscoveryEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return fmt.Errorf("decode discovery stream event: %w", err)
	}
	if err := validateDiscoveryEvent(event, sessionID); err != nil {
		return err
	}
	return onEvent(event)
}

// validateDiscoverySnapshot rejects protocol aliases and incomplete resumable identities.
func validateDiscoverySnapshot(snapshot DiscoverySnapshot) error {
	if snapshot.Version != DiscoveryProtocolVersion || snapshot.SessionID == "" || snapshot.Revision == 0 || !knownDiscoveryState(snapshot.State) {
		return errors.New("Registry returned an invalid discovery snapshot")
	}
	if len(snapshot.Payload) > maxDiscoveryEventBytes {
		return errors.New("discovery snapshot payload exceeds its metadata limit")
	}
	return nil
}

// validateDiscoveryEvent ensures a stream cannot cross sessions or protocols unnoticed.
func validateDiscoveryEvent(event DiscoveryEvent, sessionID string) error {
	if event.Version != DiscoveryProtocolVersion || event.SessionID != sessionID || event.Revision == 0 || !knownDiscoveryState(event.State) || !knownDiscoveryEvent(event.Type) {
		return errors.New("Registry returned an invalid discovery event")
	}
	if len(event.Payload) > maxDiscoveryEventBytes {
		return errors.New("discovery event payload exceeds its metadata limit")
	}
	return nil
}

// knownDiscoveryEvent rejects historical or invented event labels rather than
// treating a non-empty string as a valid signal to client decision logic.
func knownDiscoveryEvent(event DiscoveryEventType) bool {
	switch event {
	case DiscoveryEventStateChanged, DiscoveryEventSourceCandidate,
		DiscoveryEventSourceResolved, DiscoveryEventCrawlProgress,
		DiscoveryEventOperationsDiscovered, DiscoveryEventSelectionRequired,
		DiscoveryEventExtractionProgress, DiscoveryEventDraftReady,
		DiscoveryEventEnrichmentProposed, DiscoveryEventReviewRequired,
		DiscoveryEventPlanReady, DiscoveryEventFailed, DiscoveryEventCancelled:
		return true
	default:
		return false
	}
}

// knownDiscoveryState admits only the state vocabulary implemented by the current Registry contract.
func knownDiscoveryState(state DiscoveryState) bool {
	switch state {
	case DiscoveryStateResolveSource, DiscoveryStateFetchSpec, DiscoveryStateCrawlDocs,
		DiscoveryStateDiscoverOperations, DiscoveryStateAwaitingSelection,
		DiscoveryStateExtractContract, DiscoveryStateEnrichContract,
		DiscoveryStateAwaitingReview, DiscoveryStatePlanReady,
		DiscoveryStateError, DiscoveryStateCancelled:
		return true
	default:
		return false
	}
}

// requireDiscoveryEOF rejects concatenated JSON values that could be interpreted inconsistently.
func requireDiscoveryEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("discovery payload contains trailing JSON")
	}
	return nil
}
