package env

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/providers"
	"github.com/collinbentley1/cli/ot/run"
	"golang.org/x/sys/unix"
)

const (
	defaultInfisicalEnvironment = "dev"
	activePathEnv               = "OT_INFISICAL_ACTIVE_PATH"
	activeEnvironmentEnv        = "OT_INFISICAL_ACTIVE_ENV"
)

var ErrReexecuted = errors.New("reexecuted under infisical")

type Options struct {
	Environment      string
	Paths            []string
	ProjectConfigDir string
	Args             []string
}

type RunOptions struct {
	Environment      string
	Path             string
	ProjectConfigDir string
	Watch            bool
}

var (
	lookPath           = exec.LookPath
	runWithSignals     = run.RunInteractiveWithSignals
	runInteractive     = run.RunInteractive
	runCapture         = run.RunCapture
	currentExecutable  = os.Executable
	ensureLoginHandler = ensureInfisicalLogin
)

func RunSelfIfNeeded(ctx context.Context, opts Options) error {
	if !providers.SecretsEnabled() {
		return nil
	}
	paths := normalizePaths(opts.Paths)
	if os.Getenv("OT_ENV_DEBUG") != "" && len(paths) == 0 {
		fmt.Println("[env] no Infisical paths configured for this command")
	}
	if len(paths) == 0 {
		return nil
	}
	if len(paths) != 1 {
		return fmt.Errorf("infisical run supports one secret path per command; got %s", strings.Join(paths, ", "))
	}
	if _, err := lookPath("infisical"); err != nil {
		if cloudAgentEnv() {
			if installErr := ensureCloudAgentInfisical(ctx); installErr != nil {
				return installErr
			}
		}
	}
	if _, err := lookPath("infisical"); err != nil {
		return fmt.Errorf("infisical CLI not found; %s", infisicalInstallHint())
	}

	environment := normalizeEnvironment(opts.Environment)
	path := paths[0]
	if os.Getenv(activePathEnv) == path && os.Getenv(activeEnvironmentEnv) == environment {
		return nil
	}

	if err := ensureLoginHandler(ctx, opts.ProjectConfigDir, environment, path); err != nil {
		return err
	}

	executable, err := currentExecutable()
	if err != nil {
		return fmt.Errorf("resolve ot executable: %w", err)
	}
	spec := WrapRunSpec(run.Spec{
		Program: executable,
		Args:    opts.Args,
		Dir:     opts.ProjectConfigDir,
		Env: map[string]string{
			activePathEnv:        path,
			activeEnvironmentEnv: environment,
		},
	}, RunOptions{
		Environment:      environment,
		Path:             path,
		ProjectConfigDir: opts.ProjectConfigDir,
	})

	if os.Getenv("OT_ENV_DEBUG") != "" {
		fmt.Printf("[env] re-executing under Infisical path %q from %s\n", path, environment)
	}
	if err := runWithSignals(ctx, spec); err != nil {
		return err
	}
	return ErrReexecuted
}

// ensureInfisicalLogin makes sure the local Infisical CLI has a usable
// session before we re-exec under `infisical run`. If the developer hasn't
// run `infisical login` and no machine credentials are available, we drop
// them into the standard interactive `infisical login` flow (which on a
// developer Mac opens the browser-based OAuth path).
//
// In agent / CI environments universal-auth env vars are present, and the
// per-process bash wrapper from WrapRunSpec handles login itself; we skip
// the interactive prompt in that case.
func ensureInfisicalLogin(ctx context.Context, repoRoot, environment, path string) error {
	if useRuntimeUniversalAuth() {
		return nil
	}
	if strings.TrimSpace(os.Getenv("INFISICAL_TOKEN")) != "" {
		return nil
	}
	hasSession, probeErr := hasInfisicalSession(ctx, repoRoot, environment, path)
	if probeErr != nil {
		return probeErr
	}
	if hasSession {
		return nil
	}

	// File-lock so two concurrent ot invocations don't both pop a browser.
	lockDir := filepath.Join(repoRoot, ".ot", "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return fmt.Errorf("create infisical login lock dir: %w", err)
	}
	lockPath := filepath.Join(lockDir, "infisical-auth.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open infisical login lock: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock infisical login lock: %w", err)
	}
	defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()

	hasSession, probeErr = hasInfisicalSession(ctx, repoRoot, environment, path)
	if probeErr != nil {
		return probeErr
	}
	if hasSession {
		return nil
	}

	fmt.Fprintln(os.Stderr, "Infisical session not found; launching `infisical login` (a browser window may open).")
	args := []string{"login"}
	if domain := strings.TrimSpace(os.Getenv("INFISICAL_API_URL")); domain != "" {
		args = append(args, "--domain", domain)
	}
	if err := runInteractive(ctx, run.Spec{
		Program: "infisical",
		Args:    args,
		Dir:     repoRoot,
	}); err != nil {
		return fmt.Errorf("infisical login: %w", err)
	}
	hasSession, probeErr = hasInfisicalSession(ctx, repoRoot, environment, path)
	if probeErr != nil {
		return probeErr
	}
	if !hasSession {
		return errors.New("infisical login did not complete; rerun `ot` once you've finished signing in")
	}
	return nil
}

// hasInfisicalSession probes whether `infisical run` will succeed without
// requiring a fresh login by running a no-op command through it. This tests
// any auth path the CLI accepts (persisted login, INFISICAL_TOKEN, etc.) —
// so it stays correct as the CLI evolves and avoids profile-switching
// commands that fail in non-interactive contexts.
//
// Returns (true, nil) when the probe succeeds, (false, nil) only when the
// probe fails with the CLI's "must be logged in" message, and (false, err)
// for any other failure (bad path, bad projectId, network) so the caller
// can surface the real error instead of routing through interactive login.
func hasInfisicalSession(ctx context.Context, repoRoot, environment, path string) (bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{
		"--silent",
		"--log-level=error",
		"run",
		"--env=" + environment,
		"--path=" + path,
	}
	if dir := strings.TrimSpace(repoRoot); dir != "" {
		args = append(args, "--project-config-dir", dir)
	}
	if projectID := strings.TrimSpace(os.Getenv("INFISICAL_PROJECT_ID")); projectID != "" {
		args = append(args, "--projectId", projectID)
	}
	args = append(args, "--", "true")
	out, err := runCapture(probeCtx, run.Spec{Program: "infisical", Args: args})
	if err == nil {
		return true, nil
	}
	if isInfisicalNotLoggedInMessage(out) {
		return false, nil
	}
	return false, fmt.Errorf("infisical session probe failed: %w: %s", err, strings.TrimSpace(out))
}

func isInfisicalNotLoggedInMessage(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "must be logged in") ||
		strings.Contains(lower, "you must be logged in")
}

func infisicalInstallHint() string {
	if runtime.GOOS == "darwin" && os.Getenv("OT_CLOUD_AGENT") == "" {
		return "install it with `brew install infisical/get-cli/infisical`"
	}
	return "install it in the cloud environment setup script from Infisical's official apt repository"
}

func cloudAgentEnv() bool {
	if os.Getenv("OT_CLOUD_AGENT") != "" || os.Getenv("OT_CLOUD_AGENT_HOST") != "" {
		return true
	}
	return os.Getenv("CODEX_ENV_GO_VERSION") != "" || os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != ""
}

func ensureCloudAgentInfisical(ctx context.Context) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home for Infisical install: %w", err)
	}
	localBin := home + "/.local/bin"
	path := os.Getenv("PATH")
	found := false
	for _, entry := range strings.Split(path, ":") {
		if entry == localBin {
			found = true
			break
		}
	}
	if !found {
		if path == "" {
			_ = os.Setenv("PATH", localBin)
		} else {
			_ = os.Setenv("PATH", localBin+":"+path)
		}
	}

	script := `
set -euo pipefail
mkdir -p "$HOME/.local/bin"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) infisical_arch=amd64 ;;
  aarch64|arm64) infisical_arch=arm64 ;;
  *) echo "unsupported Infisical arch: $arch" >&2; exit 1 ;;
esac
version="0.43.79"
tmp_dir="$(mktemp -d)"
curl --fail --location --silent --show-error --max-time 90 \
  "https://github.com/Infisical/cli/releases/download/v${version}/cli_${version}_linux_${infisical_arch}.tar.gz" \
  -o "$tmp_dir/infisical.tgz"
tar -C "$tmp_dir" -xzf "$tmp_dir/infisical.tgz" infisical
install -m 0755 "$tmp_dir/infisical" "$HOME/.local/bin/infisical"
rm -rf "$tmp_dir"
`
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "bash",
		Args:    []string{"-lc", script},
	})
	if err != nil {
		return fmt.Errorf("install infisical for cloud agent: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func WrapRunSpec(spec run.Spec, opts RunOptions) run.Spec {
	if !providers.SecretsEnabled() {
		return spec
	}
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return spec
	}
	args := runArgs(opts)
	args = append(args, "--", spec.Program)
	args = append(args, spec.Args...)

	if useRuntimeUniversalAuth() {
		return run.Spec{
			Program: "bash",
			Args:    append([]string{"-lc", universalAuthRunScript(runArgs(opts)), "ot-infisical-run", spec.Program}, spec.Args...),
			Dir:     spec.Dir,
			Env:     spec.Env,
		}
	}

	return run.Spec{
		Program: "infisical",
		Args:    args,
		Dir:     spec.Dir,
		Env:     spec.Env,
	}
}

func WrapRunShellCommand(command string, opts RunOptions) string {
	if !providers.SecretsEnabled() {
		return command
	}
	if useRuntimeUniversalAuth() {
		// Mirror WrapRunSpec: the run script quotes every embedded arg
		// itself and consumes the wrapped command via its trailing
		// `-- "$@"`, so the command must arrive as real positional args
		// (quoted exactly once) rather than being embedded pre-quoted.
		return "bash -lc " + shellQuote(universalAuthRunScript(shellRunArgs(opts))) +
			" ot-infisical-run bash -lc " + shellQuote(command)
	}
	args := shellRunArgs(opts)
	args = append(args, "--", "bash", "-lc", shellQuote(command))
	return strings.Join(append([]string{"infisical"}, args...), " ")
}

func runArgs(opts RunOptions) []string {
	args := []string{
		"--silent",
		"--log-level=error",
		"run",
		"--env=" + normalizeEnvironment(opts.Environment),
		"--path=" + strings.TrimSpace(opts.Path),
	}
	if projectConfigDir := strings.TrimSpace(opts.ProjectConfigDir); projectConfigDir != "" {
		args = append(args, "--project-config-dir", projectConfigDir)
	}
	if projectID := strings.TrimSpace(os.Getenv("INFISICAL_PROJECT_ID")); projectID != "" {
		args = append(args, "--projectId", projectID)
	}
	if opts.Watch {
		args = append(args, "--watch")
	}
	return args
}

func shellRunArgs(opts RunOptions) []string {
	args := []string{
		"--silent",
		"--log-level=error",
		"run",
		"--env=" + normalizeEnvironment(opts.Environment),
		"--path=" + strings.TrimSpace(opts.Path),
	}
	if projectConfigDir := strings.TrimSpace(opts.ProjectConfigDir); projectConfigDir != "" {
		args = append(args, "--project-config-dir="+projectConfigDir)
	}
	if projectID := strings.TrimSpace(os.Getenv("INFISICAL_PROJECT_ID")); projectID != "" {
		args = append(args, "--projectId="+projectID)
	}
	if opts.Watch {
		args = append(args, "--watch")
	}
	return args
}

func useRuntimeUniversalAuth() bool {
	return strings.TrimSpace(os.Getenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID")) != "" &&
		strings.TrimSpace(os.Getenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET")) != ""
}

func universalAuthRunScript(args []string) string {
	return `set -euo pipefail
if [ -z "${INFISICAL_TOKEN:-}" ] && [ -n "${INFISICAL_UNIVERSAL_AUTH_CLIENT_ID:-}" ] && [ -n "${INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET:-}" ]; then
  INFISICAL_TOKEN="$(infisical login --domain "${INFISICAL_API_URL:-https://app.infisical.com/api}" --method=universal-auth --client-id "$INFISICAL_UNIVERSAL_AUTH_CLIENT_ID" --client-secret "$INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET" --plain --silent)"
  export INFISICAL_TOKEN
fi
exec infisical ` + strings.Join(shellQuoteArgs(args), " ") + ` -- "$@"`
}

func shellQuoteArgs(args []string) []string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return quoted
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func normalizeEnvironment(environment string) string {
	value := strings.TrimSpace(os.ExpandEnv(environment))
	if value == "" {
		return defaultInfisicalEnvironment
	}
	return value
}

func normalizePaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		value := strings.TrimSpace(os.ExpandEnv(path))
		if value == "" {
			continue
		}
		normalized = append(normalized, value)
	}
	return normalized
}

// Infisical is the shipped secrets-provider implementation: it wraps
// commands under `infisical run` so the mapped secret scope is injected
// into the child environment and never written to disk.
type Infisical struct{}

var _ providers.Secrets = Infisical{}

func (Infisical) Name() string { return providers.SecretsInfisical }

func (Infisical) WrapRunSpec(spec run.Spec, opts providers.SecretsRunOptions) run.Spec {
	return WrapRunSpec(spec, RunOptions(opts))
}

func (Infisical) WrapShellCommand(command string, opts providers.SecretsRunOptions) string {
	return WrapRunShellCommand(command, RunOptions(opts))
}
