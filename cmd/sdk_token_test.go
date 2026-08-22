package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestSDKTokenGenerateRequestDefaultsToFullAccessWithoutExpiry preserves service-token compatibility.
func TestSDKTokenGenerateRequestDefaultsToFullAccessWithoutExpiry(t *testing.T) {
	command := newSDKTokenGenerateTestCommand()
	request, err := sdkTokenGenerateRequest(command, "local-service")
	// A valid default request should not fail local CLI validation.
	if err != nil {
		t.Fatalf("sdkTokenGenerateRequest: %v", err)
	}
	// SDK scope remains implicit and expiry remains optional for long-running services.
	if request.Name != "local-service" || len(request.Allow) != 0 || request.ExpiresIn != nil {
		t.Fatalf("request = %#v", request)
	}
}

// TestSDKTokenGenerateRequestParsesExpiry verifies temporary tester access reaches the Engine request.
func TestSDKTokenGenerateRequestParsesExpiry(t *testing.T) {
	command := newSDKTokenGenerateTestCommand()
	// The command flag is the user-facing input boundary for short-lived access.
	if err := command.Flags().Set("expires-in", "4h"); err != nil {
		t.Fatal(err)
	}
	request, err := sdkTokenGenerateRequest(command, "external-tester")
	// A supported whole-second duration should produce a request rather than a local error.
	if err != nil {
		t.Fatalf("sdkTokenGenerateRequest: %v", err)
	}
	// Four hours must be transported as the exact Engine whole-second policy.
	if request.ExpiresIn == nil || *request.ExpiresIn != 14400 {
		t.Fatalf("expires_in = %#v, want 14400", request.ExpiresIn)
	}
}

// TestSDKTokenGenerateRequestRejectsInvalidExpiry proves SDK generation uses shared validation.
func TestSDKTokenGenerateRequestRejectsInvalidExpiry(t *testing.T) {
	command := newSDKTokenGenerateTestCommand()
	// Sub-second input must not be rounded into a different access lifetime.
	if err := command.Flags().Set("expires-in", "1500ms"); err != nil {
		t.Fatal(err)
	}
	_, err := sdkTokenGenerateRequest(command, "external-tester")
	// Rejection before mutation is the safe boundary for malformed CLI input.
	if err == nil {
		t.Fatal("sdkTokenGenerateRequest accepted a sub-second expiry")
	}
}

// newSDKTokenGenerateTestCommand mirrors only the production flag needed by request parsing.
func newSDKTokenGenerateTestCommand() *cobra.Command {
	command := &cobra.Command{Use: "generate"}
	addAppTokenExpiryFlag(command)
	return command
}
