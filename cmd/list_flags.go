package cmd

import (
	"fmt"
	"io"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

type listFlags struct {
	Limit  int
	Offset int
}

func addListFlags(cmd *cobra.Command, flags *listFlags) {
	cmd.Flags().IntVar(&flags.Limit, "limit", 20, "Maximum rows to read")
	cmd.Flags().IntVar(&flags.Offset, "offset", 0, "Rows to skip before reading")
}

func (f listFlags) pageOptions() cliapi.PageOptions {
	return cliapi.PageOptions{Limit: f.Limit, Offset: f.Offset}
}

func printPageSummary(out io.Writer, total int, flags listFlags) {
	next := flags.Offset + normalCLIListLimit(flags.Limit)
	if next < total {
		fmt.Fprintf(out, "Showing %d-%d of %d. Use --offset %d for the next page.\n", flags.Offset+1, next, total, next)
		return
	}
	fmt.Fprintf(out, "Showing %d row(s).\n", total)
}

func normalCLIListLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}
