package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

func TestGetServiceOperationKeepsBodyContractsOptIn(t *testing.T) {
	tests := []struct {
		name          string
		options       api.ServiceOperationDetailOptions
		wantRequest   bool
		wantResponses bool
	}{
		{name: "summary only"},
		{name: "request and responses", options: api.ServiceOperationDetailOptions{IncludeRequest: true, IncludeResponses: true}, wantRequest: true, wantResponses: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newServiceOperationServer(t, test.wantRequest, test.wantResponses)
			defer server.Close()

			operation, err := api.NewClient(server.URL, "test-key").GetServiceOperation("svc-1", "v2", "createPayment", test.options)
			if err != nil {
				t.Fatalf("GetServiceOperation: %v", err)
			}
			if operation.Description != "Create a payment" || len(operation.Parameters) != 1 {
				t.Fatalf("operation summary = %#v", operation)
			}
			if (operation.RequestContent != nil) != test.wantRequest {
				t.Fatalf("request content present = %t, want %t", operation.RequestContent != nil, test.wantRequest)
			}
			if (len(operation.Responses) > 0) != test.wantResponses {
				t.Fatalf("responses present = %t, want %t", len(operation.Responses) > 0, test.wantResponses)
			}
		})
	}
}

func newServiceOperationServer(t *testing.T, includeRequest, includeResponses bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode GraphQL body: %v", err)
		}
		if body.Variables["serviceId"] != "svc-1" || body.Variables["version"] != "v2" || body.Variables["name"] != "createPayment" {
			t.Fatalf("variables = %#v", body.Variables)
		}
		if strings.Contains(body.Query, "request_content") != includeRequest {
			t.Fatalf("request_content selection = %t, want %t", strings.Contains(body.Query, "request_content"), includeRequest)
		}
		if strings.Contains(body.Query, "responses") != includeResponses {
			t.Fatalf("responses selection = %t, want %t", strings.Contains(body.Query, "responses"), includeResponses)
		}
		optional := ""
		if includeRequest {
			optional += `,"request_content":{"required":true,"representations":[{"media_type":"application/json","serialization":"json"}]}`
		}
		if includeResponses {
			optional += `,"responses":{"201":{"description":"created","representations":[{"media_type":"application/json"}]}}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"endpointByName":{"id":"op-1","service_id":"svc-1","name":"createPayment","description":"Create a payment","method":"POST","path":"/payments","deprecated":false,"parameters":[{"name":"idempotency-key","in":"header","required":true,"type":"string","description":"Prevents duplicate payments"}],"security_requirements":[{"schemes":[{"scheme":"bearer","scopes":["payments:write"]}]}]` + optional + `}}}`))
	}))
}

func TestGetServiceOperationReportsMissingOperation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"endpointByName":null}}`))
	}))
	defer server.Close()

	_, err := api.NewClient(server.URL, "test-key").GetServiceOperation("svc-1", "v2", "missing", api.ServiceOperationDetailOptions{})
	if err == nil || !strings.Contains(err.Error(), "operation missing not found in service version v2") {
		t.Fatalf("error = %v", err)
	}
}
