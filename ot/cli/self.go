package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/collinbentley1/cli/ot/run"
)

func ensureSelfBinary(ctx context.Context, repoRoot string) error {
	binDir := filepath.Join(repoRoot, ".ot", "bin")
	binPath := filepath.Join(binDir, "ot")

	sha, err := repoHeadSHA(ctx, repoRoot)
	if err != nil {
		return err
	}

	if info, err := os.Stat(binPath); err == nil && !info.IsDir() && cachedSelfBinaryMatches(ctx, repoRoot, binPath, sha) {
		return nil
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", binDir, err)
	}

	if err := buildOtBinary(ctx, repoRoot, binPath, sha); err != nil {
		return fmt.Errorf("build self binary: %w", err)
	}
	return nil
}

func cachedSelfBinaryMatches(ctx context.Context, repoRoot string, binPath string, sha string) bool {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: binPath,
		Args:    []string{"version"},
		Dir:     repoRoot,
		Env: map[string]string{
			"OT_SKIP_SELF_CHECK": "1",
		},
	})
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(out), "ot version "+sha+" ")
}
