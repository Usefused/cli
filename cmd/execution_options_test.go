package cmd

import (
	"strings"
	"testing"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
)

func TestValidateExecutionOptions(t *testing.T) {
	oldTimeout, oldRequestID := RequestTimeout, RequestID
	t.Cleanup(func() {
		RequestTimeout, RequestID = oldTimeout, oldRequestID
	})

	RequestTimeout = 0
	if err := validateExecutionOptions(); err == nil || !strings.Contains(err.Error(), "--timeout") {
		t.Fatalf("expected timeout validation error, got %v", err)
	}
	RequestTimeout = time.Second
	RequestID = "contains a space"
	if err := validateExecutionOptions(); err == nil || !strings.Contains(err.Error(), "--request-id") {
		t.Fatalf("expected request ID validation error, got %v", err)
	}
	RequestID = "deploy:prod-42_attempt.1"
	if err := validateExecutionOptions(); err != nil {
		t.Fatalf("expected valid execution options: %v", err)
	}
}

func TestNonInteractiveHonorsFlagAndCI(t *testing.T) {
	oldNoInput := NoInput
	t.Cleanup(func() { NoInput = oldNoInput })
	t.Setenv("CI", "")

	NoInput = true
	if err := requireInteractive("provide flags"); err == nil {
		t.Fatal("expected --no-input to reject a prompt")
	}
	NoInput = false
	t.Setenv("CI", "true")
	if err := requireInteractive("provide flags"); err == nil {
		t.Fatal("expected CI=true to reject a prompt")
	}
}

func TestNoInputStopsPromptingHelpersBeforeSideEffects(t *testing.T) {
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })

	if _, err := connectSetFields("oauth2", "oauth", ""); err == nil {
		t.Fatal("expected connect prompt to be rejected")
	}
	if _, _, err := selectSDKOperationsInteractively("missing.yaml", ""); err == nil {
		t.Fatal("expected SDK prompt to be rejected before reading config")
	}
	if _, err := resolveExplicitBucketID("production"); err == nil {
		t.Fatal("expected bucket names to be rejected before any lookup or prompt")
	}
	endpoints := []cliapi.Integration{{Method: "GET", Path: "/users"}}
	if _, err := reviewDocsEndpoints(RootCmd, endpoints); err == nil {
		t.Fatal("expected docs review prompt to be rejected")
	}
}

func TestUpdateCheckDisabledForAutomation(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("FUSED_NO_UPDATE_CHECK", "")
	if !updateCheckDisabled() {
		t.Fatal("expected CI=true to disable update checks")
	}
	t.Setenv("CI", "")
	t.Setenv("FUSED_NO_UPDATE_CHECK", "1")
	if !updateCheckDisabled() {
		t.Fatal("expected FUSED_NO_UPDATE_CHECK=1 to disable update checks")
	}
}
