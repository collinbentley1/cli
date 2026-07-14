package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/collinbentley1/cli/ot/run"
)

type Mode string

const (
	ModeFull    Mode = "full"
	ModeRuntime Mode = "runtime"
)

func runRuntimePreflight(ctx context.Context, opts Options) error {
	if IsCloudAgent() {
		return runAgentRuntimePreflight(ctx, opts)
	}

	if runtime.GOOS != "darwin" {
		return fmt.Errorf("ot is macOS-only (GOOS=%s)", runtime.GOOS)
	}

	target := normalizeRuntimeTarget(opts.Target)
	switch target {
	case "all":
		if err := ensureRuntimeFrontend(ctx, opts.RepoRoot, opts.Quiet); err != nil {
			return err
		}
		if err := ensureRuntimeBackend(ctx, opts.RepoRoot, opts.Quiet); err != nil {
			return err
		}
	case "frontend":
		if err := ensureRuntimeFrontend(ctx, opts.RepoRoot, opts.Quiet); err != nil {
			return err
		}
	case "backend":
		if err := ensureRuntimeBackend(ctx, opts.RepoRoot, opts.Quiet); err != nil {
			return err
		}
	case "auth":
		if err := ensureRuntimeBackend(ctx, opts.RepoRoot, opts.Quiet); err != nil {
			return err
		}
	case "db":
		if err := ensureRuntimeDB(ctx); err != nil {
			return err
		}
	case "wordpress":
		if err := ensureRuntimeWordpress(ctx, opts.RepoRoot); err != nil {
			return err
		}
	}

	return ensureBootstrapStateDirs(opts.RepoRoot)
}

func normalizeRuntimeTarget(target string) string {
	value := strings.TrimSpace(target)
	switch value {
	case "":
		return "all"
	case "backend-worker":
		return "backend"
	case "frontend-router":
		return "frontend"
	default:
		return value
	}
}

func ensureRuntimeFrontend(ctx context.Context, repoRoot string, quiet bool) error {
	if err := ensureRuntimeTool(ctx, brewPackageSpec{
		Name:     "tmux",
		Display:  "tmux",
		Kind:     brewPackageFormula,
		Binaries: []string{"tmux"},
	}); err != nil {
		return err
	}
	if err := ensureRuntimeTool(ctx, brewPackageSpec{
		Name:     "overmind",
		Display:  "overmind",
		Kind:     brewPackageFormula,
		Binaries: []string{"overmind"},
	}); err != nil {
		return err
	}
	if err := ensureRuntimeNode(ctx); err != nil {
		return err
	}
	return installProjectDependencies(ctx, repoRoot, quiet, dependencyScope{frontend: true})
}

func ensureRuntimeBackend(ctx context.Context, repoRoot string, quiet bool) error {
	if err := ensureRuntimeTool(ctx, brewPackageSpec{
		Name:     "uv",
		Display:  "uv",
		Kind:     brewPackageFormula,
		Binaries: []string{"uv"},
	}); err != nil {
		return err
	}
	if err := ensureRuntimeDocker(ctx, repoRoot); err != nil {
		return err
	}
	return installProjectDependencies(ctx, repoRoot, quiet, dependencyScope{backend: true})
}

func ensureRuntimeDB(ctx context.Context) error {
	if err := ensureRuntimeTool(ctx, brewPackageSpec{
		Name:     "tmux",
		Display:  "tmux",
		Kind:     brewPackageFormula,
		Binaries: []string{"tmux"},
	}); err != nil {
		return err
	}
	if err := ensureRuntimeTool(ctx, brewPackageSpec{
		Name:     "overmind",
		Display:  "overmind",
		Kind:     brewPackageFormula,
		Binaries: []string{"overmind"},
	}); err != nil {
		return err
	}
	return ensureRuntimePorter(ctx)
}

func ensureRuntimeWordpress(ctx context.Context, repoRoot string) error {
	if err := ensureRuntimeNode(ctx); err != nil {
		return err
	}
	return ensureRuntimeDocker(ctx, repoRoot)
}

func ensureRuntimeTool(ctx context.Context, spec brewPackageSpec) error {
	if len(spec.Binaries) > 0 && allBinariesAvailable(spec.Binaries) {
		return nil
	}

	brewPath, err := findBrew()
	if err != nil {
		return err
	}

	installedByBrew, err := brewPackageInstalled(ctx, brewPath, spec)
	if err != nil {
		return err
	}
	if !installedByBrew {
		return installBrewPackage(ctx, brewPath, spec)
	}
	for _, bin := range spec.Binaries {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("required tool '%s' is not available after ensuring %s", bin, spec.Display)
		}
	}
	return nil
}

func ensureRuntimeNode(ctx context.Context) error {
	const nodeVersion = "22.22.0"

	if version, err := runNodeVersion(ctx); err == nil && version == "v"+nodeVersion {
		return nil
	}
	if activateInstalledNVMNode(nodeVersion) {
		if version, err := runNodeVersion(ctx); err == nil && version == "v"+nodeVersion {
			return nil
		}
	}
	fmt.Printf("[bootstrap] Activating node v%s for runtime...\n", nodeVersion)
	return ensureNvmNode(ctx, false)
}

func runNodeVersion(ctx context.Context) (string, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "node",
		Args:    []string{"-v"},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func ensureRuntimeDocker(ctx context.Context, repoRoot string) error {
	if _, err := exec.LookPath("docker"); err != nil || !dockerDesktopInstalled() {
		brewPath, brewErr := findBrew()
		if brewErr != nil {
			return brewErr
		}
		fmt.Println("[bootstrap] Repairing Docker runtime dependencies...")
		return ensureDocker(ctx, brewPath, repoRoot)
	}
	return ensureDockerDaemonRunning(ctx)
}

func ensureRuntimePorter(ctx context.Context) error {
	if _, err := exec.LookPath("porter"); err == nil && supportsPorterDatastore(ctx) {
		return nil
	}

	brewPath, err := findBrew()
	if err != nil {
		return err
	}
	fmt.Println("[bootstrap] Repairing Porter runtime dependencies...")
	return ensurePorter(ctx, brewPath)
}

func activateInstalledNVMNode(version string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	nodeBin := filepath.Join(home, ".nvm", "versions", "node", "v"+version, "bin")
	nodePath := filepath.Join(nodeBin, "node")
	if _, err := os.Stat(nodePath); err != nil {
		return false
	}
	prependPathEntry(nodeBin)
	_ = os.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	_ = os.Setenv("NVM_BIN", nodeBin)
	return true
}
