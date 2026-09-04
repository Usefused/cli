package cmd

import (
	"context"
	"errors"

	"github.com/charmbracelet/huh"
)

var errCredentialStorageDeclined = errors.New("credential storage cancelled; plan was not retried")

type credentialMutationOptions struct {
	confirmation string
	auditCtx     context.Context
	auditAction  string
	resourceKind string
}

var promptCredentialMutationConfirmation = confirmCredentialMutation

// authorizeCredentialMutation keeps confirmation semantics shared by every
// compound workflow while explicit `secret set` remains confirmation-free
// because naming that command is already the user's intent.
func authorizeCredentialMutation(opts credentialMutationOptions) error {
	if opts.confirmation == "" {
		return nil
	}
	confirmed, err := promptCredentialMutationConfirmation(opts.confirmation)
	if err != nil {
		return err
	}
	// A typed sentinel lets successful non-blocking plans preserve their receipt
	// while legacy blocking plans still surface the cancelled remediation.
	if !confirmed {
		return errCredentialStorageDeclined
	}
	return nil
}

// confirmCredentialMutation makes optional setup explicit before collection and
// defaults to preserving the valid plan without changing bucket credentials.
func confirmCredentialMutation(message string) (bool, error) {
	confirmed := false
	err := huh.NewConfirm().Title(message).
		Description("You can create the app now and configure credentials before connecting accounts or calling secured operations.").
		Affirmative("Set up now").Negative("Skip for now").Value(&confirmed).Run()
	return confirmed, err
}

func recordCredentialMutation(opts credentialMutationOptions) {
	recordAppliedChange(opts.auditCtx, opts.auditAction, opts.resourceKind)
}
