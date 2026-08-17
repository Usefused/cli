package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestReadSDKInvokeParamsRequiresOneJSONObject verifies strict request parameter framing.
func TestReadSDKInvokeParamsRequiresOneJSONObject(t *testing.T) {
	data, err := readSDKInvokeParams(`{"count":1}`, false, strings.NewReader(""))
	if err != nil || string(data) != `{"count":1}` {
		t.Fatalf("params = %q, %v", data, err)
	}
	for _, invalid := range []string{`[]`, `null`, `{"ok":true} {"extra":true}`} {
		if _, err := readSDKInvokeParams(invalid, false, strings.NewReader("")); err == nil {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
}

// TestReadSDKInvokeParamsAndTokenCannotShareStdin verifies unambiguous stdin ownership.
func TestReadSDKInvokeParamsAndTokenCannotShareStdin(t *testing.T) {
	if _, err := readSDKInvokeParams("-", true, strings.NewReader(`{}`)); err == nil {
		t.Fatal("expected shared stdin to be rejected")
	}
}

// TestReadSDKInvokeTokenUsesOnlyExplicitRuntimeSources verifies credential boundary isolation.
func TestReadSDKInvokeTokenUsesOnlyExplicitRuntimeSources(t *testing.T) {
	oldStdin, oldEnv := sdkInvokeTokenStdin, sdkInvokeTokenEnv
	t.Cleanup(func() { sdkInvokeTokenStdin, sdkInvokeTokenEnv = oldStdin, oldEnv })
	t.Setenv(defaultSDKTokenEnvironment, "runtime-token")
	t.Setenv("FUSED_API_KEY", "control-token")
	t.Setenv("FUSED_LICENSE_KEY", "license-token")
	sdkInvokeTokenStdin, sdkInvokeTokenEnv = false, defaultSDKTokenEnvironment
	token, err := readSDKInvokeToken(bytes.NewBuffer(nil))
	if err != nil || token != "runtime-token" {
		t.Fatalf("token = %q, %v", token, err)
	}
	t.Setenv(defaultSDKTokenEnvironment, "")
	if _, err := readSDKInvokeToken(bytes.NewBuffer(nil)); err == nil || strings.Contains(err.Error(), "FUSED_API_KEY") {
		t.Fatalf("runtime token failure = %v", err)
	}
}

// TestSDKRuntimeFrameErrorPreservesStructuredEngineCode verifies Engine error propagation.
func TestSDKRuntimeFrameErrorPreservesStructuredEngineCode(t *testing.T) {
	err := sdkRuntimeFrameError(`{"code":"execution_timeout","message":"Execution exceeded its Engine policy."}`, 504)
	var invokeErr *sdkInvokeError
	if !errors.As(err, &invokeErr) {
		t.Fatalf("error type = %T", err)
	}
	if invokeErr.code != "execution_timeout" || invokeErr.message != "Execution exceeded its Engine policy." {
		t.Fatalf("invoke error = %#v", invokeErr)
	}
	if invokeErr.details["provider_http_status"] != int32(504) {
		t.Fatalf("details = %#v", invokeErr.details)
	}
}

// TestResolveSDKInvokeGRPCURLRequiresDedicatedEndpoint verifies explicit transport selection.
func TestResolveSDKInvokeGRPCURLRequiresDedicatedEndpoint(t *testing.T) {
	old := sdkInvokeGRPCURL
	t.Cleanup(func() { sdkInvokeGRPCURL = old })
	t.Setenv("FUSED_ENGINE_GRPC_URL", "")
	t.Setenv("FUSED_ENGINE_URL", "https://rest.example.com")
	sdkInvokeGRPCURL = ""
	if _, err := resolveSDKInvokeGRPCURL(); err == nil {
		t.Fatal("expected missing dedicated gRPC URL to fail")
	}
	sdkInvokeGRPCURL = "https://exec.example.com"
	if got, err := resolveSDKInvokeGRPCURL(); err != nil || got != sdkInvokeGRPCURL {
		t.Fatalf("gRPC URL = %q, %v", got, err)
	}
}
