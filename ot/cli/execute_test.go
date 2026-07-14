package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/collinbentley1/cli/ot/bootstrap"
	"github.com/collinbentley1/cli/ot/config"
	"github.com/spf13/cobra"
)

func TestInferInfisicalPaths(t *testing.T) {
	cfg := testConfig()

	testCases := []struct {
		name string
		cmd  *cobra.Command
		want []string
	}{
		{
			name: "up all",
			cmd:  newCommand("up"),
			want: nil,
		},
		{
			name: "up backend",
			cmd:  newCommand("up", "backend"),
			want: []string{"/backend"},
		},
		{
			name: "up backend worker",
			cmd:  newCommand("up", "backend-worker"),
			want: []string{"/backend"},
		},
		{
			name: "up auth",
			cmd:  newCommand("up", "auth"),
			want: []string{"/auth"},
		},
		{
			name: "up frontend",
			cmd:  newCommand("up", "frontend"),
			want: []string{"/frontend"},
		},
		{
			name: "up crm",
			cmd:  newCommand("up", "crm"),
			want: []string{"/crm"},
		},
		{
			name: "up crm mcp stdio",
			cmd:  newUpCommand(t, "crm", "--mcp-stdio"),
			want: []string{"/crm"},
		},
		{
			name: "up db has no env",
			cmd:  newCommand("up", "db"),
			want: nil,
		},
		{
			name: "up frontend-router has no env",
			cmd:  newCommand("up", "frontend-router"),
			want: nil,
		},
		{
			name: "up wordpress has no env",
			cmd:  newCommand("up", "wordpress"),
			want: nil,
		},
		{
			name: "check datadog",
			cmd:  newCommand("check", "datadog"),
			want: []string{"/ot"},
		},
		{
			name: "check all",
			cmd:  newCommand("check"),
			want: nil,
		},
		{
			name: "check backend",
			cmd:  newCommand("check", "backend"),
			want: []string{"/backend"},
		},
		{
			name: "check auth",
			cmd:  newCommand("check", "auth"),
			want: []string{"/auth"},
		},
		{
			name: "check crm",
			cmd:  newCommand("check", "crm"),
			want: []string{"/crm"},
		},
		{
			name: "check frontend",
			cmd:  newCommand("check", "frontend"),
			want: []string{"/frontend"},
		},
		{
			name: "check ot",
			cmd:  newCommand("check", "ot"),
			want: []string{"/ot"},
		},
		{
			name: "check wordpress has no env",
			cmd:  newCommand("check", "wordpress"),
			want: nil,
		},
		{
			name: "datadog hook",
			cmd:  newCommand("datadog-hook"),
			want: []string{"/ot"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := inferInfisicalPaths(testCase.cmd, cfg)
			assertStringSlicesEqual(t, got, testCase.want)
		})
	}
}

func TestInferInfisicalPathsDefersEnvExpansionToEnvLoader(t *testing.T) {
	t.Setenv("OT_TEST_INFISICAL_PATH", "/prefix$SUFFIX")
	t.Setenv("SUFFIX", "mutated")

	cfg := &config.Config{}
	cfg.Infisical.Paths.Check = map[string][]string{
		"datadog": {"$OT_TEST_INFISICAL_PATH"},
	}

	got := inferInfisicalPaths(newCommand("check", "datadog"), cfg)
	want := []string{"$OT_TEST_INFISICAL_PATH"}
	assertStringSlicesEqual(t, got, want)
}

func TestPersistentPreRunArgsSelectCheckTarget(t *testing.T) {
	cfg := testConfig()
	cmd := newCommand("check")

	gotPaths := inferInfisicalPathsForArgs(cmd, cfg, []string{"ot"})
	assertStringSlicesEqual(t, gotPaths, []string{"/ot"})

	opts := bootstrapOptionsForCommandForArgs(cmd, "/tmp/repo", []string{"ot"})
	if opts.Mode != bootstrap.ModeFull {
		t.Fatalf("Mode = %q, want %q", opts.Mode, bootstrap.ModeFull)
	}
	if opts.Target != "ot" {
		t.Fatalf("Target = %q, want ot", opts.Target)
	}
}

func TestPersistentPreRunRootCommandArgsSelectCheckTarget(t *testing.T) {
	cfg := testConfig()
	cmd := newCommand("ot")
	args := []string{"check", "ot"}

	gotPaths := inferInfisicalPathsForArgs(cmd, cfg, args)
	assertStringSlicesEqual(t, gotPaths, []string{"/ot"})

	opts := bootstrapOptionsForCommandForArgs(cmd, "/tmp/repo", args)
	if opts.Mode != bootstrap.ModeFull {
		t.Fatalf("Mode = %q, want %q", opts.Mode, bootstrap.ModeFull)
	}
	if opts.Target != "ot" {
		t.Fatalf("Target = %q, want ot", opts.Target)
	}
}

func TestBootstrapOptionsForUpUseRuntimePreflight(t *testing.T) {
	opts := bootstrapOptionsForCommand(newCommand("up", "frontend"), "/tmp/repo")

	if opts.Mode != bootstrap.ModeRuntime {
		t.Fatalf("Mode = %q, want %q", opts.Mode, bootstrap.ModeRuntime)
	}
	if opts.Target != "frontend" {
		t.Fatalf("Target = %q, want %q", opts.Target, "frontend")
	}
	if !opts.Quiet {
		t.Fatal("expected up bootstrap to be quiet")
	}
}

func TestBootstrapOptionsForUpWithoutServiceTargetAll(t *testing.T) {
	opts := bootstrapOptionsForCommand(newCommand("up"), "/tmp/repo")

	if opts.Mode != bootstrap.ModeRuntime {
		t.Fatalf("Mode = %q, want %q", opts.Mode, bootstrap.ModeRuntime)
	}
	if opts.Target != "all" {
		t.Fatalf("Target = %q, want %q", opts.Target, "all")
	}
}

func TestBootstrapOptionsForCheckRemainFullBootstrap(t *testing.T) {
	opts := bootstrapOptionsForCommand(newCommand("check", "backend"), "/tmp/repo")

	if opts.Mode != bootstrap.ModeFull {
		t.Fatalf("Mode = %q, want %q", opts.Mode, bootstrap.ModeFull)
	}
	if opts.Target != "backend" {
		t.Fatalf("Target = %q, want backend", opts.Target)
	}
}

func TestBootstrapOptionsForCheckWithoutTargetUsesAll(t *testing.T) {
	opts := bootstrapOptionsForCommand(newCommand("check"), "/tmp/repo")

	if opts.Mode != bootstrap.ModeFull {
		t.Fatalf("Mode = %q, want %q", opts.Mode, bootstrap.ModeFull)
	}
	if opts.Target != "all" {
		t.Fatalf("Target = %q, want all", opts.Target)
	}
}

func TestEnsureProcfileDevIncludesBackendWorker(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_ID", "")
	t.Setenv("OT_WORKTREE_ISOLATION_ACTIVE", "")
	rt := &Runtime{RepoRoot: t.TempDir()}

	procfile, err := ensureProcfile(rt, "dev")
	if err != nil {
		t.Fatalf("ensureProcfile() returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.RepoRoot, filepath.FromSlash(procfile)))
	if err != nil {
		t.Fatalf("read procfile: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"backend: OT_BACKEND_SKIP_WORKER=1 .ot/bin/ot up backend",
		"backend-worker: OT_BACKEND_WORKER_SKIP_MIGRATE=1 .ot/bin/ot up backend-worker",
		"backend-mcp-tunnel: .ot/bin/ot backend-mcp-tunnel",
		"auth-api: OT_AUTH_SKIP_MIGRATE=1 .ot/bin/ot up auth",
		"frontend-marketing: infisical --silent --log-level=error run --env=dev --path=/frontend --project-config-dir=. --watch -- bash -lc 'cd frontend && npm run dev --workspace marketing'",
		"frontend-router: .ot/bin/ot up frontend-router",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated dev procfile missing %q:\n%s", want, content)
		}
	}
}

func TestEnsureProcfileBackendRunsWebAndWorker(t *testing.T) {
	rt := &Runtime{RepoRoot: t.TempDir()}

	procfile, err := ensureProcfile(rt, "backend")
	if err != nil {
		t.Fatalf("ensureProcfile() returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.RepoRoot, filepath.FromSlash(procfile)))
	if err != nil {
		t.Fatalf("read procfile: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"backend: OT_BACKEND_SKIP_WORKER=1 .ot/bin/ot up backend",
		"backend-worker: OT_BACKEND_WORKER_SKIP_MIGRATE=1 .ot/bin/ot up backend-worker",
		"backend-mcp-tunnel: .ot/bin/ot backend-mcp-tunnel",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated backend procfile missing %q:\n%s", want, content)
		}
	}
}

func TestBackendNotRunningError(t *testing.T) {
	if !backendNotRunningError(errors.New("backend not running")) {
		t.Fatalf("backendNotRunningError() should accept not-running errors")
	}
	if backendNotRunningError(errors.New("port 8005 already in use by another process")) {
		t.Fatalf("backendNotRunningError() should reject unrelated errors")
	}
	if backendNotRunningError(nil) {
		t.Fatalf("backendNotRunningError() should reject nil")
	}
}

func TestNormalizeTailscaleDomainSupportsPerEngineerMachines(t *testing.T) {
	tests := map[string]string{
		"demo-machine.example.ts.net.":           "demo-machine.example.ts.net",
		"demo-machine-two.example.ts.net":        "demo-machine-two.example.ts.net",
		"  demo-machine-three.example.ts.net.  ": "demo-machine-three.example.ts.net",
	}

	for input, want := range tests {
		if got := normalizeTailscaleDomain(input); got != want {
			t.Fatalf("normalizeTailscaleDomain(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFindTailscaleFunnelEnableURL(t *testing.T) {
	output := `
Funnel is not enabled on your tailnet.
To enable, visit:

         https://login.tailscale.com/f/funnel?node=nExampleNode
`

	got := findTailscaleFunnelEnableURL(output)
	want := "https://login.tailscale.com/f/funnel?node=nExampleNode"
	if got != want {
		t.Fatalf("findTailscaleFunnelEnableURL() = %q, want %q", got, want)
	}
}

func TestEnsureProcfileFrontendUsesInfisicalWatch(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_ID", "")
	t.Setenv("OT_WORKTREE_ISOLATION_ACTIVE", "")
	rt := &Runtime{RepoRoot: t.TempDir()}

	procfile, err := ensureProcfile(rt, "frontend")
	if err != nil {
		t.Fatalf("ensureProcfile() returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.RepoRoot, filepath.FromSlash(procfile)))
	if err != nil {
		t.Fatalf("read procfile: %v", err)
	}
	content := string(data)
	want := "frontend: infisical --silent --log-level=error run --env=dev --path=/frontend --project-config-dir=. --watch -- bash -lc 'cd frontend && npm run dev:all'"
	if !strings.Contains(content, want) {
		t.Fatalf("generated frontend procfile missing %q:\n%s", want, content)
	}
	if !strings.Contains(content, "frontend-router: .ot/bin/ot up frontend-router") {
		t.Fatalf("generated frontend procfile missing repo-local router command:\n%s", content)
	}
}

func TestEnsureProcfileCRMUsesInfisicalWatch(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_ID", "")
	t.Setenv("OT_WORKTREE_ISOLATION_ACTIVE", "")
	rt := &Runtime{RepoRoot: t.TempDir()}

	procfile, err := ensureProcfile(rt, "crm")
	if err != nil {
		t.Fatalf("ensureProcfile() returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.RepoRoot, filepath.FromSlash(procfile)))
	if err != nil {
		t.Fatalf("read procfile: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"crm-web: infisical --silent --log-level=error run --env=dev --path=/crm --project-config-dir=. --watch -- bash -lc 'cd crm && uv run crm-web'",
		"crm-worker: infisical --silent --log-level=error run --env=dev --path=/crm --project-config-dir=. --watch -- bash -lc 'cd crm && uv run crm-worker'",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated CRM procfile missing %q:\n%s", want, content)
		}
	}
}

func TestEnsureProcfileWorktreeExportsLocalOverridesInsideInfisical(t *testing.T) {
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_ID", "")
	t.Setenv("INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET", "")
	t.Setenv("INFISICAL_PROJECT_ID", "")
	t.Setenv("OT_WORKTREE_ISOLATION_ACTIVE", "1")
	t.Setenv("APP_PORT", "24005")
	t.Setenv("NEXT_PUBLIC_API_BASE_URL", "http://127.0.0.1:24005")
	rt := &Runtime{RepoRoot: t.TempDir()}

	procfile, err := ensureProcfile(rt, "frontend")
	if err != nil {
		t.Fatalf("ensureProcfile() returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rt.RepoRoot, filepath.FromSlash(procfile)))
	if err != nil {
		t.Fatalf("read procfile: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"export OT_WORKTREE_ISOLATION_ACTIVE=",
		"export APP_PORT=",
		"24005",
		"NEXT_PUBLIC_API_BASE_URL",
		"cd frontend && npm run dev:all",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated frontend procfile missing %q:\n%s", want, content)
		}
	}
}

func TestFrontendRouterUsesConfiguredPorts(t *testing.T) {
	t.Setenv("MARKETING_PORT", "24101")
	t.Setenv("AUTH_WEB_PORT", "24102")
	t.Setenv("APP_WEB_PORT", "24103")
	t.Setenv("DOCS_PORT", "24108")

	router := newFrontendRouter()

	if got := router.marketing.String(); got != "http://localhost:24101" {
		t.Fatalf("marketing target = %q", got)
	}
	if got := router.auth.String(); got != "http://localhost:24102" {
		t.Fatalf("auth target = %q", got)
	}
	if got := router.app.String(); got != "http://localhost:24103" {
		t.Fatalf("app target = %q", got)
	}
	if got := router.docs.String(); got != "http://localhost:24108" {
		t.Fatalf("docs target = %q", got)
	}
}

func TestResetSkipsBootstrapForRecovery(t *testing.T) {
	if !skipBootstrap["reset"] {
		t.Fatal("expected reset to skip bootstrap")
	}
}

func TestSkipSelfCheckForCRMMCPStdioStart(t *testing.T) {
	cmd := newUpCommand(t, "crm", "--mcp-stdio")

	if !skipSelfCheck(cmd) {
		t.Fatal("expected CRM MCP stdio startup to skip self-check")
	}
}

func TestSkipBootstrapRunForCRMMCPStdioStart(t *testing.T) {
	cmd := newUpCommand(t, "crm", "--mcp-stdio")

	if !skipBootstrapRun(cmd) {
		t.Fatal("expected CRM MCP stdio startup to skip bootstrap")
	}
}

func TestSkipBootstrapRunForCRMStart(t *testing.T) {
	cmd := newUpCommand(t, "crm")

	if !skipBootstrapRun(cmd) {
		t.Fatal("expected CRM startup to skip bootstrap")
	}
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Infisical.Environment = "dev"
	cfg.Infisical.Paths.Up = map[string][]string{
		"crm":            {"/crm"},
		"auth":           {"/auth"},
		"backend":        {"/backend"},
		"backend-worker": {"/backend"},
		"frontend":       {"/frontend"},
	}
	cfg.Infisical.Paths.Check = map[string][]string{
		"auth":     {"/auth"},
		"backend":  {"/backend"},
		"crm":      {"/crm"},
		"datadog":  {"/ot"},
		"frontend": {"/frontend"},
		"ot":       {"/ot"},
	}
	cfg.Infisical.Paths.Hook = map[string][]string{
		"datadog": {"/ot"},
	}
	return cfg
}

func newCommand(use string, args ...string) *cobra.Command {
	cmd := &cobra.Command{Use: use}
	cmd.SetArgs(args)
	_ = cmd.Flags().Parse(args)
	return cmd
}

func newUpCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()

	cmd := upCmd()
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd
}

func assertStringSlicesEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d; got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q; got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}
