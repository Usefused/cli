package cmd

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"io"
	"strings"
	"testing"
)

// TestRuntimeBucketInvocation restricts the public flag to a nonzero UUID on authored execution.
func TestRuntimeBucketInvocation(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		kind, operation, value string
		valid                  bool
	}{
		{"unified_app", "execute", id, true}, {"sdk", "execute", id, false}, {"unified_app", "get", id, false},
		{"unified_app", "execute", "tenant", false}, {"unified_app", "execute", "", false}, {"unified_app", "execute", uuid.Nil.String(), false},
	} {
		cmd := &cobra.Command{}
		cmd.Flags().String("bucket-id", "", "")
		// Tests explicitly set the flag so an empty value cannot silently mean omission.
		if err := cmd.Flags().Set("bucket-id", tc.value); err != nil {
			t.Fatal(err)
		}
		got, err := runtimeBucketInvocationFlag(cmd, tc.kind, tc.operation)
		if (err == nil) != tc.valid {
			t.Fatalf("kind=%s value=%s got=%s err=%v", tc.kind, tc.value, got, err)
		}
	}
}

// TestRuntimeBucketRequestWire keeps the routing selector separate from the app's business input.
func TestRuntimeBucketRequestWire(t *testing.T) {
	id := uuid.NewString()
	request, _, err := newSDKInvokeHTTPRequest(context.Background(), preparedSDKInvocation{EngineURL: "https://engine.example", AppID: uuid.NewString(), Token: "test-token", Request: sdkInvokeRequest{Operation: "execute", Input: json.RawMessage(`{"customer":"a"}`), BucketID: id}})
	// Serialization must preserve the selector without copying the execution token into JSON.
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil || !strings.Contains(string(body), `"bucket_id":"`+id+`"`) || strings.Contains(string(body), "test-token") {
		t.Fatalf("invalid routing envelope: err=%v", err)
	}
}
