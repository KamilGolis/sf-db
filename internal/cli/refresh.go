package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Refresh an existing clone (not implemented yet)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("refresh is not implemented yet; re-run clone into a new file for now")
		},
	}
}
