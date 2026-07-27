package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/collinbentley1/cli/ot/run"
)

func StopContainers(ctx context.Context, repoRoot string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	existing, err := listContainers(ctx)
	if err != nil {
		return err
	}
	for _, n := range names {
		if !existing[n] {
			continue
		}
		_ = run.RunInteractive(ctx, run.Spec{
			Program: "docker",
			Args:    []string{"stop", n},
			Dir:     repoRoot,
		})
	}
	return nil
}

func RemoveContainers(ctx context.Context, repoRoot string, names []string) error {
	existing, err := listContainers(ctx)
	if err != nil {
		return err
	}
	for _, n := range names {
		if !existing[n] {
			continue
		}
		_ = run.RunInteractive(ctx, run.Spec{
			Program: "docker",
			Args:    []string{"rm", "-f", n},
			Dir:     repoRoot,
		})
	}
	return nil
}

func EnsureDockerVersion(ctx context.Context) error {
	_, err := run.RunCapture(ctx, run.Spec{Program: "docker", Args: []string{"--version"}})
	if err != nil {
		return fmt.Errorf("docker not available: %w", err)
	}
	return nil
}

func listContainers(ctx context.Context) (map[string]bool, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "docker",
		Args:    []string{"ps", "-a", "--format", "{{.Names}}"},
	})
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool)
	for line := range strings.SplitSeq(out, "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			existing[name] = true
		}
	}
	return existing, nil
}
