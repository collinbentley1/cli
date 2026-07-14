package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/collinbentley1/cli/ot/docker"
	"github.com/collinbentley1/cli/ot/overmind"
	"github.com/spf13/cobra"
)

func resetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Stop all stacks, remove local containers, and wipe .ot state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			// Stop the tunnel (and restore MagicDNS from the state file)
			// before .ot — which holds that state file — is wiped below.
			_ = clearBackendMCPTunnelState(rt.RepoRoot)
			stopBackendMCPTunnel(cmd.Context())

			for _, name := range []string{"dev", "frontend", "backend", "db", "crm"} {
				st, err := overmind.Stack(rt.Config, name)
				if err != nil {
					// Configs may define a subset of the stacks; reset
					// should still clean up everything else.
					continue
				}
				_ = overmind.Quit(cmd.Context(), rt.RepoRoot, st)
			}

			containers := dockerContainersForRuntime(rt)
			_ = docker.StopContainers(cmd.Context(), rt.RepoRoot, containers)
			_ = docker.RemoveContainers(cmd.Context(), rt.RepoRoot, containers)

			otDir := filepath.Join(rt.RepoRoot, ".ot")
			if err := os.RemoveAll(otDir); err != nil {
				return fmt.Errorf("remove %s: %w", otDir, err)
			}
			return nil
		},
	}
	return cmd
}
