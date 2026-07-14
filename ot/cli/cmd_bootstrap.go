package cli

import (
	"github.com/collinbentley1/cli/ot/bootstrap"
	"github.com/spf13/cobra"
)

func bootstrapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Install and configure ot dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}
			return bootstrap.Run(cmd.Context(), bootstrap.Options{RepoRoot: rt.RepoRoot, Quiet: false})
		},
	}

	return cmd
}
