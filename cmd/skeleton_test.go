package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestPlanCmdSkeleton(t *testing.T) {
	// Temporarily override RunE to intercept execution without running logic
	origRunE := planCmd.RunE
	defer func() { planCmd.RunE = origRunE }()

	planCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Lookup("force-removals") != nil {
			t.Error("plan must not expose force-removals; force is a plan-action decision")
		}
		if cmd.Flags().Lookup("deprecate-removed-versions") != nil {
			t.Error("plan must not expose deprecate-removed-versions; deprecation is config intent")
		}
		return nil
	}

	RootCmd.SetArgs([]string{"plan"})
	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing plan: %v", err)
	}
}

func TestPlanCmdRejectsLegacyRemovalFlags(t *testing.T) {
	RootCmd.SetArgs([]string{"plan", "--force-removals"})
	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err == nil {
		t.Fatal("expected plan --force-removals to be rejected")
	}
}

func TestApplyCmdSkeleton(t *testing.T) {
	RootCmd.SetArgs([]string{"apply", "--download"})

	origRunE := applyCmd.RunE
	defer func() { applyCmd.RunE = origRunE }()

	applyCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !applyDownload {
			t.Error("expected download flag to be true")
		}
		return nil
	}

	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing apply: %v", err)
	}
}

func TestWorkspaceServiceAddCmdSkeleton(t *testing.T) {
	RootCmd.SetArgs([]string{"workspace", "service", "add", "okta", "--version", "2026-07-01"})

	origRunE := workspaceServiceAddCmd.RunE
	defer func() { workspaceServiceAddCmd.RunE = origRunE }()

	workspaceServiceAddCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || args[0] != "okta" {
			t.Errorf("expected arg 'okta', got %v", args)
		}
		if workspaceServiceAddVersion != "2026-07-01" {
			t.Errorf("expected version flag to be 2026-07-01, got %q", workspaceServiceAddVersion)
		}
		return nil
	}

	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing workspace service add: %v", err)
	}
}

func TestWorkspaceServiceVersionsCmdSkeleton(t *testing.T) {
	RootCmd.SetArgs([]string{"workspace", "service", "versions", "okta"})

	origRunE := workspaceServiceVersionsCmd.RunE
	defer func() { workspaceServiceVersionsCmd.RunE = origRunE }()

	workspaceServiceVersionsCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || args[0] != "okta" {
			t.Errorf("expected arg 'okta', got %v", args)
		}
		return nil
	}

	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing workspace service versions: %v", err)
	}
}

func TestSdkAddOperationCmdSkeleton(t *testing.T) {
	RootCmd.SetArgs([]string{"sdk", "operation", "add", "okta", "listLogEvents", "--interactive", "--apply", "--download"})

	origRunE := sdkAddOperationCmd.RunE
	defer func() { sdkAddOperationCmd.RunE = origRunE }()

	sdkAddOperationCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 2 || args[0] != "okta" || args[1] != "listLogEvents" {
			t.Errorf("expected args 'okta listLogEvents', got %v", args)
		}
		if !sdkAddOperationInteractive {
			t.Error("expected interactive flag to be true")
		}
		if !sdkAddOperationApply {
			t.Error("expected apply flag to be true")
		}
		if !sdkAddOperationDownload {
			t.Error("expected download flag to be true")
		}
		return nil
	}

	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing sdk operation add: %v", err)
	}
}

func TestSDKServiceAddCmdSkeleton(t *testing.T) {
	RootCmd.SetArgs([]string{"sdk", "service", "add", "okta", "--version", "2026-07-01"})

	origRunE := sdkAddServiceCmd.RunE
	defer func() { sdkAddServiceCmd.RunE = origRunE }()

	sdkAddServiceCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || args[0] != "okta" {
			t.Errorf("expected arg 'okta', got %v", args)
		}
		if sdkAddServiceVersion != "2026-07-01" {
			t.Errorf("expected service version flag, got %q", sdkAddServiceVersion)
		}
		return nil
	}

	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing sdk service add: %v", err)
	}
}

func TestWorkspaceServiceDeprecateCmdSkeleton(t *testing.T) {
	RootCmd.SetArgs([]string{"workspace", "service", "deprecate", "okta", "--at", "2026-10-01", "--reason", "migration"})

	origRunE := workspaceServiceDeprecateCmd.RunE
	defer func() { workspaceServiceDeprecateCmd.RunE = origRunE }()

	workspaceServiceDeprecateCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || args[0] != "okta" {
			t.Errorf("expected arg 'okta', got %v", args)
		}
		if workspaceServiceDeprecateAt != "2026-10-01" {
			t.Errorf("expected at flag to be set, got %q", workspaceServiceDeprecateAt)
		}
		if workspaceServiceDeprecateReason != "migration" {
			t.Errorf("expected reason flag to be set, got %q", workspaceServiceDeprecateReason)
		}
		return nil
	}

	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing workspace service deprecate: %v", err)
	}
}

func TestServiceVersionsCmdSkeleton(t *testing.T) {
	RootCmd.SetArgs([]string{"service", "versions", "okta"})

	origRunE := serviceVersionsCmd.RunE
	defer func() { serviceVersionsCmd.RunE = origRunE }()

	serviceVersionsCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || args[0] != "okta" {
			t.Errorf("expected arg 'okta', got %v", args)
		}
		return nil
	}

	out := new(bytes.Buffer)
	RootCmd.SetOut(out)
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error executing service versions: %v", err)
	}
}

func TestServiceVersionsRejectsLegacyNounFirstForm(t *testing.T) {
	out := runCommandInDirExpectError(t, t.TempDir(), "http://unused.invalid", []string{"service", "okta", "versions"})
	if !strings.Contains(out, `unknown command "okta"`) {
		t.Fatalf("expected legacy form to be rejected, got %q", out)
	}
}
