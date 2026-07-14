package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/bootstrap"
	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
)

func selfcheckCmd() *cobra.Command {
	var baseRef string
	var ifChanged bool

	cmd := &cobra.Command{
		Use:   "selfcheck",
		Short: "Ensure ot binary matches repo revision",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()

			needsUpgrade, err := otBinaryNeedsUpgrade(ctx, rt.RepoRoot, Version, baseRef)
			if err != nil {
				return err
			}
			if !needsUpgrade {
				if ifChanged {
					fmt.Println("[ot] CLI sources unchanged; skipping ot rebuild.")
				}
				return nil
			}

			fmt.Println("[ot] CLI is out of date; running ot upgrade...")
			return execUpgrade(rt)
		},
	}
	cmd.Flags().StringVar(&baseRef, "base", "", "Git revision to compare against HEAD for changed CLI sources")
	cmd.Flags().BoolVar(&ifChanged, "if-changed", false, "Only rebuild when CLI source paths changed")
	return cmd
}

func execUpgrade(rt *Runtime) error {
	if bootstrap.IsCloudAgent() {
		return buildAndInstallOtAt(context.Background(), rt.RepoRoot)
	}
	upgradeBin := filepath.Join(rt.RepoRoot, ".ot", "bin", "ot")
	if err := ensureSelfBinary(context.Background(), rt.RepoRoot); err != nil {
		return err
	}
	return run.RunInteractive(context.Background(), run.Spec{
		Program: upgradeBin,
		Args:    []string{"upgrade", "ot"},
		Dir:     rt.RepoRoot,
		Env: map[string]string{
			"OT_SKIP_SELF_CHECK": "1",
		},
	})
}

var otSelfCheckPathspecs = []string{"ot", "go.mod", "go.sum"}

func otBinaryNeedsUpgrade(ctx context.Context, repoRoot string, binVersion string, baseRef string) (bool, error) {
	repoSha, err := repoHeadSHA(ctx, repoRoot)
	if err != nil {
		return false, err
	}

	binSha := strings.TrimSpace(binVersion)
	if binSha != "" && binSha != "dev" && binSha == repoSha {
		return false, nil
	}

	base := strings.TrimSpace(baseRef)
	if base == "" && binSha != "" && binSha != "dev" {
		base = binSha
	}
	if base == "" {
		return true, nil
	}

	changed, err := otSourceChangedBetween(ctx, repoRoot, base, repoSha)
	if err != nil {
		// If the installed binary points at a pruned, rebased, or otherwise
		// missing commit, rebuild once instead of leaving the local CLI stale.
		return true, nil
	}
	return changed, nil
}

func otSourceChangedBetween(ctx context.Context, repoRoot string, baseRef string, headRef string) (bool, error) {
	base := strings.TrimSpace(baseRef)
	head := strings.TrimSpace(headRef)
	if base == "" || head == "" {
		return true, nil
	}
	args := []string{"-C", repoRoot, "diff", "--name-only", base, head, "--"}
	args = append(args, otSelfCheckPathspecs...)
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "git",
		Args:    args,
	})
	if err != nil {
		return false, fmt.Errorf("git diff CLI sources: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}
