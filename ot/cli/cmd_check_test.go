package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendCheckHooksIncludeDatadogStaticAnalyzer(t *testing.T) {
	hooks := backendCheckHooks()
	if len(hooks) == 0 || hooks[0] != "datadog-static-analyzer" {
		t.Fatalf("backendCheckHooks() should run Datadog first, got %#v", hooks)
	}
}

func TestMCPHarnessBackendURLUsesEnvAndArgs(t *testing.T) {
	t.Setenv("HARNESS_BACKEND_URL", "http://127.0.0.1:28065/")

	got, ok := mcpHarnessBackendURL(nil)
	if !ok {
		t.Fatal("mcpHarnessBackendURL() skipped backend check")
	}
	if got != "http://127.0.0.1:28065" {
		t.Fatalf("mcpHarnessBackendURL() = %q, want worktree backend URL", got)
	}

	got, ok = mcpHarnessBackendURL([]string{"--backend-url", "http://127.0.0.1:30001"})
	if !ok {
		t.Fatal("mcpHarnessBackendURL(--backend-url) skipped backend check")
	}
	if got != "http://127.0.0.1:30001" {
		t.Fatalf("mcpHarnessBackendURL(--backend-url) = %q", got)
	}

	got, ok = mcpHarnessBackendURL([]string{"--backend-url=http://127.0.0.1:30002/"})
	if !ok {
		t.Fatal("mcpHarnessBackendURL(--backend-url=) skipped backend check")
	}
	if got != "http://127.0.0.1:30002" {
		t.Fatalf("mcpHarnessBackendURL(--backend-url=) = %q", got)
	}
}

func TestMCPHarnessArgsWithFlagsAppendsExplicitTarget(t *testing.T) {
	got := mcpHarnessArgsWithFlags(
		[]string{"--no-browser", "--target", "demo-destination"},
		"demo-outfitter",
		"v1.0.8",
	)
	want := []string{"--no-browser", "--target", "demo-destination", "--target", "demo-outfitter", "--target-version", "v1.0.8"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("mcpHarnessArgsWithFlags() = %#v, want %#v", got, want)
	}
}

func TestCheckCmdRegistersMCPHarnessFlags(t *testing.T) {
	cmd := checkCmd()
	for _, name := range []string{
		"target",
		"target-version",
		"backend-url",
		"mcp-url",
		"openai-model",
		"openai-base-url",
		"host",
		"port",
		"system-prompt-file",
		"no-browser",
		"list-targets",
		"verbose",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Fatalf("check command missing MCP harness flag --%s", name)
		}
	}
}

func TestMCPHarnessArgsWithOptionsAppendsDirectFlags(t *testing.T) {
	got := mcpHarnessArgsWithOptions(
		[]string{"--target", "demo-destination"},
		mcpHarnessOptions{
			Target:           "demo-outfitter",
			TargetVersion:    "v1.0.8",
			BackendURL:       "http://127.0.0.1:52785",
			MCPURL:           "https://example.test/mcp",
			OpenAIModel:      "gpt-test",
			OpenAIBaseURL:    "https://api.example.test/v1",
			Host:             "127.0.0.1",
			Port:             9876,
			SystemPromptFile: "/tmp/prompt.txt",
			NoBrowser:        true,
			ListTargets:      true,
			Verbose:          true,
		},
	)
	want := []string{
		"--target", "demo-destination",
		"--target", "demo-outfitter",
		"--target-version", "v1.0.8",
		"--backend-url", "http://127.0.0.1:52785",
		"--mcp-url", "https://example.test/mcp",
		"--openai-model", "gpt-test",
		"--openai-base-url", "https://api.example.test/v1",
		"--host", "127.0.0.1",
		"--port", "9876",
		"--system-prompt-file", "/tmp/prompt.txt",
		"--no-browser",
		"--list-targets",
		"--verbose",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("mcpHarnessArgsWithOptions() = %#v, want %#v", got, want)
	}
}

func TestMCPHarnessBackendURLSkipsWhenBackendIsUnused(t *testing.T) {
	t.Setenv("HARNESS_BACKEND_URL", "http://127.0.0.1:28065")

	for _, args := range [][]string{
		{"--mcp-url", "http://example.test/mcp"},
		{"--mcp-url=http://example.test/mcp"},
		{"--list-targets"},
	} {
		if got, ok := mcpHarnessBackendURL(args); ok {
			t.Fatalf("mcpHarnessBackendURL(%v) = %q, true; want skip", args, got)
		}
	}
}

func TestMCPHarnessDefaultEnvUsesWorktreeTunnelTargets(t *testing.T) {
	repoRoot := t.TempDir()
	catalogDir := filepath.Join(repoRoot, "backend", "server", "mcp_app")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("create catalog dir: %v", err)
	}
	catalog := `{
  "customers": [
    {
      "slug": "demo-destination",
      "title": "Demo Destination",
      "versions": [
        {"template_version": "v1.0.1", "state": "candidate"}
      ]
    },
    {
      "slug": "demo-outfitter",
      "title": "Demo Outfitter",
      "versions": [
        {"template_version": "v1.0.1", "state": "published"}
      ]
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(catalogDir, "customer_apps.json"), []byte(catalog), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	if err := writeBackendMCPTunnelState(repoRoot, "https://demo-machine.example.ts.net/_ot/abc123/mcp"); err != nil {
		t.Fatalf("write tunnel state: %v", err)
	}

	env := mcpHarnessDefaultEnv(repoRoot, nil)

	if got := env["HARNESS_BACKEND_URL"]; got != "https://demo-machine.example.ts.net/_ot/abc123" {
		t.Fatalf("HARNESS_BACKEND_URL = %q", got)
	}
	if got := env["HARNESS_TARGET"]; got != "root-app" {
		t.Fatalf("HARNESS_TARGET = %q", got)
	}
	targets := env["HARNESS_MCP_TARGETS_JSON"]
	for _, want := range []string{
		`"slug":"root-app"`,
		`"url":"https://demo-machine.example.ts.net/_ot/abc123/mcp"`,
		`"slug":"demo-destination"`,
		`"url":"https://demo-machine.example.ts.net/_ot/abc123/demo-destination/v1.0.1/mcp"`,
		`"slug":"demo-outfitter"`,
	} {
		if !strings.Contains(targets, want) {
			t.Fatalf("HARNESS_MCP_TARGETS_JSON missing %q in %s", want, targets)
		}
	}
}

func TestBackendHealthURLPreservesBasePath(t *testing.T) {
	got, err := backendHealthURL("https://example.test/_ot/worktree?ignored=true")
	if err != nil {
		t.Fatalf("backendHealthURL() returned error: %v", err)
	}
	if got != "https://example.test/_ot/worktree/health" {
		t.Fatalf("backendHealthURL() = %q", got)
	}
}

func TestEnsureMCPBackendReachableChecksHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("request path = %q, want /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := ensureMCPBackendReachable(context.Background(), server.URL); err != nil {
		t.Fatalf("ensureMCPBackendReachable() returned error: %v", err)
	}
}

func TestEnsureMCPBackendReachableReportsRecoveryCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := ensureMCPBackendReachable(context.Background(), server.URL)
	if err == nil {
		t.Fatal("ensureMCPBackendReachable() returned nil, want error")
	}
	message := err.Error()
	for _, want := range []string{
		"MCP backend is not reachable",
		"health check returned HTTP 503",
		"ot up backend",
		"ot check mcp",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error missing %q:\n%s", want, message)
		}
	}
}
