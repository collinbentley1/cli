package backend

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/collinbentley1/cli/ot/run"
)

func TestDefaultInitDBURL(t *testing.T) {
	got := defaultInitDBURL("5433")
	want := "postgresql://postgres:postgres@127.0.0.1:5433/postgres"
	if got != want {
		t.Fatalf("defaultInitDBURL() = %q, want %q", got, want)
	}
}

func TestDefaultAppDBURL(t *testing.T) {
	got := defaultAppDBURL("5433")
	want := "postgresql://postgres:postgres@127.0.0.1:5433/app"
	if got != want {
		t.Fatalf("defaultAppDBURL() = %q, want %q", got, want)
	}
}

func TestDefaultDBImage(t *testing.T) {
	got := defaultDBImage()
	want := "pgvector/pgvector:pg18"
	if got != want {
		t.Fatalf("defaultDBImage() = %q, want %q", got, want)
	}
}

func TestMigrateSpec(t *testing.T) {
	spec := migrateSpec("/tmp/backend", "postgresql://postgres:postgres@127.0.0.1:5433/postgres")
	if spec.Program != "uv" {
		t.Fatalf("migrateSpec().Program = %q, want %q", spec.Program, "uv")
	}
	if spec.Dir != "/tmp/backend" {
		t.Fatalf("migrateSpec().Dir = %q, want %q", spec.Dir, "/tmp/backend")
	}
	wantArgs := []string{"run", "python", "-m", "scripts.migrate"}
	for i, want := range wantArgs {
		if spec.Args[i] != want {
			t.Fatalf("migrateSpec().Args[%d] = %q, want %q", i, spec.Args[i], want)
		}
	}
	if spec.Env["DB_INIT_URL"] != "postgresql://postgres:postgres@127.0.0.1:5433/postgres" {
		t.Fatalf("migrateSpec().Env[DB_INIT_URL] = %q", spec.Env["DB_INIT_URL"])
	}
}

func TestSyncKnowledgeSpec(t *testing.T) {
	spec := syncKnowledgeSpec("/tmp/backend")
	if spec.Program != "uv" {
		t.Fatalf("syncKnowledgeSpec().Program = %q, want %q", spec.Program, "uv")
	}
	if spec.Dir != "/tmp/backend" {
		t.Fatalf("syncKnowledgeSpec().Dir = %q, want %q", spec.Dir, "/tmp/backend")
	}
	wantArgs := []string{"run", "python", "-m", "scripts.sync_knowledge"}
	for i, want := range wantArgs {
		if spec.Args[i] != want {
			t.Fatalf("syncKnowledgeSpec().Args[%d] = %q, want %q", i, spec.Args[i], want)
		}
	}
}

func TestSyncWordpressSpec(t *testing.T) {
	spec := syncWordpressSpec("/tmp/backend")
	if spec.Program != "uv" {
		t.Fatalf("syncWordpressSpec().Program = %q, want %q", spec.Program, "uv")
	}
	if spec.Dir != "/tmp/backend" {
		t.Fatalf("syncWordpressSpec().Dir = %q, want %q", spec.Dir, "/tmp/backend")
	}
	wantArgs := []string{"run", "python", "-m", "scripts.sync_wordpress"}
	for i, want := range wantArgs {
		if spec.Args[i] != want {
			t.Fatalf("syncWordpressSpec().Args[%d] = %q, want %q", i, spec.Args[i], want)
		}
	}
}

func TestSyncOperatorCatalogsSpec(t *testing.T) {
	spec := syncOperatorCatalogsSpec("/tmp/backend")
	if spec.Program != "uv" {
		t.Fatalf("syncOperatorCatalogsSpec().Program = %q, want %q", spec.Program, "uv")
	}
	if spec.Dir != "/tmp/backend" {
		t.Fatalf("syncOperatorCatalogsSpec().Dir = %q, want %q", spec.Dir, "/tmp/backend")
	}
	wantArgs := []string{"run", "python", "-m", "scripts.sync_operator_catalogs"}
	for i, want := range wantArgs {
		if spec.Args[i] != want {
			t.Fatalf("syncOperatorCatalogsSpec().Args[%d] = %q, want %q", i, spec.Args[i], want)
		}
	}
}

func TestWordpressStartupSyncFailureMessage(t *testing.T) {
	message := wordpressStartupSyncFailureMessage(errors.New("sync failed"))

	for _, want := range []string{
		"Operator catalog sync failed",
		"continuing startup",
		"WordPress/booking-vendor sync",
		"sync failed",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("wordpressStartupSyncFailureMessage() missing %q in %q", want, message)
		}
	}
}

func TestCompileMCPAppContractsSpec(t *testing.T) {
	spec := compileMCPAppContractsSpec("/tmp/backend")
	if spec.Program != "uv" {
		t.Fatalf("compileMCPAppContractsSpec().Program = %q, want %q", spec.Program, "uv")
	}
	if spec.Dir != "/tmp/backend" {
		t.Fatalf("compileMCPAppContractsSpec().Dir = %q, want %q", spec.Dir, "/tmp/backend")
	}
	wantArgs := []string{"run", "python", "scripts/compile_mcp_app_contracts.py", "--quiet"}
	for i, want := range wantArgs {
		if spec.Args[i] != want {
			t.Fatalf("compileMCPAppContractsSpec().Args[%d] = %q, want %q", i, spec.Args[i], want)
		}
	}
}

func TestWorkerSpec(t *testing.T) {
	spec := workerSpec("/tmp/backend")
	if spec.Program != "uv" {
		t.Fatalf("workerSpec().Program = %q, want %q", spec.Program, "uv")
	}
	if spec.Dir != "/tmp/backend" {
		t.Fatalf("workerSpec().Dir = %q, want %q", spec.Dir, "/tmp/backend")
	}
	wantArgs := []string{"run", "python", "-m", "server.worker.main"}
	for i, want := range wantArgs {
		if spec.Args[i] != want {
			t.Fatalf("workerSpec().Args[%d] = %q, want %q", i, spec.Args[i], want)
		}
	}
	if spec.Env["PYTHONUNBUFFERED"] != "1" {
		t.Fatalf("workerSpec().Env[PYTHONUNBUFFERED] = %q", spec.Env["PYTHONUNBUFFERED"])
	}
}

func TestServeAuthSpec(t *testing.T) {
	spec := serveAuthSpec("/tmp/backend", "127.0.0.1", "8004")
	if spec.Program != "uv" {
		t.Fatalf("serveAuthSpec().Program = %q, want %q", spec.Program, "uv")
	}
	if spec.Dir != "/tmp/backend" {
		t.Fatalf("serveAuthSpec().Dir = %q, want %q", spec.Dir, "/tmp/backend")
	}
	if spec.Args[2] != authAppModule {
		t.Fatalf("serveAuthSpec().Args[2] = %q, want %q", spec.Args[2], authAppModule)
	}
	if _, ok := spec.Env["OT_BACKEND_COMPILE_MCP_CONTRACTS_ON_STARTUP"]; ok {
		t.Fatalf("serveAuthSpec() should not enable MCP app contract startup compilation")
	}
}

func TestServeSpecCompilesMCPAppContractsOnStartup(t *testing.T) {
	spec := serveSpec("/tmp/backend", "127.0.0.1", "8005")
	if spec.Env["OT_BACKEND_COMPILE_MCP_CONTRACTS_ON_STARTUP"] != "1" {
		t.Fatalf(
			"serveSpec().Env[OT_BACKEND_COMPILE_MCP_CONTRACTS_ON_STARTUP] = %q, want %q",
			spec.Env["OT_BACKEND_COMPILE_MCP_CONTRACTS_ON_STARTUP"],
			"1",
		)
	}
}

func TestServeSpecReloadsGeneratedMCPAppArtifacts(t *testing.T) {
	spec := serveSpec("/tmp/backend", "127.0.0.1", "8005")
	args := strings.Join(spec.Args, "\n")
	for _, want := range []string{
		"'--reload-dir' '/tmp/backend/server'",
		"'--reload-dir' '/tmp/backend/app'",
		"'--reload-include' '*.json'",
		"'--reload-include' '*.html'",
		"'--reload-include' '*.toml'",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("serveSpec args missing %q:\n%q", want, spec.Args)
		}
	}
}

func TestServeSpecAppendsBackendMCPTunnelHostAfterInfisical(t *testing.T) {
	spec := serveSpec("/tmp/backend", "127.0.0.1", "8005")
	args := strings.Join(spec.Args, "\n")
	for _, want := range []string{
		"OT_BACKEND_MCP_TUNNEL_HOST",
		"OT_BACKEND_MCP_TUNNEL_TENANT_URLS",
		"export MCP_ALLOWED_HOSTS",
		"export MCP_PUBLIC_URL OT_WIDGET_STATIC_ASSET_BASE_URL OT_WIDGET_RESOURCE_DOMAINS",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("serveSpec args missing %q:\n%q", want, spec.Args)
		}
	}
}

func TestBackendServeShellCommandNormalizesAllowedHostsAfterInfisical(t *testing.T) {
	command := exec.Command(
		"bash",
		"-lc",
		backendServeShellCommand(run.Spec{
			Program: "bash",
			Args:    []string{"-lc", `printf '%s' "${MCP_ALLOWED_HOSTS}"`},
		}),
	)
	command.Env = append(
		os.Environ(),
		"MCP_ALLOWED_HOSTS=backend.example.com, https://demo-machine.example.ts.net/mcp",
		"OT_BACKEND_MCP_TUNNEL_HOST=demo-machine.example.ts.net.",
	)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("backendServeShellCommand() failed: %v\n%s", err, output)
	}

	got := string(output)
	want := "backend.example.com,demo-machine.example.ts.net"
	if got != want {
		t.Fatalf("MCP_ALLOWED_HOSTS = %q, want %q", got, want)
	}
}

func TestBackendServeShellCommandExportsTunnelPublicURLsAfterInfisical(t *testing.T) {
	command := exec.Command(
		"bash",
		"-lc",
		backendServeShellCommand(run.Spec{
			Program: "bash",
			Args: []string{"-lc", strings.Join([]string{
				`printf '%s\n' "${MCP_PUBLIC_URL}"`,
				`printf '%s\n' "${OT_WIDGET_STATIC_ASSET_BASE_URL}"`,
				`printf '%s\n' "${OT_WIDGET_RESOURCE_DOMAINS}"`,
				`printf '%s\n' "${DEMO_DESTINATION_MCP_PUBLIC_URL}"`,
				`printf '%s\n' "${DEMO_OUTFITTER_MCP_PUBLIC_URL}"`,
			}, "; ")},
		}),
	)
	command.Env = append(
		os.Environ(),
		"MCP_PUBLIC_URL=https://backend.example.com/mcp",
		"OT_WIDGET_STATIC_ASSET_BASE_URL=https://static.example.com",
		"OT_WIDGET_RESOURCE_DOMAINS=https://static.example.com,demo-machine.example.ts.net",
		"DEMO_DESTINATION_MCP_PUBLIC_URL=https://demo-machine-two.example.ts.net/demo-destination/v1.0.1/mcp",
		"OT_BACKEND_MCP_TUNNEL_HOST=demo-machine.example.ts.net.",
		"OT_BACKEND_MCP_TUNNEL_TENANT_URLS=demo-destination=https://demo-machine.example.ts.net/demo-destination/v1.0.1/mcp|demo-outfitter=https://demo-machine.example.ts.net/demo-outfitter/v1.0.1/mcp",
	)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("backendServeShellCommand() failed: %v\n%s", err, output)
	}

	got := strings.Split(strings.TrimSpace(string(output)), "\n")
	want := []string{
		"https://demo-machine.example.ts.net/mcp",
		"https://demo-machine.example.ts.net",
		"https://static.example.com,https://demo-machine.example.ts.net",
		"https://demo-machine.example.ts.net/demo-destination/v1.0.1/mcp",
		"https://demo-machine.example.ts.net/demo-outfitter/v1.0.1/mcp",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tunnel public env output = %#v, want %#v", got, want)
	}
}

func TestDefaultAuthCORSAllowOrigins(t *testing.T) {
	clearLocalPortEnv(t)

	got := defaultAuthCORSAllowOrigins()
	want := "http://localhost:3000,http://127.0.0.1:3000,http://localhost:3006,http://127.0.0.1:3006,http://localhost:8006,http://127.0.0.1:8006"
	if got != want {
		t.Fatalf("defaultAuthCORSAllowOrigins() = %q, want %q", got, want)
	}
}

func TestDefaultAuthCORSAllowOriginsUsesConfiguredPorts(t *testing.T) {
	t.Setenv("LOCAL_ROUTER_PORT", "24000")
	t.Setenv("AUTH_WEB_PORT", "24002")
	t.Setenv("CRM_PORT", "24006")

	got := defaultAuthCORSAllowOrigins()
	want := "http://localhost:24000,http://127.0.0.1:24000,http://localhost:24002,http://127.0.0.1:24002,http://localhost:24006,http://127.0.0.1:24006"
	if got != want {
		t.Fatalf("defaultAuthCORSAllowOrigins() = %q, want %q", got, want)
	}
}

func TestDefaultAuthAllowedReturnToOrigins(t *testing.T) {
	clearLocalPortEnv(t)

	got := defaultAuthAllowedReturnToOrigins()
	want := "http://localhost:3000,http://127.0.0.1:3000,http://localhost:3006,http://127.0.0.1:3006,http://localhost:8006,http://127.0.0.1:8006"
	if got != want {
		t.Fatalf("defaultAuthAllowedReturnToOrigins() = %q, want %q", got, want)
	}
}

func clearLocalPortEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LOCAL_ROUTER_PORT",
		"MARKETING_PORT",
		"AUTH_WEB_PORT",
		"APP_WEB_PORT",
		"DOCS_PORT",
		"CRM_PORT",
	} {
		t.Setenv(key, "")
	}
}

func TestAppendCSVEnvNormalizesAndDeduplicatesHosts(t *testing.T) {
	t.Setenv("MCP_ALLOWED_HOSTS", "backend.example.com, https://demo-machine.example.ts.net/mcp")

	appendCSVEnv(
		"MCP_ALLOWED_HOSTS",
		"demo-machine.example.ts.net",
		"http://demo-machine-two.example.ts.net/mcp",
	)

	got := strings.Split(os.Getenv("MCP_ALLOWED_HOSTS"), ",")
	want := []string{
		"backend.example.com",
		"demo-machine.example.ts.net",
		"demo-machine-two.example.ts.net",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("MCP_ALLOWED_HOSTS = %#v, want %#v", got, want)
	}
}
