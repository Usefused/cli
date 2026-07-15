package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var workspaceInput io.Reader = os.Stdin

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage Fused workspace configuration",
	Long:  `Manage your central workspace policy, including allowed services and versions.`,
}

var workspacePlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan workspace configuration",
	RunE: WithTelemetry("cli.workspace.plan", func(cmd *cobra.Command, args []string) error {
		return runConfigPlan(planOptions{filter: filterWorkspace, jsonOut: workspacePlanJSON, receiptOut: workspacePlanReceiptOut})
	}),
}

var workspacePlanJSON bool
var workspacePlanReceiptOut string
var workspaceApplyPlanID string
var workspaceApplyReceiptPath string
var workspaceApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply workspace configuration",
	RunE: WithTelemetry("cli.workspace.apply", func(cmd *cobra.Command, args []string) error {
		warnIfProductionEnvironment(cmd)
		return runConfigApply(applyOptions{
			filter:      filterWorkspace,
			planID:      workspaceApplyPlanID,
			receiptPath: workspaceApplyReceiptPath,
		})
	}),
}

// warnIfProductionEnvironment is a best-effort UX nudge (Task 8,
// engine_workspace_registration_plan.md): `workspace apply` can activate or
// deactivate services workspace-wide, so before running it we check the
// Engine's /health echo of its --environment label and warn if it's
// "production" (the default -- most Engines will hit this unless an
// operator has explicitly labeled a non-production deployment). Never
// blocks or fails the apply: any error here (offline Engine, older Engine
// without the environment field, etc.) just means the warning is silently
// skipped.
func warnIfProductionEnvironment(cmd *cobra.Command) {
	client, err := getAPIClient()
	if err != nil {
		return
	}
	health, err := client.Health()
	if err != nil || health == nil || health.Environment == "" {
		return
	}
	if strings.EqualFold(health.Environment, "production") {
		fmt.Fprintf(cmd.OutOrStdout(), "Warning: applying against a production Engine (environment=%s).\n", health.Environment)
	}
}

var workspaceServicesCmd = &cobra.Command{
	Use:   "services",
	Short: "Manage workspace services",
}

var workspaceServicesListInteractive bool
var workspaceServicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace services",
	RunE: WithTelemetry("cli.workspace.services.list", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		services, err := client.ListWorkspaceServices()
		if err != nil {
			return err
		}
		if len(services) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No workspace services found.")
			return nil
		}

		if workspaceServicesListInteractive {
			scanner := bufio.NewScanner(workspaceInput)
			for i, service := range services {
				fmt.Fprintf(cmd.OutOrStdout(), "%d. %s\n", i+1, service.ServiceName)
			}
			fmt.Fprint(cmd.OutOrStdout(), "Select service: ")
			if !scanner.Scan() {
				return fmt.Errorf("no service selected")
			}
			choiceStr := scanner.Text()
			choice, err := strconv.Atoi(choiceStr)
			if err != nil || choice < 1 || choice > len(services) {
				return fmt.Errorf("invalid service selection")
			}
			selected := services[choice-1]
			fmt.Fprintf(cmd.OutOrStdout(), "Enabled Versions for %s: %s\n", selected.ServiceName, strings.Join(workspaceServiceVersionNames(selected), ", "))
			return nil
		}

		for _, service := range services {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", service.ServiceName, service.ServiceID, service.Version, strings.Join(workspaceServiceVersionNames(service), ", "))
		}
		return nil
	}),
}

var workspaceHasCmd = &cobra.Command{
	Use:   "has <service_name>",
	Short: "Check if a service is available in the workspace",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.workspace.has", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		serviceName := args[0]
		services, err := client.ListWorkspaceServices(serviceName)
		if err != nil {
			return err
		}
		for _, service := range services {
			if service.ServiceName == serviceName {
				fmt.Fprintf(cmd.OutOrStdout(), "Found service %s (Enabled Versions: %s)\n", service.ServiceName, strings.Join(workspaceServiceVersionNames(service), ", "))
				return nil
			}
		}
		return fmt.Errorf("service %s not found in workspace", serviceName)
	}),
}

var workspaceServiceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage a specific workspace service",
}

var workspaceServiceAddVersion string
var workspaceServiceAddID string
var workspaceServiceAddCmd = &cobra.Command{
	Use:   "add <service>",
	Short: "Add a service to workspace",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.workspace.service.add", func(cmd *cobra.Command, args []string) error {
		if err := addWorkspaceService(ConfigFile, args[0], workspaceServiceAddID, workspaceServiceAddVersion); err != nil {
			return err
		}
		fmt.Printf("Added service %s with version %s\n", args[0], workspaceServiceAddVersion)
		return nil
	}),
}

var workspaceServiceRemoveForce bool
var workspaceServiceRemoveCmd = &cobra.Command{
	Use:   "remove <service>",
	Short: "Remove a service from workspace",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.workspace.service.remove", func(cmd *cobra.Command, args []string) error {
		if err := removeWorkspaceService(ConfigFile, args[0]); err != nil {
			return err
		}
		fmt.Printf("Removed service %s\n", args[0])
		return nil
	}),
}

var workspaceServiceDeprecateAt string
var workspaceServiceDeprecateReason string
var workspaceServiceDeprecateCmd = &cobra.Command{
	Use:   "deprecate <service>",
	Short: "Add service deprecation intent to workspace config",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.workspace.service.deprecate", func(cmd *cobra.Command, args []string) error {
		if err := addWorkspaceDeprecation(ConfigFile, args[0], "", workspaceServiceDeprecateAt, workspaceServiceDeprecateReason); err != nil {
			return err
		}
		fmt.Printf("Added deprecation for service %s at %s\n", args[0], workspaceServiceDeprecateAt)
		return nil
	}),
}

var workspaceServiceVersionCmd = &cobra.Command{
	Use:   "version",
	Short: "Manage versions for a workspace service",
}

var workspaceServiceVersionAddCmd = &cobra.Command{
	Use:   "add <service> <version>",
	Short: "Add an allowed version to a workspace service",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.workspace.service.version.add", func(cmd *cobra.Command, args []string) error {
		if err := addWorkspaceVersion(ConfigFile, args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("Added version %s to service %s\n", args[1], args[0])
		return nil
	}),
}

var workspaceServiceVersionRemoveForce bool
var workspaceServiceVersionRemoveCmd = &cobra.Command{
	Use:   "remove <service> <version>",
	Short: "Remove an allowed version from a workspace service",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.workspace.service.version.remove", func(cmd *cobra.Command, args []string) error {
		if err := removeWorkspaceVersion(ConfigFile, args[0], args[1]); err != nil {
			return err
		}
		fmt.Printf("Removed version %s from service %s\n", args[1], args[0])
		return nil
	}),
}

var workspaceServiceVersionDeprecateAt string
var workspaceServiceVersionDeprecateReason string
var workspaceServiceVersionDeprecateCmd = &cobra.Command{
	Use:   "deprecate <service> <version>",
	Short: "Add service-version deprecation intent to workspace config",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.workspace.service.version.deprecate", func(cmd *cobra.Command, args []string) error {
		if err := addWorkspaceDeprecation(ConfigFile, args[0], args[1], workspaceServiceVersionDeprecateAt, workspaceServiceVersionDeprecateReason); err != nil {
			return err
		}
		fmt.Printf("Added deprecation for version %s of service %s at %s\n", args[1], args[0], workspaceServiceVersionDeprecateAt)
		return nil
	}),
}

func init() {
	RootCmd.AddCommand(workspaceCmd)

	workspaceCmd.AddCommand(workspacePlanCmd)
	workspacePlanCmd.Flags().BoolVar(&workspacePlanJSON, "json", false, "Print plan receipt JSON instead of writing default receipt")
	workspacePlanCmd.Flags().StringVar(&workspacePlanReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")
	workspaceCmd.AddCommand(workspaceApplyCmd)
	workspaceApplyCmd.Flags().StringVar(&workspaceApplyPlanID, "plan-id", "", "Apply a specific remote plan ID")
	workspaceApplyCmd.Flags().StringVar(&workspaceApplyReceiptPath, "receipt", "", "Read a specific plan receipt")

	workspaceCmd.AddCommand(workspaceServicesCmd)
	workspaceServicesListCmd.Flags().BoolVarP(&workspaceServicesListInteractive, "interactive", "i", false, "Interactive service selection")
	workspaceServicesCmd.AddCommand(workspaceServicesListCmd)
	workspaceCmd.AddCommand(workspaceServiceCmd)
	workspaceCmd.AddCommand(workspaceHasCmd)
	workspaceServiceCmd.AddCommand(workspaceServiceAddCmd)
	workspaceServiceAddCmd.Flags().StringVar(&workspaceServiceAddVersion, "version", "", "Version to enable; omitted resolves latest during plan")
	workspaceServiceAddCmd.Flags().StringVar(&workspaceServiceAddID, "service-id", "", "Registry service UUID to store in workspace config")

	workspaceServiceCmd.AddCommand(workspaceServiceRemoveCmd)
	workspaceServiceRemoveCmd.Flags().BoolVar(&workspaceServiceRemoveForce, "force", false, "Force removal when the generated plan action is applied")
	workspaceServiceCmd.AddCommand(workspaceServiceDeprecateCmd)
	workspaceServiceDeprecateCmd.Flags().StringVar(&workspaceServiceDeprecateAt, "at", "", "Deprecation effective date in YYYY-MM-DD")
	workspaceServiceDeprecateCmd.Flags().StringVar(&workspaceServiceDeprecateReason, "reason", "", "Reason for deprecation")
	workspaceServiceDeprecateCmd.MarkFlagRequired("at")

	workspaceServiceCmd.AddCommand(workspaceServiceVersionCmd)

	workspaceServiceVersionCmd.AddCommand(workspaceServiceVersionAddCmd)
	workspaceServiceVersionCmd.AddCommand(workspaceServiceVersionRemoveCmd)
	workspaceServiceVersionRemoveCmd.Flags().BoolVar(&workspaceServiceVersionRemoveForce, "force", false, "Force removal")
	workspaceServiceVersionCmd.AddCommand(workspaceServiceVersionDeprecateCmd)
	workspaceServiceVersionDeprecateCmd.Flags().StringVar(&workspaceServiceVersionDeprecateAt, "at", "", "Deprecation effective date in YYYY-MM-DD")
	workspaceServiceVersionDeprecateCmd.Flags().StringVar(&workspaceServiceVersionDeprecateReason, "reason", "", "Reason for deprecation")
	workspaceServiceVersionDeprecateCmd.MarkFlagRequired("at")
}
