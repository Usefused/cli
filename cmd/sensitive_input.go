package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

const maxSensitiveInputBytes = 1 << 20

func readSensitiveValue(cmd *cobra.Command, label string) (string, error) {
	// Bounded stdin keeps credentials out of argv without allowing an
	// accidentally redirected large file to exhaust CLI memory.
	data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxSensitiveInputBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s from stdin: %w", label, err)
	}
	if len(data) > maxSensitiveInputBytes {
		return "", fmt.Errorf("%s input exceeds %d bytes", label, maxSensitiveInputBytes)
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if value == "" {
		return "", fmt.Errorf("%s input from stdin is empty", label)
	}
	return value, nil
}
