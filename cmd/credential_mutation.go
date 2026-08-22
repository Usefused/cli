package cmd

import (
	"context"
	"errors"

	"github.com/charmbracelet/huh"
)

type credentialMutationOptions struct {
	confirmation string
	auditCtx     context.Context
	auditAction  string
	resourceKind string
}

var promptCredentialMutationConfirmation = confirmCredentialMutation

// authorizeCredentialMutation keeps confirmation semantics shared by every
// compound workflow while explicit `secret set`/`connect set` commands remain
// confirmation-free because naming that command is already the user's intent.
func authorizeCredentialMutation(opts credentialMutationOptions) error {
	if opts.confirmation == "" {
		return nil
	}
	confirmed, err := promptCredentialMutationConfirmation(opts.confirmation)
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("credential storage cancelled; SDK plan was not retried")
	}
	return nil
}

func confirmCredentialMutation(message string) (bool, error) {
	confirmed := true
	err := huh.NewConfirm().Title(message).Affirmative("Store").Negative("Cancel").Value(&confirmed).Run()
	return confirmed, err
}

func recordCredentialMutation(opts credentialMutationOptions) {
	recordAppliedChange(opts.auditCtx, opts.auditAction, opts.resourceKind)
}
