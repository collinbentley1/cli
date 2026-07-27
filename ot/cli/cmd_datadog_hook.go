package cli

import (
	"context"
	"strings"

	"github.com/collinbentley1/cli/ot/datadog"
	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
)

func datadogHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "datadog-hook",
		Short: "Run Datadog static analyzer hook",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			defaultBranch := defaultBranch(cmd.Context(), rt.RepoRoot)

			return datadog.RunHook(cmd.Context(), datadog.Options{
				RepoRoot:      rt.RepoRoot,
				DefaultBranch: defaultBranch,
			})
		},
	}
}

func defaultBranch(ctx context.Context, repoRoot string) string {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "git",
		Args:    []string{"-C", repoRoot, "symbolic-ref", "refs/remotes/origin/HEAD"},
	})
	if err != nil {
		return "main"
	}
	ref := strings.TrimSpace(out)
	ref = strings.TrimPrefix(ref, "refs/remotes/origin/")
	if ref == "" {
		return "main"
	}
	return ref
}
