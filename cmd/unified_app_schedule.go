package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// newUnifiedScheduleCommand keeps UI and CLI on one durable Engine scheduling contract.
func newUnifiedScheduleCommand() *cobra.Command {
	parent := &cobra.Command{Use: "schedule", Short: "Schedule one-time or recurring Unified App runs", Args: cobra.NoArgs, RunE: requireSubcommand,
		Long: "Schedules belong to the app and execute its active version with saved input. Engine must be running. Missed recurring times produce one catch-up run; failed runs are not automatically retried. App management permission is required."}
	// Explicit verbs authorize only the selected schedule mutation, never app deployment or execution now.
	for _, action := range []string{"list", "create", "update", "pause", "resume", "delete"} {
		action := action
		command := &cobra.Command{Use: action + " [app-name]", Short: strings.ToUpper(action[:1]) + action[1:] + " Unified App schedules", Args: cobra.MaximumNArgs(1)}
		// Each invocation resolves its app from an explicit name or the existing -f config selector.
		command.RunE = WithTelemetry("cli.unified.schedule."+action, func(cmd *cobra.Command, args []string) error { return runUnifiedSchedule(cmd, args, action) })
		command.Flags().Bool("json", false, "Print JSON (list includes private saved input)")
		// Only targeted mutations need an existing schedule identity.
		if action != "list" && action != "create" {
			command.Flags().String("id", "", "Schedule ID from schedule list")
			_ = command.MarkFlagRequired("id")
		}
		// Creation and editing share timing and input flags; omitted update flags preserve saved values.
		if action == "create" || action == "update" {
			addScheduleWriteFlags(command)
		}
		parent.AddCommand(command)
	}
	return parent
}

// addScheduleWriteFlags keeps JSON inputs out of shell history when supplied with --input-file.
func addScheduleWriteFlags(cmd *cobra.Command) {
	cmd.Flags().String("name", "", "Schedule name")
	cmd.Flags().String("at", "", "One-time RFC3339 timestamp with offset, for example 2026-12-01T09:00:00Z")
	cmd.Flags().String("cron", "", "Recurring five-field cron, for example '0 9 * * 1-5'")
	cmd.Flags().String("timezone", "UTC", "IANA timezone for recurrence, for example Europe/London")
	cmd.Flags().String("input", "", "Input JSON; prefer --input-file for private values")
	cmd.Flags().String("input-file", "", "Read input JSON from a file, or - for stdin")
	cmd.Flags().Bool("paused", false, "Save without enabling execution")
	cmd.MarkFlagsMutuallyExclusive("at", "cron")
	cmd.MarkFlagsMutuallyExclusive("input", "input-file")
}

// runUnifiedSchedule resolves a family before any mutation and never retries an uncertain save.
func runUnifiedSchedule(cmd *cobra.Command, args []string, action string) error {
	name, err := unifiedPromotionName(args, ConfigFile)
	// Ambiguous local declarations cannot select an arbitrary app.
	if err != nil {
		return err
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	appID, err := client.ResolveUnifiedAppScheduleTarget(name)
	if err != nil {
		return err
	}
	// Creation allocates a single stable schedule ID before crossing the network.
	if action == "create" {
		return createUnifiedSchedule(cmd, client, appID)
	}
	items, err := client.ListUnifiedAppSchedules(appID)
	if err != nil {
		return err
	}
	if action == "list" {
		return printSchedules(cmd, items)
	}
	id, _ := cmd.Flags().GetString("id")
	// The selected ID must exist in this authorized family before its revision can be used.
	for _, item := range items {
		if item.ID == id {
			return mutateUnifiedSchedule(cmd, client, appID, action, item)
		}
	}
	return errors.New("schedule was not found in this Unified App")
}

// createUnifiedSchedule retains a single identity even when a network response leaves persistence uncertain.
func createUnifiedSchedule(cmd *cobra.Command, client *api.Client, appID string) error {
	item := api.UnifiedAppScheduleInput{ID: uuid.NewString(), Timezone: "UTC", Enabled: true, Input: json.RawMessage(`{}`)}
	if err := applyScheduleFlags(cmd, &item); err != nil {
		return err
	}
	if err := client.SaveUnifiedAppSchedule(appID, item); err != nil {
		return fmt.Errorf("schedule %s was not confirmed; list schedules before creating another: %w", item.ID, err)
	}
	return scheduleMutationOutput(cmd, "create", item.ID)
}

// mutateUnifiedSchedule submits one mutation against the freshly reviewed revision.
func mutateUnifiedSchedule(cmd *cobra.Command, client *api.Client, appID, action string, item api.UnifiedAppSchedule) error {
	// Deletion retains prior execution receipts and does not cancel an in-flight run.
	if action == "delete" {
		if err := client.DeleteUnifiedAppSchedule(appID, item.ID, item.Revision); err != nil {
			return err
		}
		return scheduleMutationOutput(cmd, action, item.ID)
	}
	// Partial edits preserve all fields not selected by explicit flags.
	if action == "update" {
		if err := applyScheduleFlags(cmd, &item.UnifiedAppScheduleInput); err != nil {
			return err
		}
	} else {
		// Pause and resume are explicit state changes, never inferred from listing or inspection.
		item.Enabled = action == "resume"
	}
	if err := client.SaveUnifiedAppSchedule(appID, item.UnifiedAppScheduleInput); err != nil {
		return err
	}
	return scheduleMutationOutput(cmd, action, item.ID)
}

// applyScheduleFlags updates only explicit fields and requires a deliberate timing mode on creation.
func applyScheduleFlags(cmd *cobra.Command, item *api.UnifiedAppScheduleInput) error {
	// Text fields remain untouched on partial edits unless their flags are present.
	for flag, destination := range map[string]*string{"name": &item.Name, "timezone": &item.Timezone} {
		if cmd.Flags().Changed(flag) {
			*destination, _ = cmd.Flags().GetString(flag)
		}
	}
	// Timing conversion stays independent of private input handling and partial metadata edits.
	if err := applyScheduleTimingFlags(cmd, item); err != nil {
		return err
	}
	// Pausing is explicit, so editing an input never resumes a paused schedule accidentally.
	if cmd.Flags().Changed("paused") {
		paused, _ := cmd.Flags().GetBool("paused")
		item.Enabled = !paused
	}
	if cmd.Flags().Changed("input") || cmd.Flags().Changed("input-file") {
		input, err := readScheduleInput(cmd)
		if err != nil {
			return err
		}
		item.Input = input
	}
	// Server validation remains authoritative for calendars, schema, and past dates.
	if !scheduleHasRequiredFields(*item) {
		return errors.New("a schedule requires --name and either --at or --cron")
	}
	return nil
}

// scheduleHasRequiredFields requires a named timing mode before contacting Engine.
func scheduleHasRequiredFields(item api.UnifiedAppScheduleInput) bool {
	return strings.TrimSpace(item.Name) != "" && (item.RunAt != nil || item.Cron != "")
}

// applyScheduleTimingFlags clears the alternate timing mode only when explicitly selected.
func applyScheduleTimingFlags(cmd *cobra.Command, item *api.UnifiedAppScheduleInput) error {
	// Choosing a new timing mode clears the mutually exclusive saved mode.
	if cmd.Flags().Changed("at") {
		value, _ := cmd.Flags().GetString("at")
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return errors.New("--at requires an RFC3339 timestamp with an explicit offset")
		}
		value = parsed.Format(time.RFC3339)
		item.RunAt = &value
		item.Cron = ""
	}
	if cmd.Flags().Changed("cron") {
		item.Cron, _ = cmd.Flags().GetString("cron")
		item.RunAt = nil
	}
	return nil
}

// readScheduleInput bounds file/stdin reads and never includes private input in error output.
func readScheduleInput(cmd *cobra.Command) (json.RawMessage, error) {
	value, _ := cmd.Flags().GetString("input")
	var reader io.Reader = strings.NewReader(value)
	// File input is preferred for private payloads and shares the same byte cap as inline JSON.
	if cmd.Flags().Changed("input-file") {
		path, _ := cmd.Flags().GetString("input-file")
		reader = cmd.InOrStdin()
		if path != "-" {
			file, err := os.Open(path)
			if err != nil {
				return nil, errors.New("could not open schedule input file")
			}
			defer file.Close()
			reader = file
		}
	}
	data, err := io.ReadAll(io.LimitReader(reader, (512<<10)+1))
	// Malformed or excessive JSON must be rejected before any credentialed mutation.
	if err != nil || len(data) > 512<<10 || !json.Valid(data) {
		return nil, errors.New("input must be valid JSON no larger than 512 KiB")
	}
	return json.RawMessage(data), nil
}

// printSchedules keeps plaintext input out of default terminal output while supporting explicit JSON export.
func printSchedules(cmd *cobra.Command, items []api.UnifiedAppSchedule) error {
	if wantsJSON(cmd) {
		return writeJSON(cmd, map[string]any{"schedules": items})
	}
	for _, item := range items {
		next := "completed"
		// Paused status takes precedence over the retained future timestamp.
		if !item.Enabled {
			next = "paused"
		} else if item.NextRunAt != nil {
			next = *item.NextRunAt
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", item.ID, item.Name, next, item.LastStatus); err != nil {
			return err
		}
	}
	return nil
}

// scheduleMutationOutput records only a bounded operation and opaque schedule identity, never saved input.
func scheduleMutationOutput(cmd *cobra.Command, action, id string) error {
	recordAppliedChange(cmd.Context(), "unified.schedule."+action, "unified_app")
	if wantsJSON(cmd) {
		return writeJSON(cmd, map[string]string{"id": id, "action": action, "status": "succeeded"})
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Schedule %s: %s succeeded.\n", id, action)
	return err
}

// init attaches scheduling to the existing hosted-app command group.
func init() { unifiedCmd.AddCommand(newUnifiedScheduleCommand()) }
