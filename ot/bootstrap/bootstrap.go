package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/hooks"
	"github.com/collinbentley1/cli/ot/run"
)

type Options struct {
	RepoRoot string
	Quiet    bool
	Mode     Mode
	Target   string
}

type brewPackageKind string

const (
	brewPackageFormula brewPackageKind = "formula"
	brewPackageCask    brewPackageKind = "cask"
)

type brewPackageSpec struct {
	Name       string
	Display    string
	Kind       brewPackageKind
	Tap        string
	Binaries   []string
	CaskroomID string
}

func Run(ctx context.Context, opts Options) error {
	if opts.Mode == ModeRuntime {
		return runRuntimePreflight(ctx, opts)
	}

	if IsCloudAgent() {
		return runAgentBootstrap(ctx, opts)
	}

	if runtime.GOOS != "darwin" {
		return fmt.Errorf("ot is macOS-only (GOOS=%s)", runtime.GOOS)
	}

	brewPath, err := findBrew()
	if err != nil {
		return err
	}

	if err := ensureTool(ctx, brewPath, "tmux", []string{"tmux"}); err != nil {
		return err
	}
	if err := ensureTool(ctx, brewPath, "overmind", []string{"overmind"}); err != nil {
		return err
	}
	if err := ensureTool(ctx, brewPath, "ripgrep", []string{"rg"}); err != nil {
		return err
	}
	if err := ensureTool(ctx, brewPath, "uv", []string{"uv"}); err != nil {
		return err
	}
	if err := ensureNvmNode(ctx, opts.Quiet); err != nil {
		return err
	}
	if err := ensureSfw(ctx); err != nil {
		return err
	}
	if err := ensureTool(ctx, brewPath, "go", []string{"go"}); err != nil {
		return err
	}
	if err := ensureTool(ctx, brewPath, "golangci-lint", []string{"golangci-lint"}); err != nil {
		return err
	}
	if err := ensureTool(ctx, brewPath, "pre-commit", []string{"pre-commit"}); err != nil {
		return err
	}
	if err := ensureTool(ctx, brewPath, "gh", []string{"gh"}); err != nil {
		return err
	}
	if err := ensureLibPQ(ctx, brewPath); err != nil {
		return err
	}
	if err := ensureDatadogStaticAnalyzerGitHook(ctx, brewPath, opts.RepoRoot); err != nil {
		return err
	}

	if err := ensureDocker(ctx, brewPath, opts.RepoRoot); err != nil {
		return err
	}
	if err := ensurePorter(ctx, brewPath); err != nil {
		return err
	}
	if err := ensureInfisicalCLI(ctx, brewPath); err != nil {
		return err
	}
	ctx = withBootstrapMode(ctx)
	if err := installProjectDependencies(ctx, opts.RepoRoot, opts.Quiet, dependencyScope{
		backend:  true,
		crm:      true,
		frontend: true,
	}); err != nil {
		return err
	}
	if err := installGitHooks(ctx, opts.RepoRoot); err != nil {
		return err
	}
	if err := ensureAgentCLIs(ctx, brewPath); err != nil {
		return err
	}
	if err := ensureEntire(ctx, brewPath, opts.RepoRoot); err != nil {
		return err
	}

	return ensureBootstrapStateDirs(opts.RepoRoot)
}

func findBrew() (string, error) {
	if p, err := exec.LookPath("brew"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("homebrew (brew) not found; install Homebrew first")
}

func ensureTool(ctx context.Context, brewPath, brewFormula string, binaries []string) error {
	return ensureBrewPackage(ctx, brewPath, brewPackageSpec{
		Name:     brewFormula,
		Display:  brewFormula,
		Kind:     brewPackageFormula,
		Binaries: binaries,
	})
}

func ensureBrewPackage(ctx context.Context, brewPath string, spec brewPackageSpec) error {
	installedByBrew, err := brewPackageInstalled(ctx, brewPath, spec)
	if err != nil {
		return err
	}

	if !allBinariesAvailable(spec.Binaries) && !installedByBrew {
		if err := installBrewPackage(ctx, brewPath, spec); err != nil {
			return err
		}
		installedByBrew = true
	}

	if installedByBrew {
		needsUpgrade, err := brewPackageNeedsUpgrade(ctx, brewPath, spec)
		if err != nil {
			return err
		}
		if needsUpgrade {
			if err := upgradeBrewPackage(ctx, brewPath, spec); err != nil {
				return err
			}
		}
	}

	for _, bin := range spec.Binaries {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("required tool '%s' is not available after ensuring %s", bin, spec.Display)
		}
	}

	return nil
}

func allBinariesAvailable(binaries []string) bool {
	for _, bin := range binaries {
		if _, err := exec.LookPath(bin); err != nil {
			return false
		}
	}
	return true
}

func brewPackageInstalled(ctx context.Context, brewPath string, spec brewPackageSpec) (bool, error) {
	switch spec.Kind {
	case brewPackageFormula:
		out, err := run.RunCapture(ctx, run.Spec{
			Program: brewPath,
			Args:    []string{"list", "--versions", spec.Name},
		})
		if err != nil {
			return false, nil
		}
		if strings.TrimSpace(out) != "" {
			return true, nil
		}
		return false, nil
	case brewPackageCask:
		prefix, err := brewPrefix(ctx, brewPath)
		if err != nil {
			return false, err
		}
		caskroomID := spec.CaskroomID
		if caskroomID == "" {
			caskroomID = spec.Name
		}
		_, err = os.Stat(filepath.Join(prefix, "Caskroom", caskroomID))
		if err == nil {
			return true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat Homebrew cask %s: %w", spec.Display, err)
	default:
		return false, fmt.Errorf("unsupported brew package kind %q", spec.Kind)
	}
}

func brewPackageNeedsUpgrade(ctx context.Context, brewPath string, spec brewPackageSpec) (bool, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: brewPath,
		Args:    brewUpgradeArgs(spec, true),
	})
	if err != nil {
		return false, fmt.Errorf("brew upgrade --dry-run %s: %w", spec.Name, err)
	}
	return brewDryRunIndicatesUpgrade(out), nil
}

func brewDryRunIndicatesUpgrade(output string) bool {
	return strings.Contains(output, "Would upgrade")
}

func installBrewPackage(ctx context.Context, brewPath string, spec brewPackageSpec) error {
	if spec.Tap != "" {
		if err := run.RunInteractive(ctx, run.Spec{
			Program: brewPath,
			Args:    []string{"tap", spec.Tap},
		}); err != nil {
			return fmt.Errorf("brew tap %s failed: %w", spec.Tap, err)
		}
	}

	fmt.Printf("[bootstrap] Installing %s...\n", spec.Display)
	if err := run.RunInteractive(ctx, run.Spec{
		Program: brewPath,
		Args:    brewInstallArgs(spec),
	}); err != nil {
		return fmt.Errorf("brew install %s failed: %w", spec.Name, err)
	}
	return nil
}

func upgradeBrewPackage(ctx context.Context, brewPath string, spec brewPackageSpec) error {
	fmt.Printf("[bootstrap] Upgrading %s...\n", spec.Display)
	if err := run.RunInteractive(ctx, run.Spec{
		Program: brewPath,
		Args:    brewUpgradeArgs(spec, false),
	}); err != nil {
		return fmt.Errorf("brew upgrade %s failed: %w", spec.Name, err)
	}
	return nil
}

func brewInstallArgs(spec brewPackageSpec) []string {
	switch spec.Kind {
	case brewPackageFormula:
		return []string{"install", spec.Name}
	case brewPackageCask:
		return []string{"install", "--cask", spec.Name}
	default:
		return []string{"install", spec.Name}
	}
}

func brewUpgradeArgs(spec brewPackageSpec, dryRun bool) []string {
	args := []string{"upgrade"}
	if spec.Kind == brewPackageFormula {
		args = append(args, "--formula")
	}
	if spec.Kind == brewPackageCask {
		args = append(args, "--cask")
	}
	args = append(args, spec.Name)
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

func brewPrefix(ctx context.Context, brewPath string) (string, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: brewPath,
		Args:    []string{"--prefix"},
	})
	if err != nil {
		return "", fmt.Errorf("brew --prefix: %w", err)
	}
	prefix := strings.TrimSpace(out)
	if prefix == "" {
		return "", errors.New("brew prefix is empty")
	}
	return prefix, nil
}

func withBootstrapMode(ctx context.Context) context.Context {
	if env := os.Getenv("OT_BOOTSTRAP_MODE"); env == "1" {
		return ctx
	}
	_ = os.Setenv("OT_BOOTSTRAP_MODE", "1")
	return ctx
}

func ensureSfw(ctx context.Context) error {
	installed, err := sfwInstalledVersion(ctx)
	if err != nil {
		return err
	}
	if installed == "" {
		fmt.Println("[bootstrap] Installing sfw...")
		if err := run.RunInteractive(ctx, run.Spec{
			Program: "npm",
			Args:    []string{"install", "-g", "sfw"},
		}); err != nil {
			return fmt.Errorf("npm install -g sfw failed: %w", err)
		}
		return nil
	}

	latest, err := sfwLatestVersion(ctx)
	if err != nil {
		return err
	}
	if latest == "" || installed == latest {
		return nil
	}

	releaseTime, err := sfwReleaseTime(ctx, latest)
	if err != nil {
		return err
	}
	if releaseTime.After(time.Now().Add(-24 * time.Hour)) {
		return nil
	}

	fmt.Printf("[bootstrap] Updating sfw to %s...\n", latest)
	if err := run.RunInteractive(ctx, run.Spec{
		Program: "npm",
		Args:    []string{"install", "-g", "sfw@" + latest},
	}); err != nil {
		return fmt.Errorf("npm install -g sfw@%s failed: %w", latest, err)
	}
	return nil
}

func sfwInstalledVersion(ctx context.Context) (string, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "npm",
		Args:    []string{"list", "-g", "sfw", "--depth=0", "--json"},
	})
	if err != nil && strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("npm list -g sfw: %w", err)
	}
	type depInfo struct {
		Version string `json:"version"`
	}
	type listInfo struct {
		Dependencies map[string]depInfo `json:"dependencies"`
	}
	var info listInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", fmt.Errorf("parse npm list -g sfw: %w", err)
	}
	if dep, ok := info.Dependencies["sfw"]; ok {
		return strings.TrimSpace(dep.Version), nil
	}
	return "", nil
}

func sfwLatestVersion(ctx context.Context) (string, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "npm",
		Args:    []string{"view", "sfw", "version"},
	})
	if err != nil {
		return "", fmt.Errorf("npm view sfw version: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func sfwReleaseTime(ctx context.Context, version string) (time.Time, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "npm",
		Args:    []string{"view", "sfw", "time", "--json"},
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("npm view sfw time: %w", err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &data); err != nil {
		return time.Time{}, fmt.Errorf("parse npm view sfw time: %w", err)
	}
	val, ok := data[version]
	if !ok {
		return time.Time{}, fmt.Errorf("sfw time missing version %s", version)
	}
	str, ok := val.(string)
	if !ok || strings.TrimSpace(str) == "" {
		return time.Time{}, fmt.Errorf("sfw time invalid for %s", version)
	}
	ts, err := time.Parse(time.RFC3339, str)
	if err != nil {
		ts, err = time.Parse(time.RFC3339Nano, str)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse sfw time %s: %w", version, err)
		}
	}
	return ts, nil
}

func ensureNvmNode(ctx context.Context, quiet bool) error {
	const nodeVersion = "22.22.0"
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	nvmDir := filepath.Join(home, ".nvm")
	nvmSh := filepath.Join(nvmDir, "nvm.sh")

	if _, err := os.Stat(nvmSh); err != nil {
		if !quiet {
			fmt.Println("[bootstrap] Installing nvm...")
		}
		if err := runQuietMaybeInteractive(ctx, quiet, run.Spec{
			Program: "sh",
			Args: []string{
				"-c",
				"curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.4/install.sh | bash",
			},
		}, "install nvm"); err != nil {
			return fmt.Errorf("install nvm: %w", err)
		}
	}

	// `nvm install` already selects the version for that shell; an extra `nvm use`
	// only duplicates the "Now using" log line, and we verify the final PATH below.
	cmd := fmt.Sprintf("export NVM_DIR=\"%s\"; [ -s \"%s\" ] && . \"%s\"; nvm install %s",
		nvmDir, nvmSh, nvmSh, nodeVersion,
	)
	if err := runQuietMaybeInteractive(ctx, quiet, run.Spec{
		Program: "zsh",
		Args:    []string{"-lc", cmd},
	}, "install node via nvm"); err != nil {
		return fmt.Errorf("install node %s via nvm: %w", nodeVersion, err)
	}

	nodePathCmd := fmt.Sprintf(
		"export NVM_DIR=\"%s\"; [ -s \"%s\" ] && . \"%s\"; nvm which %s",
		nvmDir,
		nvmSh,
		nvmSh,
		nodeVersion,
	)
	nodePath, err := run.RunCapture(ctx, run.Spec{
		Program: "zsh",
		Args:    []string{"-lc", nodePathCmd},
	})
	if err != nil {
		return fmt.Errorf("resolve node %s via nvm: %w", nodeVersion, err)
	}
	nodePath = lastNonEmptyLine(nodePath)
	if nodePath == "" {
		return fmt.Errorf("resolve node %s via nvm: empty path", nodeVersion)
	}
	nodeBin := filepath.Dir(nodePath)
	if !filepath.IsAbs(nodeBin) {
		return fmt.Errorf("resolve node %s via nvm: invalid path %s", nodeVersion, nodePath)
	}

	prependPathEntry(nodeBin)
	_ = os.Setenv("NVM_DIR", nvmDir)
	_ = os.Setenv("NVM_BIN", nodeBin)

	activeVersion, err := run.RunCapture(ctx, run.Spec{
		Program: "node",
		Args:    []string{"-v"},
	})
	if err != nil {
		return fmt.Errorf("verify node %s on PATH: %w", nodeVersion, err)
	}
	if strings.TrimSpace(activeVersion) != "v"+nodeVersion {
		return fmt.Errorf("expected node v%s on PATH, got %s", nodeVersion, strings.TrimSpace(activeVersion))
	}

	return nil
}

func prependPathEntry(dir string) {
	current := os.Getenv("PATH")
	if current == "" {
		_ = os.Setenv("PATH", dir)
		return
	}

	parts := strings.Split(current, string(os.PathListSeparator))
	filtered := make([]string, 0, len(parts)+1)
	filtered = append(filtered, dir)
	for _, part := range parts {
		if part == "" || part == dir {
			continue
		}
		filtered = append(filtered, part)
	}
	_ = os.Setenv("PATH", strings.Join(filtered, string(os.PathListSeparator)))
}

func lastNonEmptyLine(output string) string {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func runQuietMaybeInteractive(ctx context.Context, quiet bool, spec run.Spec, action string) error {
	if !quiet {
		return run.RunInteractive(ctx, spec)
	}

	out, err := run.RunCapture(ctx, spec)
	if err == nil {
		return nil
	}

	out = strings.TrimSpace(out)
	if out == "" {
		return err
	}
	return fmt.Errorf("%s: %w\n%s", action, err, out)
}

func ensureLibPQ(ctx context.Context, brewPath string) error {
	spec := brewPackageSpec{
		Name:     "libpq",
		Display:  "libpq",
		Kind:     brewPackageFormula,
		Binaries: []string{"psql"},
	}

	installedByBrew, err := brewPackageInstalled(ctx, brewPath, spec)
	if err != nil {
		return err
	}
	if !allBinariesAvailable(spec.Binaries) && !installedByBrew {
		if err := installBrewPackage(ctx, brewPath, spec); err != nil {
			return err
		}
		installedByBrew = true
	}
	if installedByBrew {
		needsUpgrade, err := brewPackageNeedsUpgrade(ctx, brewPath, spec)
		if err != nil {
			return err
		}
		if needsUpgrade {
			if err := upgradeBrewPackage(ctx, brewPath, spec); err != nil {
				return err
			}
		}
	}
	if _, err := exec.LookPath("psql"); err == nil {
		return nil
	}

	_ = run.RunInteractive(ctx, run.Spec{
		Program: brewPath,
		Args:    []string{"link", "--force", "libpq"},
	})

	if _, err := exec.LookPath("psql"); err == nil {
		return nil
	}

	libpqBin, err := resolveLibPQBin(ctx, brewPath)
	if err != nil {
		return err
	}
	if err := ensurePathExport(libpqBin); err != nil {
		return err
	}

	if _, err := exec.LookPath("psql"); err != nil {
		return fmt.Errorf("psql not available after installing libpq; ensure %s is on PATH", libpqBin)
	}
	return nil
}

func resolveLibPQBin(ctx context.Context, brewPath string) (string, error) {
	prefix, err := run.RunCapture(ctx, run.Spec{
		Program: brewPath,
		Args:    []string{"--prefix", "libpq"},
	})
	if err != nil {
		prefix, err = run.RunCapture(ctx, run.Spec{
			Program: brewPath,
			Args:    []string{"--prefix"},
		})
		if err != nil {
			return "", fmt.Errorf("brew --prefix: %w", err)
		}
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "", errors.New("brew prefix is empty")
	}
	return filepath.Join(prefix, "opt", "libpq", "bin"), nil
}

func ensurePathExport(dir string) error {
	if !strings.HasPrefix(dir, "/") {
		return fmt.Errorf("path export expects absolute path, got %s", dir)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	rcPath := filepath.Join(home, ".zshrc")
	line := fmt.Sprintf("export PATH=\"%s:$PATH\"", dir)

	contents, err := os.ReadFile(rcPath)
	if err == nil && strings.Contains(string(contents), line) {
		return nil
	}

	if err := run.RunInteractive(context.Background(), run.Spec{
		Program: "sh",
		Args:    []string{"-c", fmt.Sprintf("printf '%s\\n' '%s' >> %s", line, line, rcPath)},
	}); err != nil {
		return fmt.Errorf("update %s: %w", rcPath, err)
	}

	_ = run.RunInteractive(context.Background(), run.Spec{
		Program: "zsh",
		Args:    []string{"-lc", fmt.Sprintf("source %s", rcPath)},
	})

	fmt.Printf("[bootstrap] Added %s to %s. Restart your shell or run: source %s\n", dir, rcPath, rcPath)
	return nil
}

func ensureDocker(ctx context.Context, brewPath, repoRoot string) error {
	spec := brewPackageSpec{
		Name:       "docker",
		Display:    "Docker Desktop",
		Kind:       brewPackageCask,
		CaskroomID: "docker",
	}

	installedByBrew, err := brewPackageInstalled(ctx, brewPath, spec)
	if err != nil {
		return err
	}
	if installedByBrew {
		needsUpgrade, err := brewPackageNeedsUpgrade(ctx, brewPath, spec)
		if err != nil {
			return err
		}
		if needsUpgrade {
			if err := upgradeBrewPackage(ctx, brewPath, spec); err != nil {
				return err
			}
		}
	}

	if !dockerDesktopInstalled() {
		if err := installDockerFromDMG(ctx, repoRoot); err != nil {
			return err
		}
	}
	return ensureDockerDaemonRunning(ctx)
}

func dockerDesktopInstalled() bool {
	_, err := os.Stat("/Applications/Docker.app")
	return err == nil
}

func installDockerFromDMG(ctx context.Context, repoRoot string) error {
	cacheDir := filepath.Join(repoRoot, ".ot", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	dmgPath := filepath.Join(cacheDir, "Docker.dmg")
	dmgURL := "https://desktop.docker.com/mac/main/arm64/Docker.dmg"

	fmt.Println("[bootstrap] Downloading Docker Desktop...")
	if err := run.RunInteractive(ctx, run.Spec{
		Program: "curl",
		Args:    []string{"-L", "--fail", "-o", dmgPath, dmgURL},
	}); err != nil {
		return fmt.Errorf("download Docker.dmg: %w", err)
	}

	fmt.Println("[bootstrap] Installing Docker Desktop (requires sudo)...")
	if err := run.RunInteractive(ctx, run.Spec{
		Program: "sudo",
		Args:    []string{"hdiutil", "attach", dmgPath},
	}); err != nil {
		return fmt.Errorf("attach DMG: %w", err)
	}
	defer func() {
		_ = run.RunInteractive(ctx, run.Spec{
			Program: "sudo",
			Args:    []string{"hdiutil", "detach", "/Volumes/Docker"},
		})
	}()
	if err := run.RunInteractive(ctx, run.Spec{
		Program: "sudo",
		Args:    []string{"/Volumes/Docker/Docker.app/Contents/MacOS/install"},
	}); err != nil {
		return fmt.Errorf("install Docker: %w", err)
	}

	return nil
}

func ensureDockerDaemonRunning(ctx context.Context) error {
	dockerInfo := func() error {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err := run.RunCapture(cctx, run.Spec{Program: "docker", Args: []string{"info"}})
		return err
	}

	if err := dockerInfo(); err == nil {
		return nil
	}

	_ = run.RunInteractive(ctx, run.Spec{Program: "open", Args: []string{"-ga", "Docker"}})

	deadline := time.Now().Add(3 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := dockerInfo(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}

	return fmt.Errorf("docker daemon not ready: %v", lastErr)
}

func ensurePorter(ctx context.Context, brewPath string) error {
	spec := brewPackageSpec{
		Name:     "porter-dev/porter/porter",
		Display:  "Porter",
		Kind:     brewPackageFormula,
		Binaries: []string{"porter"},
	}

	installedByBrew, err := brewPackageInstalled(ctx, brewPath, spec)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("porter"); err == nil {
		if supportsPorterDatastore(ctx) {
			if installedByBrew {
				needsUpgrade, err := brewPackageNeedsUpgrade(ctx, brewPath, spec)
				if err != nil {
					return err
				}
				if needsUpgrade {
					if err := upgradeBrewPackage(ctx, brewPath, spec); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if !installedByBrew {
			return errors.New("a 'porter' binary is installed but it does not support 'porter datastore connect'; uninstall the conflicting porter and install Porter.run via Homebrew: brew install porter-dev/porter/porter")
		}
	}

	if err := ensureBrewPackage(ctx, brewPath, spec); err != nil {
		return err
	}

	if _, err := exec.LookPath("porter"); err != nil {
		return errors.New("porter not found after installation")
	}
	if !supportsPorterDatastore(ctx) {
		return errors.New("porter installed but does not support 'datastore connect'; ensure you're using the Porter.run CLI (brew install porter-dev/porter/porter)")
	}
	return nil
}

func ensureAgentCLIs(ctx context.Context, brewPath string) error {
	specs := []brewPackageSpec{
		{
			Name:       "codex",
			Display:    "Codex",
			Kind:       brewPackageCask,
			Binaries:   []string{"codex"},
			CaskroomID: "codex",
		},
		{
			Name:       "claude-code",
			Display:    "Claude Code",
			Kind:       brewPackageCask,
			Binaries:   []string{"claude"},
			CaskroomID: "claude-code",
		},
	}

	for _, spec := range specs {
		if err := ensureBrewPackage(ctx, brewPath, spec); err != nil {
			return err
		}
	}

	return nil
}

func ensureInfisicalCLI(ctx context.Context, brewPath string) error {
	return ensureBrewPackage(ctx, brewPath, brewPackageSpec{
		Name:     "infisical/get-cli/infisical",
		Display:  "Infisical CLI",
		Kind:     brewPackageFormula,
		Binaries: []string{"infisical"},
	})
}

func ensureEntire(ctx context.Context, brewPath, repoRoot string) error {
	spec := brewPackageSpec{
		Name:       "entire",
		Display:    "Entire",
		Kind:       brewPackageCask,
		Tap:        "entireio/tap",
		Binaries:   []string{"entire"},
		CaskroomID: "entire",
	}

	if err := ensureBrewPackage(ctx, brewPath, spec); err != nil {
		return err
	}

	entirePath, err := findEntireBinary(ctx, brewPath)
	if err != nil {
		return fmt.Errorf("entire not found after installation: %w", err)
	}

	for _, agent := range []string{"codex", "claude-code"} {
		if err := run.RunInteractive(ctx, run.Spec{
			Program: entirePath,
			Args:    []string{"enable", "--agent", agent},
			Dir:     repoRoot,
		}); err != nil {
			return fmt.Errorf("entire enable --agent %s failed: %w", agent, err)
		}
	}

	return nil
}

func findEntireBinary(ctx context.Context, brewPath string) (string, error) {
	if p, err := exec.LookPath("entire"); err == nil {
		return p, nil
	}

	prefix, err := brewPrefix(ctx, brewPath)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(prefix, "bin", "entire")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	return "", exec.ErrNotFound
}

const datadogStaticAnalyzerZipName = "datadog-static-analyzer-git-hook-aarch64-apple-darwin.zip"

func ensureDatadogStaticAnalyzerGitHook(ctx context.Context, brewPath, repoRoot string) error {
	brewPrefix, err := run.RunCapture(ctx, run.Spec{
		Program: brewPath,
		Args:    []string{"--prefix"},
	})
	if err != nil {
		return fmt.Errorf("brew --prefix: %w", err)
	}
	brewPrefix = strings.TrimSpace(brewPrefix)
	target := filepath.Join(brewPrefix, "bin", "datadog-static-analyzer-git-hook")

	latestTag, latestErr := datadogStaticAnalyzerLatestTag(ctx)
	cacheTagPath := filepath.Join(repoRoot, ".ot", "cache", "datadog-static-analyzer.latest")
	if latestErr == nil && latestTag != "" {
		if cachedTag, err := os.ReadFile(cacheTagPath); err == nil {
			if strings.TrimSpace(string(cachedTag)) == latestTag && fileExists(target) {
				return nil
			}
		}
		if version, err := datadogStaticAnalyzerBinaryVersion(target); err == nil && version == latestTag {
			_ = os.WriteFile(cacheTagPath, []byte(latestTag+"\n"), 0o644)
			return nil
		}
	} else if fileExists(target) {
		return nil
	}

	cacheDir := filepath.Join(repoRoot, ".ot", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	zipPath := filepath.Join(cacheDir, datadogStaticAnalyzerZipName)

	fmt.Println("[bootstrap] Downloading Datadog static analyzer git hook...")
	if err := run.RunInteractive(ctx, run.Spec{
		Program: "gh",
		Args: []string{
			"release",
			"download",
			"--repo",
			"DataDog/datadog-static-analyzer",
			"--pattern",
			datadogStaticAnalyzerZipName,
			"--dir",
			cacheDir,
			"--clobber",
		},
	}); err != nil {
		return fmt.Errorf("download datadog static analyzer git hook: %w", err)
	}

	_ = zipPath

	unzipDir := filepath.Join(cacheDir, "datadog-static-analyzer-git-hook")
	if err := os.MkdirAll(unzipDir, 0o755); err != nil {
		return fmt.Errorf("create unzip dir: %w", err)
	}
	if err := run.RunInteractive(ctx, run.Spec{
		Program: "unzip",
		Args:    []string{"-o", zipPath, "-d", unzipDir},
	}); err != nil {
		return fmt.Errorf("unzip datadog static analyzer git hook: %w", err)
	}

	src := filepath.Join(unzipDir, "datadog-static-analyzer-git-hook")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("datadog-static-analyzer-git-hook not found in zip")
	}

	_ = os.Remove(target)
	if err := os.Rename(src, target); err != nil {
		return fmt.Errorf("install datadog-static-analyzer-git-hook: %w", err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return fmt.Errorf("chmod datadog-static-analyzer-git-hook: %w", err)
	}
	if _, err := exec.LookPath("xattr"); err == nil {
		if err := run.RunInteractive(ctx, run.Spec{
			Program: "xattr",
			Args:    []string{"-dr", "com.apple.quarantine", target},
		}); err != nil {
			return fmt.Errorf("xattr datadog-static-analyzer-git-hook: %w", err)
		}
	}

	if latestTag != "" {
		_ = os.WriteFile(cacheTagPath, []byte(latestTag+"\n"), 0o644)
	}

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func datadogStaticAnalyzerBinaryVersion(binPath string) (string, error) {
	if _, err := os.Stat(binPath); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := run.RunCapture(ctx, run.Spec{
		Program: binPath,
		Args:    []string{"--version"},
	})
	if err != nil {
		return "", err
	}
	return parseDatadogVersion(out), nil
}

func datadogStaticAnalyzerLatestTag(ctx context.Context) (string, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "gh",
		Args: []string{
			"release",
			"view",
			"--repo",
			"DataDog/datadog-static-analyzer",
			"--json",
			"tagName",
			"--jq",
			".tagName",
		},
	})
	if err != nil {
		return "", err
	}
	return parseDatadogVersion(out), nil
}

func parseDatadogVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	return value
}

func supportsPorterDatastore(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := run.RunCapture(cctx, run.Spec{Program: "porter", Args: []string{"datastore", "connect", "--help"}})
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(out), "secure tunnel") || strings.Contains(strings.ToLower(out), "datastore")
}

func installGitHooks(ctx context.Context, repoRoot string) error {
	_ = ctx
	if _, err := hooks.Install(repoRoot); err != nil {
		return err
	}
	return nil
}

type dependencyScope struct {
	backend  bool
	crm      bool
	frontend bool
}

func installProjectDependencies(ctx context.Context, repoRoot string, quiet bool, scope dependencyScope) error {
	runCmd := func(spec run.Spec) error {
		if quiet {
			return run.RunSilent(ctx, spec)
		}
		return run.RunInteractive(ctx, spec)
	}

	backendDir := filepath.Join(repoRoot, "backend")
	if scope.backend {
		if _, err := os.Stat(filepath.Join(backendDir, "pyproject.toml")); err == nil {
			if !uvSyncUpToDate(backendDir) {
				fmt.Println("[bootstrap] Installing backend Python dependencies...")
				if err := runCmd(run.Spec{
					Program: "uv",
					Args:    []string{"sync"},
					Dir:     backendDir,
				}); err != nil {
					return fmt.Errorf("uv sync backend: %w", err)
				}
			}
		}
	}

	crmDir := filepath.Join(repoRoot, "crm")
	if scope.crm {
		if _, err := os.Stat(filepath.Join(crmDir, "pyproject.toml")); err == nil {
			if !uvSyncUpToDate(crmDir) {
				fmt.Println("[bootstrap] Installing CRM Python dependencies...")
				if err := runCmd(run.Spec{
					Program: "uv",
					Args:    []string{"sync"},
					Dir:     crmDir,
				}); err != nil {
					return fmt.Errorf("uv sync crm: %w", err)
				}
			}
		}
	}

	frontendDir := filepath.Join(repoRoot, "frontend")
	if scope.frontend {
		if _, err := os.Stat(filepath.Join(frontendDir, "app", "package.json")); err == nil {
			if !npmInstallUpToDate(frontendDir) {
				fmt.Println("[bootstrap] Installing frontend Node.js dependencies...")
				if err := runCmd(run.Spec{
					Program: "npm",
					Args:    []string{"ci"},
					Dir:     frontendDir,
				}); err != nil {
					return fmt.Errorf("npm ci frontend: %w", err)
				}
			}
		}
	}

	return nil
}

func ensureBootstrapStateDirs(repoRoot string) error {
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ot", "overmind"), 0o755); err != nil {
		return fmt.Errorf("create .ot/overmind: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, ".ot", "locks"), 0o755); err != nil {
		return fmt.Errorf("create .ot/locks: %w", err)
	}
	return nil
}

func uvSyncUpToDate(dir string) bool {
	venv := filepath.Join(dir, ".venv")
	venvInfo, err := os.Stat(venv)
	if err != nil {
		return false
	}

	for _, lockFile := range []string{"uv.lock", "pyproject.toml"} {
		lockPath := filepath.Join(dir, lockFile)
		lockInfo, err := os.Stat(lockPath)
		if err != nil {
			continue
		}
		if lockInfo.ModTime().After(venvInfo.ModTime()) {
			return false
		}
	}
	return true
}

func npmInstallUpToDate(dir string) bool {
	nodeModules := filepath.Join(dir, "node_modules")
	nmInfo, err := os.Stat(nodeModules)
	if err != nil {
		return false
	}

	lockPath := filepath.Join(dir, "package-lock.json")
	lockInfo, err := os.Stat(lockPath)
	if err != nil {
		return true
	}
	return nmInfo.ModTime().After(lockInfo.ModTime())
}
