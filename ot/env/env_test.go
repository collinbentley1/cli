package env

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/collinbentley1/cli/ot/run"
)

func TestRunSelfIfNeededSkipsWhenNoPathsConfigured(t *testing.T) {
	originalRunWithSignals := runWithSignals
	t.Cleanup(func() {
		runWithSignals = originalRunWithSignals
	})

	runWithSignals = func(ctx context.Context, spec run.Spec) error {
		t.Fatalf("runWithSignals should not be called, got %+v", spec)
		return nil
	}

	if err := RunSelfIfNeeded(context.Background(), Options{}); err != nil {
		t.Fatalf("RunSelfIfNeeded() returned error: %v", err)
	}
}

func TestRunSelfIfNeededRejectsMultiplePaths(t *testing.T) {
	err := RunSelfIfNeeded(context.Background(), Options{Paths: []string{"/backend", "/frontend"}})
	if err == nil {
		t.Fatal("expected multi-path error")
	}
	if got := err.Error(); got != "infisical run supports one secret path per command; got /backend, /frontend" {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestRunSelfIfNeededSkipsWhenAlreadyActive(t *testing.T) {
	t.Setenv(activePathEnv, "/backend")
	t.Setenv(activeEnvironmentEnv, "dev")

	originalRunWithSignals := runWithSignals
	t.Cleanup(func() {
		runWithSignals = originalRunWithSignals
	})

	runWithSignals = func(ctx context.Context, spec run.Spec) error {
		t.Fatalf("runWithSignals should not be called, got %+v", spec)
		return nil
	}

	if err := RunSelfIfNeeded(context.Background(), Options{
		Environment: "dev",
		Paths:       []string{"/backend"},
	}); err != nil {
		t.Fatalf("RunSelfIfNeeded() returned error: %v", err)
	}
}

func TestRunSelfIfNeededReexecutesUnderInfisicalRun(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_ID", "")
	originalLookPath := lookPath
	originalRunWithSignals := runWithSignals
	originalCurrentExecutable := currentExecutable
	originalEnsureLoginHandler := ensureLoginHandler
	t.Cleanup(func() {
		lookPath = originalLookPath
		runWithSignals = originalRunWithSignals
		currentExecutable = originalCurrentExecutable
		ensureLoginHandler = originalEnsureLoginHandler
	})

	lookPath = func(file string) (string, error) {
		if file != "infisical" {
			return "", exec.ErrNotFound
		}
		return "/opt/homebrew/bin/infisical", nil
	}
	currentExecutable = func() (string, error) {
		return "/opt/homebrew/bin/ot", nil
	}
	ensureLoginHandler = func(_ context.Context, _, _, _ string) error { return nil }

	var got run.Spec
	runWithSignals = func(ctx context.Context, spec run.Spec) error {
		got = spec
		return nil
	}

	err := RunSelfIfNeeded(context.Background(), Options{
		Environment:      "dev",
		Paths:            []string{"/backend"},
		ProjectConfigDir: "/repo",
		Args:             []string{"up", "backend"},
	})
	if !errors.Is(err, ErrReexecuted) {
		t.Fatalf("RunSelfIfNeeded() error = %v, want ErrReexecuted", err)
	}

	wantArgs := []string{
		"--silent",
		"--log-level=error",
		"run",
		"--env=dev",
		"--path=/backend",
		"--project-config-dir",
		"/repo",
		"--",
		"/opt/homebrew/bin/ot",
		"up",
		"backend",
	}
	if got.Program != "infisical" {
		t.Fatalf("Program = %q, want infisical", got.Program)
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("Args = %v, want %v", got.Args, wantArgs)
	}
	if got.Dir != "/repo" {
		t.Fatalf("Dir = %q, want /repo", got.Dir)
	}
	wantEnv := map[string]string{
		activePathEnv:        "/backend",
		activeEnvironmentEnv: "dev",
	}
	if !reflect.DeepEqual(got.Env, wantEnv) {
		t.Fatalf("Env = %v, want %v", got.Env, wantEnv)
	}
}

func TestWrapRunSpecUsesInfisicalRun(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_ID", "")
	spec := run.Spec{
		Program: "uv",
		Args:    []string{"run", "uvicorn", "server.main:app"},
		Dir:     "/repo/backend",
		Env: map[string]string{
			"PYTHONUNBUFFERED": "1",
		},
	}

	got := WrapRunSpec(spec, RunOptions{
		Environment:      "dev",
		Path:             "/backend",
		ProjectConfigDir: "/repo",
		Watch:            true,
	})

	wantArgs := []string{
		"--silent",
		"--log-level=error",
		"run",
		"--env=dev",
		"--path=/backend",
		"--project-config-dir",
		"/repo",
		"--watch",
		"--",
		"uv",
		"run",
		"uvicorn",
		"server.main:app",
	}
	if got.Program != "infisical" {
		t.Fatalf("Program = %q, want infisical", got.Program)
	}
	if !reflect.DeepEqual(got.Args, wantArgs) {
		t.Fatalf("Args = %v, want %v", got.Args, wantArgs)
	}
	if got.Dir != spec.Dir {
		t.Fatalf("Dir = %q, want %q", got.Dir, spec.Dir)
	}
	if !reflect.DeepEqual(got.Env, spec.Env) {
		t.Fatalf("Env = %v, want %v", got.Env, spec.Env)
	}
}

func TestWrapRunSpecUsesRuntimeUniversalAuthWithoutPersistedLogin(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "client-id")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "client-secret")
	t.Setenv("INFISICAL_PROJECT_ID", "project-id")

	got := WrapRunSpec(run.Spec{
		Program: "ot",
		Args:    []string{"check", "ot"},
		Dir:     "/repo",
	}, RunOptions{
		Environment:      "dev",
		Path:             "/ot",
		ProjectConfigDir: "/repo",
	})

	if got.Program != "bash" {
		t.Fatalf("Program = %q, want bash", got.Program)
	}
	if len(got.Args) < 5 || got.Args[0] != "-lc" {
		t.Fatalf("unexpected bash args: %v", got.Args)
	}
	script := got.Args[1]
	for _, want := range []string{
		"infisical login",
		"INFISICAL_TOKEN",
		"--path=/ot",
		"--projectId",
		`exec infisical`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("runtime auth script missing %q:\n%s", want, script)
		}
	}
	wantTail := []string{"ot-infisical-run", "ot", "check", "ot"}
	if !reflect.DeepEqual(got.Args[2:], wantTail) {
		t.Fatalf("Args tail = %v, want %v", got.Args[2:], wantTail)
	}
}

// Regression test: the universal-auth path of WrapRunShellCommand used to
// embed the pre-quoted command inside the run script, whose shellQuoteArgs
// quoted every arg a second time — so the once-de-quoted command arrived at
// bash as a single word ("echo hi && echo bye: command not found", exit 127)
// and every multi-word procfile line broke whenever universal-auth env vars
// were set. Execute the generated command line against a stub `infisical`
// and assert the wrapped multi-word command actually runs.
func TestWrapRunShellCommandUniversalAuthExecutesMultiWordCommand(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "client-id")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "client-secret")
	t.Setenv("INFISICAL_PROJECT_ID", "")
	t.Setenv("OT_SECRETS_PROVIDER", "")

	command := WrapRunShellCommand("echo hi && echo bye", RunOptions{
		Environment:      "dev",
		Path:             "/frontend",
		ProjectConfigDir: ".",
	})

	// Stub infisical: skip everything up to `--`, then exec the wrapped
	// command — the same contract the real `infisical run -- cmd...` has.
	stubDir := t.TempDir()
	stub := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--" ]; then shift; break; fi
  shift
done
exec "$@"
`
	if err := os.WriteFile(filepath.Join(stubDir, "infisical"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write stub infisical: %v", err)
	}
	// Stub bash that maps -lc to -c: the generated line uses a login shell,
	// and on macOS /etc/profile's path_helper rebuilds PATH with system dirs
	// first, letting a real infisical beat the stub. The quoting/positional-
	// arg plumbing under test is identical in a non-login shell.
	bashStub := `#!/bin/sh
if [ "$1" = "-lc" ]; then
  shift
  exec /bin/bash -c "$@"
fi
exec /bin/bash "$@"
`
	if err := os.WriteFile(filepath.Join(stubDir, "bash"), []byte(bashStub), 0o755); err != nil {
		t.Fatalf("write stub bash: %v", err)
	}

	cmd := exec.Command("/bin/bash", "-c", command)
	// INFISICAL_TOKEN is pre-set so the run script skips its login branch.
	cmd.Env = []string{
		"PATH=" + stubDir + ":/usr/bin:/bin",
		"HOME=" + t.TempDir(),
		"INFISICAL_TOKEN=stub-token",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated command failed: %v\ncommand: %s\noutput:\n%s", err, command, out)
	}
	got := strings.TrimSpace(string(out))
	if got != "hi\nbye" {
		t.Fatalf("wrapped command output = %q, want %q\ncommand: %s", got, "hi\nbye", command)
	}
}

func TestEnsureInfisicalLoginSkipsWhenUniversalAuthConfigured(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "client-id")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "client-secret")

	originalRunCapture := runCapture
	originalRunInteractive := runInteractive
	t.Cleanup(func() {
		runCapture = originalRunCapture
		runInteractive = originalRunInteractive
	})
	runCapture = func(_ context.Context, spec run.Spec) (string, error) {
		t.Fatalf("runCapture should not be called when universal auth is configured: %+v", spec)
		return "", nil
	}
	runInteractive = func(_ context.Context, spec run.Spec) error {
		t.Fatalf("runInteractive should not be called when universal auth is configured: %+v", spec)
		return nil
	}

	if err := ensureInfisicalLogin(context.Background(), t.TempDir(), "dev", "/backend"); err != nil {
		t.Fatalf("ensureInfisicalLogin returned %v", err)
	}
}

func TestEnsureInfisicalLoginSkipsWhenTokenPresent(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_TOKEN", "preexisting-token")

	originalRunCapture := runCapture
	originalRunInteractive := runInteractive
	t.Cleanup(func() {
		runCapture = originalRunCapture
		runInteractive = originalRunInteractive
	})
	runCapture = func(_ context.Context, spec run.Spec) (string, error) {
		t.Fatalf("runCapture should not be called when INFISICAL_TOKEN is set: %+v", spec)
		return "", nil
	}
	runInteractive = func(_ context.Context, spec run.Spec) error {
		t.Fatalf("runInteractive should not be called when INFISICAL_TOKEN is set: %+v", spec)
		return nil
	}

	if err := ensureInfisicalLogin(context.Background(), t.TempDir(), "dev", "/backend"); err != nil {
		t.Fatalf("ensureInfisicalLogin returned %v", err)
	}
}

func TestEnsureInfisicalLoginRunsLoginInteractivelyWhenSessionMissing(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_TOKEN", "")
	t.Setenv("INFISICAL_PROJECT_ID", "test-project-id")
	t.Setenv("INFISICAL_API_URL", "https://infisical.example.com/api")

	originalRunCapture := runCapture
	originalRunInteractive := runInteractive
	t.Cleanup(func() {
		runCapture = originalRunCapture
		runInteractive = originalRunInteractive
	})

	repoRoot := t.TempDir()
	probeCalls := 0
	runCapture = func(_ context.Context, spec run.Spec) (string, error) {
		probeCalls++
		if spec.Program != "infisical" {
			t.Fatalf("probe Program = %q, want infisical", spec.Program)
		}
		wantArgs := []string{
			"--silent",
			"--log-level=error",
			"run",
			"--env=dev",
			"--path=/backend",
			"--project-config-dir", repoRoot,
			"--projectId", "test-project-id",
			"--", "true",
		}
		if !reflect.DeepEqual(spec.Args, wantArgs) {
			t.Fatalf("probe Args = %v, want %v", spec.Args, wantArgs)
		}
		if probeCalls < 3 {
			return "You must be logged in to run this command. To login, run [infisical login]\n",
				errors.New("exit status 1")
		}
		return "", nil
	}

	var loginSpec run.Spec
	runInteractive = func(_ context.Context, spec run.Spec) error {
		loginSpec = spec
		return nil
	}

	if err := ensureInfisicalLogin(context.Background(), repoRoot, "dev", "/backend"); err != nil {
		t.Fatalf("ensureInfisicalLogin returned %v", err)
	}
	if loginSpec.Program != "infisical" {
		t.Fatalf("login Program = %q, want infisical", loginSpec.Program)
	}
	wantLoginArgs := []string{"login", "--domain", "https://infisical.example.com/api"}
	if !reflect.DeepEqual(loginSpec.Args, wantLoginArgs) {
		t.Fatalf("login Args = %v, want %v", loginSpec.Args, wantLoginArgs)
	}
	if loginSpec.Dir != repoRoot {
		t.Fatalf("login Dir = %q, want %q", loginSpec.Dir, repoRoot)
	}
	if probeCalls != 3 {
		t.Fatalf("probeCalls = %d, want 3 (pre-lock, post-lock, post-login)", probeCalls)
	}
}

func TestEnsureInfisicalLoginErrorsIfLoginDidNotPersist(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_TOKEN", "")

	originalRunCapture := runCapture
	originalRunInteractive := runInteractive
	t.Cleanup(func() {
		runCapture = originalRunCapture
		runInteractive = originalRunInteractive
	})
	runCapture = func(_ context.Context, _ run.Spec) (string, error) {
		return "You must be logged in to run this command.\n", errors.New("exit status 1")
	}
	runInteractive = func(_ context.Context, _ run.Spec) error {
		return nil
	}

	err := ensureInfisicalLogin(context.Background(), t.TempDir(), "dev", "/backend")
	if err == nil || !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("expected 'did not complete' error, got %v", err)
	}
}

func TestEnsureInfisicalLoginSurfacesNonAuthProbeErrors(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_TOKEN", "")

	originalRunCapture := runCapture
	originalRunInteractive := runInteractive
	t.Cleanup(func() {
		runCapture = originalRunCapture
		runInteractive = originalRunInteractive
	})
	runCapture = func(_ context.Context, _ run.Spec) (string, error) {
		// Simulate a transient API error, NOT an auth failure. The caller
		// should bubble this up rather than route through `infisical login`.
		return "Failed to fetch secrets: connection reset by peer\n",
			errors.New("exit status 1")
	}
	runInteractive = func(_ context.Context, _ run.Spec) error {
		t.Fatalf("infisical login must not run when the probe failure is non-auth")
		return nil
	}

	err := ensureInfisicalLogin(context.Background(), t.TempDir(), "dev", "/backend")
	if err == nil {
		t.Fatal("expected probe error to bubble up")
	}
	if !strings.Contains(err.Error(), "session probe failed") ||
		!strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("expected probe-failure error with original message, got %v", err)
	}
}

func TestSecretsProviderNoneDisablesWrapping(t *testing.T) {
	t.Setenv("OT_SECRETS_PROVIDER", "none")

	spec := run.Spec{
		Program: "uv",
		Args:    []string{"run", "crm-web"},
		Dir:     "/repo/crm",
	}
	got := WrapRunSpec(spec, RunOptions{Environment: "dev", Path: "/crm", ProjectConfigDir: "/repo"})
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("WrapRunSpec() = %+v, want unchanged spec", got)
	}

	command := "cd crm && uv run crm-web"
	if gotCmd := WrapRunShellCommand(command, RunOptions{Environment: "dev", Path: "/crm"}); gotCmd != command {
		t.Fatalf("WrapRunShellCommand() = %q, want unchanged command", gotCmd)
	}

	if err := RunSelfIfNeeded(context.Background(), Options{Paths: []string{"/backend", "/frontend"}}); err != nil {
		t.Fatalf("RunSelfIfNeeded() should be a no-op with secrets disabled, got %v", err)
	}
}
