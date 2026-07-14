package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/bootstrap"
	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

func upgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade [all|frontend|backend|ot]",
		Short: "Upgrade dependencies and rebuild ot CLI",
		Long: `Upgrade dependencies (Go modules, frontend npm packages, Python uv packages) with a cooldown,
then rebuild and reinstall the ot CLI from source.`,
		Args: func(cmd *cobra.Command, args []string) error {
			return cobra.MaximumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			targets, err := resolveUpgradeTargets(args)
			if err != nil {
				return err
			}

			if targets.ot {
				if err := upgradeGoModules(cmd, rt); err != nil {
					return err
				}
				if err := buildAndInstallOt(cmd, rt); err != nil {
					return err
				}
			}

			if targets.frontend {
				if err := upgradeFrontend(cmd, rt, upgradeCooldown); err != nil {
					return err
				}
			}
			if targets.backend {
				if err := upgradeBackend(cmd, rt, uvCooldownValue()); err != nil {
					return err
				}
			}
			fmt.Println("[upgrade] Done!")
			return nil
		},
	}
	return cmd
}

type upgradeTargets struct {
	frontend bool
	backend  bool
	ot       bool
}

func resolveUpgradeTargets(args []string) (upgradeTargets, error) {
	if len(args) == 0 {
		return upgradeTargets{frontend: true, backend: true, ot: true}, nil
	}
	if len(args) != 1 {
		return upgradeTargets{}, fmt.Errorf("expected one target or none (valid: frontend, backend, ot)")
	}
	switch args[0] {
	case "all":
		return upgradeTargets{frontend: true, backend: true, ot: true}, nil
	case "frontend":
		return upgradeTargets{frontend: true}, nil
	case "backend":
		return upgradeTargets{backend: true}, nil
	case "ot":
		return upgradeTargets{ot: true}, nil
	default:
		return upgradeTargets{}, fmt.Errorf("unknown target %q (valid: frontend, backend, ot)", args[0])
	}
}

func upgradeFrontend(cmd *cobra.Command, rt *Runtime, cooldown time.Duration) error {
	fmt.Println("[upgrade] Upgrading frontend npm dependencies...")
	if err := enforceNpmCooldown(cmd, rt, cooldown); err != nil {
		return err
	}
	hasOutdated, err := npmOutdatedExists(cmd, rt)
	if err != nil {
		return err
	}
	if !hasOutdated {
		fmt.Println("[upgrade] Frontend npm dependencies already up to date (direct deps); skipping npm update.")
		return nil
	}
	return runWithSfw(cmd, run.Spec{
		Program: "npm",
		Args:    []string{"update"},
		Dir:     filepath.Join(rt.RepoRoot, "frontend"),
		Env: map[string]string{
			"NPM_CONFIG_PREFER_ONLINE": "true",
		},
	})
}

func upgradeBackend(cmd *cobra.Command, rt *Runtime, cooldown string) error {
	fmt.Println("[upgrade] Upgrading backend uv dependencies...")
	needsUpdate, err := uvUpdatesAvailable(cmd, rt.RepoRoot, filepath.Join(rt.RepoRoot, "backend"), cooldown)
	if err != nil {
		return err
	}
	if !needsUpdate {
		fmt.Println("[upgrade] Backend uv dependencies already up to date (or within cooldown); skipping uv lock.")
		return nil
	}
	return runWithSfw(cmd, run.Spec{
		Program: "uv",
		Args:    []string{"lock", "--upgrade", "--refresh", "--no-cache"},
		Dir:     filepath.Join(rt.RepoRoot, "backend"),
		Env: map[string]string{
			"UV_EXCLUDE_NEWER": cooldown,
		},
	})
}

func upgradeGoModules(cmd *cobra.Command, rt *Runtime) error {
	if strings.TrimSpace(os.Getenv("OT_SKIP_SELF_CHECK")) == "1" {
		fmt.Println("[upgrade] Skipping Go module upgrades during self-check.")
		return nil
	}
	if strings.TrimSpace(os.Getenv("OT_BOOTSTRAP_MODE")) == "1" {
		fmt.Println("[upgrade] Skipping Go module upgrades during bootstrap.")
		return nil
	}
	fmt.Println("[upgrade] Upgrading Go modules...")
	needsUpdate, candidates, err := goUpdatesAvailable(cmd, upgradeCooldown)
	if err != nil {
		return err
	}
	if !needsUpdate {
		fmt.Println("[upgrade] Go modules already up to date (or updates within cooldown); skipping GuardDog and go get.")
		return nil
	}
	if os.Getenv("OT_DEBUG_GO_UPGRADE") == "1" {
		fmt.Println("[upgrade] Go updates eligible (outside cooldown):")
		for _, item := range candidates {
			fmt.Printf("  - %s\n", item)
		}
	}
	if err := guarddogGoVerify(cmd, rt.RepoRoot); err != nil {
		return err
	}
	if err := run.RunInteractive(cmd.Context(), run.Spec{
		Program: "go",
		Args:    []string{"get", "-u", "./..."},
		Dir:     rt.RepoRoot,
	}); err != nil {
		return err
	}
	return run.RunInteractive(cmd.Context(), run.Spec{
		Program: "go",
		Args:    []string{"mod", "tidy"},
		Dir:     rt.RepoRoot,
	})
}

func buildAndInstallOt(cmd *cobra.Command, rt *Runtime) error {
	return buildAndInstallOtAt(cmd.Context(), rt.RepoRoot)
}

func buildAndInstallOtAt(ctx context.Context, repoRoot string) error {
	binDir := filepath.Join(repoRoot, ".ot", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	otBin := filepath.Join(binDir, "ot")

	fmt.Println("[upgrade] Building ot...")
	sha, err := repoHeadSHA(ctx, repoRoot)
	if err != nil {
		return err
	}
	if err := buildOtBinary(ctx, repoRoot, otBin, sha); err != nil {
		return fmt.Errorf("go build: %w", err)
	}

	symlinkPath, err := otInstallSymlinkPath(ctx)
	if err != nil {
		return err
	}
	_ = os.Remove(symlinkPath)
	if err := os.Symlink(otBin, symlinkPath); err != nil {
		return fmt.Errorf("symlink to %s: %w", symlinkPath, err)
	}

	fmt.Printf("[upgrade] ot installed to %s\n", symlinkPath)
	return nil
}

func repoHeadSHA(ctx context.Context, repoRoot string) (string, error) {
	sha, err := run.RunCapture(ctx, run.Spec{
		Program: "git",
		Args:    []string{"-C", repoRoot, "rev-parse", "HEAD"},
	})
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(sha), nil
}

func buildOtBinary(ctx context.Context, repoRoot, otBin, sha string) error {
	return run.RunInteractive(ctx, run.Spec{
		Program: "go",
		Args: []string{
			"build",
			"-ldflags",
			fmt.Sprintf("-X github.com/collinbentley1/cli/ot/cli.Version=%s", sha),
			"-o",
			otBin,
			"./ot",
		},
		Dir: repoRoot,
	})
}

func otInstallSymlinkPath(ctx context.Context) (string, error) {
	if bootstrap.IsCloudAgent() {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		binDir := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", binDir, err)
		}
		return filepath.Join(binDir, "ot"), nil
	}

	brewPrefix, err := run.RunCapture(ctx, run.Spec{
		Program: "brew",
		Args:    []string{"--prefix"},
	})
	if err != nil {
		return "", fmt.Errorf("brew --prefix: %w", err)
	}
	brewPrefix = strings.TrimSpace(brewPrefix)
	return filepath.Join(brewPrefix, "bin", "ot"), nil
}

var upgradeCooldown = 24 * time.Hour

func uvCooldownValue() string {
	return time.Now().Add(-upgradeCooldown).Format(time.RFC3339)
}

func npmOutdatedExists(cmd *cobra.Command, rt *Runtime) (bool, error) {
	deps := map[string]string{}
	if err := loadPackageJSONDeps(filepath.Join(rt.RepoRoot, "frontend"), deps); err != nil {
		return false, err
	}

	workspacePkgs, err := frontendWorkspacePackages(rt.RepoRoot)
	if err != nil {
		return false, err
	}
	for _, workspacePkg := range workspacePkgs {
		pkgDir := filepath.Join(rt.RepoRoot, "frontend", workspacePkg.RelPath)
		if err := loadPackageJSONDeps(pkgDir, deps); err != nil {
			return false, err
		}
	}
	allowedDependents, err := frontendDirectDependents(rt.RepoRoot)
	if err != nil {
		return false, err
	}
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)

	const batchSize = 40
	for i := 0; i < len(names); i += batchSize {
		end := i + batchSize
		if end > len(names) {
			end = len(names)
		}
		args := append([]string{"outdated", "--json"}, names[i:end]...)
		out, err := runCaptureWithSfw(cmd, run.Spec{
			Program: "npm",
			Args:    args,
			Dir:     filepath.Join(rt.RepoRoot, "frontend"),
		})
		if err != nil && strings.TrimSpace(out) == "" {
			return false, fmt.Errorf("npm outdated: %w", err)
		}
		out = strings.TrimSpace(out)
		if out == "" {
			continue
		}
		out, err = extractJSONPayload(out)
		if err != nil {
			return false, fmt.Errorf("parse npm outdated: %w", err)
		}
		batch := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(out), &batch); err != nil {
			return false, fmt.Errorf("parse npm outdated: %w", err)
		}
		for _, raw := range batch {
			entries, err := parseNpmOutdatedEntries(raw)
			if err != nil {
				return false, err
			}
			for _, entry := range entries {
				if isDirectNpmOutdatedEntry(entry, allowedDependents) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func uvUpdatesAvailable(cmd *cobra.Command, repoRoot, dir, cutoff string) (bool, error) {
	lockPath := filepath.Join(dir, "uv.lock")
	lockContent, err := os.ReadFile(lockPath)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", lockPath, err)
	}
	versions, err := parseUvLockVersions(string(lockContent))
	if err != nil {
		return false, err
	}
	declared, err := parseUvProjectDependencies(dir)
	if err != nil {
		return false, err
	}
	if len(declared) == 0 {
		return false, nil
	}
	cutoffTime, err := time.Parse(time.RFC3339, cutoff)
	if err != nil {
		return false, fmt.Errorf("parse uv cutoff: %w", err)
	}
	for _, dep := range declared {
		current := versions[dep]
		if current == "" {
			continue
		}
		out, err := runCaptureWithSfw(cmd, run.Spec{
			Program: "uv",
			Args:    []string{"pip", "index", "versions", dep, "--json"},
		})
		if err != nil {
			fmt.Printf("[upgrade] uv index lookup failed for %s; proceeding with upgrade check.\n", dep)
			return true, nil
		}
		out, err = extractJSONPayload(out)
		if err != nil {
			return false, fmt.Errorf("parse uv versions %s: %w", dep, err)
		}
		info, err := parseUvIndexVersions(out)
		if err != nil {
			return false, fmt.Errorf("parse uv versions %s: %w", dep, err)
		}
		releaseTime, ok := info.ReleaseTime(current)
		if ok && releaseTime.After(cutoffTime) {
			continue
		}
		for _, candidate := range info.NewerThan(current) {
			when, ok := info.ReleaseTime(candidate)
			if !ok {
				continue
			}
			if when.Before(cutoffTime) || when.Equal(cutoffTime) {
				return true, nil
			}
		}
	}
	return false, nil
}

type uvIndexVersions struct {
	Versions map[string]time.Time
}

func (u uvIndexVersions) ReleaseTime(version string) (time.Time, bool) {
	when, ok := u.Versions[version]
	return when, ok
}

func (u uvIndexVersions) NewerThan(version string) []string {
	current := normalizeSemver(version)
	if current == "" {
		return nil
	}
	latest := []string{}
	for v := range u.Versions {
		candidate := normalizeSemver(v)
		if candidate == "" {
			continue
		}
		if semver.Compare(candidate, current) > 0 {
			latest = append(latest, v)
		}
	}
	sort.Strings(latest)
	return latest
}

func normalizeSemver(version string) string {
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	canonical := semver.Canonical(version)
	if canonical == "" {
		return ""
	}
	return canonical
}

func parseUvIndexVersions(raw string) (uvIndexVersions, error) {
	var payload struct {
		Versions []struct {
			Version  string `json:"version"`
			Uploaded string `json:"uploaded"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return uvIndexVersions{}, err
	}
	versions := make(map[string]time.Time)
	for _, entry := range payload.Versions {
		if entry.Version == "" || entry.Uploaded == "" {
			continue
		}
		when, err := time.Parse(time.RFC3339, entry.Uploaded)
		if err != nil {
			continue
		}
		versions[entry.Version] = when
	}
	return uvIndexVersions{Versions: versions}, nil
}

func parseUvProjectDependencies(dir string) ([]string, error) {
	path := filepath.Join(dir, "pyproject.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	inProject := false
	inDeps := false
	var deps []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.Trim(line, "[]")
			inProject = section == "project"
			inDeps = false
			continue
		}
		if !inProject {
			continue
		}
		if strings.HasPrefix(line, "dependencies") && strings.Contains(line, "[") {
			inDeps = true
			continue
		}
		if inDeps {
			if strings.HasPrefix(line, "]") {
				inDeps = false
				continue
			}
			if strings.HasPrefix(line, "\"") {
				value := strings.Trim(line, "\",")
				name := strings.FieldsFunc(value, func(r rune) bool {
					return r == ' ' || r == '<' || r == '>' || r == '=' || r == '!' || r == '~'
				})
				if len(name) > 0 && name[0] != "" {
					deps = append(deps, name[0])
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return deps, nil
}

func parseUvLockVersions(raw string) (map[string]string, error) {
	versions := map[string]string{}
	var currentName string
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "name = ") {
			currentName = strings.Trim(strings.TrimPrefix(line, "name = "), "\"")
		}
		if strings.HasPrefix(line, "version = ") && currentName != "" {
			version := strings.Trim(strings.TrimPrefix(line, "version = "), "\"")
			versions[currentName] = version
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}

type npmOutdated struct {
	Current   string `json:"current"`
	Wanted    string `json:"wanted"`
	Latest    string `json:"latest"`
	Dependent string `json:"dependent"`
	Location  string `json:"location"`
}

func parseNpmOutdatedEntries(raw json.RawMessage) ([]npmOutdated, error) {
	rawStr := strings.TrimSpace(string(raw))
	if rawStr == "" || rawStr == "null" {
		return nil, nil
	}
	if rawStr[0] == '[' {
		var list []npmOutdated
		if err := json.Unmarshal([]byte(rawStr), &list); err != nil {
			return nil, err
		}
		return list, nil
	}
	var single npmOutdated
	if err := json.Unmarshal([]byte(rawStr), &single); err != nil {
		return nil, err
	}
	return []npmOutdated{single}, nil
}

func isDirectNpmOutdatedEntry(entry npmOutdated, allowedDependents map[string]struct{}) bool {
	if len(allowedDependents) == 0 {
		return true
	}
	dependent := strings.TrimSpace(entry.Dependent)
	if dependent == "" {
		return false
	}
	_, ok := allowedDependents[dependent]
	return ok
}

func enforceNpmCooldown(cmd *cobra.Command, rt *Runtime, cooldown time.Duration) error {
	cutoff := time.Now().Add(-cooldown)
	deps := map[string]string{}
	if err := loadPackageJSONDeps(filepath.Join(rt.RepoRoot, "frontend"), deps); err != nil {
		return err
	}

	workspacePkgs, err := frontendWorkspacePackages(rt.RepoRoot)
	if err != nil {
		return err
	}
	for _, workspacePkg := range workspacePkgs {
		pkgDir := filepath.Join(rt.RepoRoot, "frontend", workspacePkg.RelPath)
		if err := loadPackageJSONDeps(pkgDir, deps); err != nil {
			return err
		}
	}
	allowedDependents, err := frontendDirectDependents(rt.RepoRoot)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)

	outdated := map[string]npmOutdated{}
	const batchSize = 40
	for i := 0; i < len(names); i += batchSize {
		end := i + batchSize
		if end > len(names) {
			end = len(names)
		}
		args := append([]string{"outdated", "--json"}, names[i:end]...)
		out, err := runCaptureWithSfw(cmd, run.Spec{
			Program: "npm",
			Args:    args,
			Dir:     filepath.Join(rt.RepoRoot, "frontend"),
		})
		if err != nil && strings.TrimSpace(out) == "" {
			return fmt.Errorf("npm outdated: %w", err)
		}
		out = strings.TrimSpace(out)
		if out == "" {
			continue
		}
		out, err = extractJSONPayload(out)
		if err != nil {
			return fmt.Errorf("parse npm outdated: %w", err)
		}
		batch := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(out), &batch); err != nil {
			return fmt.Errorf("parse npm outdated: %w", err)
		}
		for name, raw := range batch {
			entries, err := parseNpmOutdatedEntries(raw)
			if err != nil {
				return fmt.Errorf("parse npm outdated %s: %w", name, err)
			}
			for _, entry := range entries {
				if isDirectNpmOutdatedEntry(entry, allowedDependents) {
					outdated[name] = entry
					break
				}
			}
		}
	}

	if len(outdated) == 0 {
		return nil
	}

	blocked := make([]string, 0)
	for name, info := range outdated {
		version := info.Wanted
		if version == "" {
			version = info.Latest
		}
		if version == "" {
			continue
		}
		out, err := runCaptureWithSfw(cmd, run.Spec{
			Program: "npm",
			Args:    []string{"view", name, "time", "--json"},
		})
		if err != nil {
			return fmt.Errorf("npm view time %s: %w", name, err)
		}
		out, err = extractJSONPayload(out)
		if err != nil {
			return fmt.Errorf("parse npm time %s: %w", name, err)
		}
		release, err := npmReleaseTime(out, version)
		if err != nil {
			return err
		}
		if release.After(cutoff) {
			blocked = append(blocked, fmt.Sprintf("%s@%s", name, version))
		}
	}

	if len(blocked) > 0 {
		sort.Strings(blocked)
		return fmt.Errorf("npm cooldown active; retry after %s (blocked: %s)", cutoff.Format(time.RFC3339), strings.Join(blocked, ", "))
	}
	return nil
}

type packageJSON struct {
	Name            string            `json:"name"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

type workspacesConfig struct {
	Workspaces []string `json:"workspaces"`
}

type frontendWorkspacePackage struct {
	RelPath string
	Name    string
}

func loadPackageJSONDeps(dir string, out map[string]string) error {
	path := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for name, spec := range pkg.Dependencies {
		if name != "" {
			out[name] = spec
		}
	}
	for name, spec := range pkg.DevDependencies {
		if name != "" {
			out[name] = spec
		}
	}
	return nil
}

func frontendWorkspacePackages(repoRoot string) ([]frontendWorkspacePackage, error) {
	frontendDir := filepath.Join(repoRoot, "frontend")
	path := filepath.Join(frontendDir, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg workspacesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Workspaces) == 0 {
		return nil, nil
	}
	workspaceMap := make(map[string]frontendWorkspacePackage)
	for _, pattern := range cfg.Workspaces {
		matches, err := filepath.Glob(filepath.Join(frontendDir, pattern))
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", pattern, err)
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || !info.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(match, "package.json")); err != nil {
				continue
			}
			rel, err := filepath.Rel(frontendDir, match)
			if err != nil {
				continue
			}
			pkgData, err := os.ReadFile(filepath.Join(match, "package.json"))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", filepath.Join(match, "package.json"), err)
			}
			var pkg packageJSON
			if err := json.Unmarshal(pkgData, &pkg); err != nil {
				return nil, fmt.Errorf("parse %s: %w", filepath.Join(match, "package.json"), err)
			}
			workspaceMap[rel] = frontendWorkspacePackage{
				RelPath: rel,
				Name:    pkg.Name,
			}
		}
	}
	if len(workspaceMap) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(workspaceMap))
	for ws := range workspaceMap {
		keys = append(keys, ws)
	}
	sort.Strings(keys)
	workspaces := make([]frontendWorkspacePackage, 0, len(keys))
	for _, ws := range keys {
		workspaces = append(workspaces, workspaceMap[ws])
	}
	return workspaces, nil
}

func frontendDirectDependents(repoRoot string) (map[string]struct{}, error) {
	frontendDir := filepath.Join(repoRoot, "frontend")
	rootData, err := os.ReadFile(filepath.Join(frontendDir, "package.json"))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Join(frontendDir, "package.json"), err)
	}
	var rootPkg packageJSON
	if err := json.Unmarshal(rootData, &rootPkg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Join(frontendDir, "package.json"), err)
	}
	dependents := map[string]struct{}{}
	if rootPkg.Name != "" {
		dependents[rootPkg.Name] = struct{}{}
	}
	workspacePkgs, err := frontendWorkspacePackages(repoRoot)
	if err != nil {
		return nil, err
	}
	for _, workspacePkg := range workspacePkgs {
		if workspacePkg.RelPath != "" {
			dependents[workspacePkg.RelPath] = struct{}{}
			dependents[filepath.Base(workspacePkg.RelPath)] = struct{}{}
		}
		if workspacePkg.Name != "" {
			dependents[workspacePkg.Name] = struct{}{}
		}
	}
	return dependents, nil
}

func npmReleaseTime(raw string, version string) (time.Time, error) {
	var data map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &data); err != nil {
		return time.Time{}, fmt.Errorf("parse npm time: %w", err)
	}
	val, ok := data[version]
	if !ok {
		return time.Time{}, fmt.Errorf("npm time missing version %s", version)
	}
	str, ok := val.(string)
	if !ok || strings.TrimSpace(str) == "" {
		return time.Time{}, fmt.Errorf("npm time invalid for %s", version)
	}
	ts, err := time.Parse(time.RFC3339, str)
	if err != nil {
		ts, err = time.Parse(time.RFC3339Nano, str)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse npm time %s: %w", version, err)
		}
	}
	return ts, nil
}

func extractJSONPayload(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", nil
	}
	first := strings.IndexAny(clean, "[{")
	if first == -1 {
		return "", fmt.Errorf("no json payload found")
	}
	if clean[first] == '{' {
		last := strings.LastIndex(clean, "}")
		if last == -1 || last < first {
			return "", fmt.Errorf("unterminated json object")
		}
		return strings.TrimSpace(clean[first : last+1]), nil
	}
	last := strings.LastIndex(clean, "]")
	if last == -1 || last < first {
		return "", fmt.Errorf("unterminated json array")
	}
	return strings.TrimSpace(clean[first : last+1]), nil
}

type goModuleUpdate struct {
	Path   string `json:"Path"`
	Update *struct {
		Version string `json:"Version"`
		Time    string `json:"Time"`
	} `json:"Update"`
	Indirect bool `json:"Indirect"`
}

func goUpdatesAvailable(cmd *cobra.Command, cooldown time.Duration) (bool, []string, error) {
	cutoff := time.Now().Add(-cooldown)
	out, err := run.RunCapture(cmd.Context(), run.Spec{
		Program: "go",
		Args:    []string{"list", "-m", "-u", "-json", "all"},
	})
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg != "" {
			return false, nil, fmt.Errorf("go list -m -u failed: %s", msg)
		}
		return false, nil, fmt.Errorf("go list -m -u: %w", err)
	}

	dec := json.NewDecoder(bufio.NewReader(strings.NewReader(out)))
	candidates := make([]string, 0)
	for {
		var mod goModuleUpdate
		if err := dec.Decode(&mod); err != nil {
			if err == io.EOF {
				break
			}
			return false, nil, fmt.Errorf("parse go list output: %w", err)
		}
		if mod.Indirect {
			continue
		}
		if mod.Update == nil || strings.TrimSpace(mod.Update.Version) == "" {
			continue
		}
		var updateTime time.Time
		if strings.TrimSpace(mod.Update.Time) != "" {
			parsed, err := time.Parse(time.RFC3339Nano, mod.Update.Time)
			if err != nil {
				return false, nil, fmt.Errorf("parse go module time for %s: %w", mod.Path, err)
			}
			updateTime = parsed
		} else {
			parsed, err := goModuleTime(cmd, mod.Path, mod.Update.Version)
			if err != nil {
				return false, nil, err
			}
			updateTime = parsed
		}
		if updateTime.IsZero() {
			continue
		}
		if !updateTime.After(cutoff) {
			candidates = append(candidates, fmt.Sprintf("%s@%s (%s)", mod.Path, mod.Update.Version, updateTime.Format(time.RFC3339)))
		}
	}
	if len(candidates) > 0 {
		sort.Strings(candidates)
		return true, candidates, nil
	}
	return false, nil, nil
}

type goDownloadInfo struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Time    string `json:"Time"`
}

func goModuleTime(cmd *cobra.Command, path string, version string) (time.Time, error) {
	out, err := run.RunCapture(cmd.Context(), run.Spec{
		Program: "go",
		Args:    []string{"mod", "download", "-json", path + "@" + version},
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("go mod download %s@%s: %w", path, version, err)
	}
	var info goDownloadInfo
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &info); err != nil {
		return time.Time{}, fmt.Errorf("parse go mod download %s@%s: %w", path, version, err)
	}
	if info.Time == "" {
		return time.Time{}, fmt.Errorf("go mod download missing time for %s@%s", path, version)
	}
	ts, err := time.Parse(time.RFC3339Nano, info.Time)
	if err != nil {
		ts, err = time.Parse(time.RFC3339, info.Time)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse go mod download time %s@%s: %w", path, version, err)
		}
	}
	return ts, nil
}

func runWithSfw(cmd *cobra.Command, spec run.Spec) error {
	return run.RunInteractive(cmd.Context(), wrapSfw(spec))
}

func runCaptureWithSfw(cmd *cobra.Command, spec run.Spec) (string, error) {
	return run.RunCapture(cmd.Context(), wrapSfw(spec))
}

func wrapSfw(spec run.Spec) run.Spec {
	args := make([]string, 0, len(spec.Args)+1)
	args = append(args, spec.Program)
	args = append(args, spec.Args...)
	return run.Spec{
		Program: "sfw",
		Args:    args,
		Dir:     spec.Dir,
		Env:     spec.Env,
	}
}

func guarddogGoVerify(cmd *cobra.Command, repoRoot string) error {
	fmt.Println("[upgrade] Running GuardDog for Go modules...")
	out, err := run.RunCapture(cmd.Context(), run.Spec{
		Program: "docker",
		Args: []string{
			"run",
			"--rm",
			"-v",
			fmt.Sprintf("%s:/workspace", repoRoot),
			"-w",
			"/workspace",
			"ghcr.io/datadog/guarddog",
			"--log-level",
			"ERROR",
			"go",
			"verify",
			"--output-format",
			"sarif",
			"/workspace/go.mod",
		},
		Dir: repoRoot,
	})
	if err != nil {
		return fmt.Errorf("guarddog go verify: %w", err)
	}

	return parseGuarddogSarif(out)
}

func parseGuarddogSarif(raw string) error {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return fmt.Errorf("guarddog sarif output empty")
	}
	if !strings.HasPrefix(clean, "{") {
		start := strings.Index(clean, "{")
		end := strings.LastIndex(clean, "}")
		if start == -1 || end == -1 || end <= start {
			return fmt.Errorf("parse guarddog sarif output: %s", clean)
		}
		clean = strings.TrimSpace(clean[start : end+1])
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(clean), &payload); err != nil {
		return fmt.Errorf("parse guarddog sarif output: %w", err)
	}
	version, _ := payload["version"].(string)
	if strings.TrimSpace(version) == "" {
		return fmt.Errorf("guarddog sarif output missing version")
	}

	runs, _ := payload["runs"].([]any)
	issues := 0
	for _, runVal := range runs {
		runObj, ok := runVal.(map[string]any)
		if !ok {
			continue
		}
		results, _ := runObj["results"].([]any)
		issues += len(results)
	}
	if issues > 0 {
		return fmt.Errorf("guarddog found %d issues in Go modules", issues)
	}
	return nil
}
