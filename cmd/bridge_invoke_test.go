package cmd

import (
	"fmt"
	"testing"
)

// TestBridgeInvokeDecodesSharedRESTEnvelope preserves Bridge results while rejecting malformed or unrelated success shapes.
func TestBridgeInvokeDecodesSharedRESTEnvelope(t *testing.T) {
	raw := fmt.Sprintf(`{"app_id":%q,"operation":"bridge:echo_device","kind":"bridge","status_code":200,"results":[{"online":true}]}`, sdkInvokeTestAppID)
	response, err := decodeSDKInvokeHTTPResponse([]byte(raw))
	// A Bridge result must be consumable through the ordinary app invoke decoder.
	if err != nil || response.Kind != "bridge" || len(response.Results) != 1 {
		t.Fatalf("response=%+v error=%v", response, err)
	}
	for _, invalid := range []string{
		fmt.Sprintf(`{"app_id":%q,"operation":"bridge:echo_device","kind":"bridge","status_code":200,"results":[]}`, sdkInvokeTestAppID),
		fmt.Sprintf(`{"app_id":%q,"operation":"items.get","kind":"bridge","status_code":200,"results":[{}]}`, sdkInvokeTestAppID),
		`{"online":true}`,
	} {
		// Bare results or an invalid namespace cannot prove which app operation completed.
		if _, err := decodeSDKInvokeHTTPResponse([]byte(invalid)); err == nil {
			t.Fatal("invalid Bridge response accepted")
		}
	}
}
