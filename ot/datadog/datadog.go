package datadog

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/collinbentley1/cli/ot/run"
)

type Options struct {
	RepoRoot      string
	DefaultBranch string
}

func RunHook(ctx context.Context, opts Options) error {
	if err := validateEnv(); err != nil {
		return err
	}

	if _, err := exec.LookPath("datadog-static-analyzer-git-hook"); err != nil {
		return fmt.Errorf("datadog-static-analyzer-git-hook not found")
	}

	defaultBranch := opts.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = "main"
	}

	args := []string{
		"-r", opts.RepoRoot,
		"--enable-static-analysis", "true",
		"--enable-secrets", "true",
		"--default-branch", defaultBranch,
	}

	if err := run.RunInteractive(ctx, run.Spec{
		Program: "datadog-static-analyzer-git-hook",
		Args:    args,
		Dir:     opts.RepoRoot,
	}); err != nil {
		return fmt.Errorf("datadog-static-analyzer-git-hook failed: %w", err)
	}

	return nil
}

func validateEnv() error {
	required := []string{"DD_API_KEY", "DD_APP_KEY"}
	var missing []string
	for _, k := range required {
		if strings.TrimSpace(os.Getenv(k)) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}
