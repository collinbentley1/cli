package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/backend"
	"github.com/collinbentley1/cli/ot/config"
	crmservice "github.com/collinbentley1/cli/ot/crm"
	dotenv "github.com/collinbentley1/cli/ot/env"
	"github.com/collinbentley1/cli/ot/localdev"
	"github.com/collinbentley1/cli/ot/overmind"
	"github.com/collinbentley1/cli/ot/porter"
	"github.com/collinbentley1/cli/ot/providers"
	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
)

func upCmd() *cobra.Command {
	var envFlag string
	var mcpStdio bool
	var wordpressReset bool

	cmd := &cobra.Command{
		Use:   "up [service]",
		Short: "Start dev stack or individual services",
		Long: `Start the dev stack or individual services.

Without arguments, starts the full dev stack (backend web + worker + auth API + frontend).
With a service name, runs that service or local service stack.

Services:
  auth            Run shared auth service
  backend         Run backend web + worker stack (background). Bootstraps the
                  WordPress fixture site first when it isn't already running
                  so the backend picks up its credentials on startup. Set
                  OT_BACKEND_SKIP_WORDPRESS=1 to skip the bootstrap.
  backend-worker  Run backend worker only (foreground)
  crm             Run CRM web + worker stack (background)
  frontend        Run frontend dev servers + local path router
  wordpress       Run the project-provided local WordPress fixture site
  db              Start Porter DB tunnels (background)

Examples:
  ot up                 # full dev stack (background)
  ot up auth            # just shared auth service (foreground)
  ot up backend         # backend web + worker (background)
  ot up backend-worker  # just backend worker (foreground)
  ot up db              # DB tunnels (background)
  ot up wordpress       # local WordPress fixture site
  ot up frontend        # start frontends + local path router`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			switch args[0] {
			case "auth", "backend", "backend-worker", "crm", "frontend", "wordpress", "db", "frontend-router":
				if args[0] != "frontend-router" && len(args) > 1 {
					return fmt.Errorf("unknown service: %s (valid: auth, backend, backend-worker, crm, frontend, wordpress, frontend-router, db)", strings.Join(args, " "))
				}
				return nil
			default:
				return fmt.Errorf("unknown service: %s (valid: auth, backend, backend-worker, crm, frontend, wordpress, frontend-router, db)", args[0])
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			if len(args) == 0 {
				return upDevStack(cmd, rt)
			}

			switch args[0] {
			case "auth":
				return upAuth(cmd, rt)
			case "backend":
				return upBackend(cmd, rt)
			case "backend-worker":
				return upBackendWorker(cmd, rt)
			case "crm":
				return upCRM(cmd, rt, mcpStdio)
			case "frontend":
				return upFrontend(cmd, rt)
			case "wordpress":
				return upWordpress(cmd, rt, wordpressReset)
			case "db":
				return upDB(cmd, rt, envFlag)
			case "frontend-router":
				return runFrontendRouter(cmd.Context())
			default:
				return fmt.Errorf("unknown service: %s (valid: auth, backend, backend-worker, crm, frontend, wordpress, frontend-router, db)", args[0])
			}
		},
	}

	cmd.Flags().StringVar(&envFlag, "env", "", "Target environment for DB tunnels: nonprod (default)")
	cmd.Flags().BoolVar(
		&mcpStdio,
		"mcp-stdio",
		false,
		"Run CRM as a stdio MCP server for local MCP clients like Codex",
	)
	cmd.Flags().BoolVar(
		&wordpressReset,
		"reset",
		false,
		"Destroy and rehydrate the local WordPress fixture site before starting",
	)
	return cmd
}

func upDevStack(cmd *cobra.Command, rt *Runtime) error {
	if err := ensureSelfBinary(cmd.Context(), rt.RepoRoot); err != nil {
		return err
	}
	if isDevStackRunning(rt) {
		fmt.Println("Stopping existing dev stack...")
		if err := downDevStack(cmd, rt); err != nil {
			return err
		}
	}

	st, err := overmind.Stack(rt.Config, "dev")
	if err != nil {
		return err
	}
	if st.Procfile == "" {
		procfile, err := ensureProcfile(rt, "dev")
		if err != nil {
			return err
		}
		st.Procfile = procfile
	}

	fmt.Println("Starting dev stack (backend + worker + auth API + frontend)...")
	fmt.Println()
	_ = clearBackendMCPTunnelState(rt.RepoRoot)

	if err := overmind.EnsureStartedSilent(
		cmd.Context(),
		rt.RepoRoot,
		st,
		backendStackExtraEnv(cmd.Context(), rt.RepoRoot),
	); err != nil {
		return err
	}

	fmt.Println("Dev stack started:")
	fmt.Printf("  backend:   http://localhost:%s\n", getenvDefault("APP_PORT", "8005"))
	printBackendMCPTunnelStartupHint()
	fmt.Printf("  auth API:  http://localhost:%s\n", getenvDefault("AUTH_PORT", "8004"))
	fmt.Printf("  marketing: http://localhost:%s\n", getenvDefault("MARKETING_PORT", "3005"))
	fmt.Printf("  auth web:  http://localhost:%s\n", getenvDefault("AUTH_WEB_PORT", "3006"))
	fmt.Printf("  app:       http://localhost:%s\n", getenvDefault("APP_WEB_PORT", "3007"))
	fmt.Printf("  docs:      http://localhost:%s\n", getenvDefault("DOCS_PORT", "3008"))
	fmt.Printf("  router (use this): http://localhost:%s\n", getenvDefault("LOCAL_ROUTER_PORT", "3000"))
	fmt.Println()
	fmt.Println("Run 'ot logs' to stream all logs, 'ot logs backend' for backend web + worker.")
	fmt.Println("Run 'ot down' to stop.")
	return nil
}

func upBackend(cmd *cobra.Command, rt *Runtime) error {
	if shouldBootstrapWordpressForBackend() {
		if err := ensureWordpressForBackend(cmd, rt); err != nil {
			return err
		}
	}
	if strings.TrimSpace(os.Getenv("OT_BACKEND_SKIP_WORKER")) == "" {
		return upBackendStack(cmd, rt)
	}
	if shouldStopDevProcess() && stopDevProcess(cmd.Context(), rt, "backend") {
		fmt.Println("Stopping existing backend process...")
	}
	fmt.Println("Starting backend...")
	force := strings.EqualFold(os.Getenv("FORCE_RESTART"), "1")
	return backend.Start(cmd.Context(), backendOptions(rt, force))
}

func shouldBootstrapWordpressForBackend() bool {
	skip := strings.TrimSpace(os.Getenv("OT_BACKEND_SKIP_WORDPRESS"))
	return skip == "" || skip == "0" || strings.EqualFold(skip, "false")
}

// ensureWordpressForBackend brings up the WordPress fixture site before the
// backend stack so the backend's launch wrapper can source the credentials
// from `.ot/wordpress/backend-env.sh`. Skipped when both the env file and the
// WP port are already healthy, or when OT_BACKEND_SKIP_WORDPRESS is set.
func ensureWordpressForBackend(cmd *cobra.Command, rt *Runtime) error {
	envPath := filepath.Join(rt.RepoRoot, ".ot", "wordpress", "backend-env.sh")
	envExists := false
	if info, err := os.Stat(envPath); err == nil && !info.IsDir() {
		envExists = true
	}
	port := wordpressPortForRepo(rt.RepoRoot)
	alive := wordpressFixtureAlive(cmd.Context(), port)
	if envExists && alive {
		return nil
	}
	switch {
	case !envExists && !alive:
		fmt.Println("WordPress fixture is not running — starting it before backend...")
	case !envExists:
		fmt.Println("WordPress backend env not yet written — running WordPress bootstrap...")
	default:
		fmt.Println("WordPress fixture port is not responding — restarting it before backend...")
	}
	return upWordpress(cmd, rt, false)
}

func wordpressPortForRepo(repoRoot string) int {
	if profile, err := localdev.Ensure(repoRoot); err == nil && profile != nil && profile.Ports.WordPress > 0 {
		return profile.Ports.WordPress
	}
	if envPort := strings.TrimSpace(os.Getenv("WP_ENV_PORT")); envPort != "" {
		if port, err := strconv.Atoi(envPort); err == nil && port > 0 {
			return port
		}
	}
	return 8888
}

func wordpressFixtureAlive(ctx context.Context, port int) bool {
	if port <= 0 {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/wp-json/", port)
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	// Any HTTP response (200 success, 401 auth required) means wp-env is up.
	return resp.StatusCode >= 200 && resp.StatusCode < 600
}

func upBackendStack(cmd *cobra.Command, rt *Runtime) error {
	if err := ensureSelfBinary(cmd.Context(), rt.RepoRoot); err != nil {
		return err
	}
	if err := backend.Prepare(cmd.Context(), backendOptions(rt, false)); err != nil {
		return err
	}
	st, socket, err := stackWithProcfile(rt, "backend")
	if err != nil {
		return err
	}
	if shouldStopDevProcess() && overmind.SocketAlive(socket) {
		fmt.Println("Stopping existing backend stack...")
		if err := overmind.Quit(cmd.Context(), rt.RepoRoot, st); err != nil {
			return err
		}
	}
	if shouldStopDevProcess() {
		_ = stopDevProcess(cmd.Context(), rt, "backend-worker")
		_ = stopDevProcess(cmd.Context(), rt, "backend")
		if err := backend.Stop(cmd.Context(), rt.RepoRoot); err == nil {
			fmt.Println("Stopping existing backend process...")
		} else if !backendNotRunningError(err) {
			return err
		}
	}
	fmt.Println("Starting backend web + worker...")
	_ = clearBackendMCPTunnelState(rt.RepoRoot)
	if err := overmind.EnsureStartedSilent(
		cmd.Context(),
		rt.RepoRoot,
		st,
		backendStackExtraEnv(cmd.Context(), rt.RepoRoot),
	); err != nil {
		return err
	}
	if err := waitForBackendStackReady(cmd.Context(), socket); err != nil {
		if message, readErr := readBackendMCPTunnelError(rt.RepoRoot); readErr == nil && message != "" {
			return fmt.Errorf("backend MCP tunnel failed: %s", message)
		}
		return err
	}
	tunnelURL := ""
	if !backendMCPTunnelDisabled() {
		var err error
		tunnelURL, err = waitForBackendMCPTunnelReady(cmd.Context(), rt.RepoRoot, socket, 45*time.Second)
		if err != nil {
			return err
		}
	}
	appPort := getenvDefault("APP_PORT", "8005")
	fmt.Println("Backend started:")
	fmt.Printf("  web:    http://localhost:%s\n", appPort)
	if tunnelURL != "" {
		fmt.Printf("  mcp:       %s\n", tunnelURL)
		printBackendMCPTenantURLs(rt.RepoRoot, tunnelURL)
	} else {
		printBackendMCPTunnelHint(cmd.Context())
	}
	fmt.Println("  worker: backend-worker")
	fmt.Println("Run 'ot logs backend' to stream backend logs, 'ot down backend' to stop.")
	return nil
}

func upBackendWorker(cmd *cobra.Command, rt *Runtime) error {
	fmt.Println("Starting backend worker...")
	return backend.StartWorker(cmd.Context(), backendOptions(rt, false))
}

func upWordpress(cmd *cobra.Command, rt *Runtime, reset bool) error {
	args := []string{"ot/scripts/wordpress_dev.sh", "start"}
	if reset {
		args = append(args, "--reset")
	}
	return run.RunInteractive(cmd.Context(), run.Spec{
		Program: "bash",
		Args:    args,
		Dir:     rt.RepoRoot,
	})
}

func backendNotRunningError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not running")
}

func upAuth(cmd *cobra.Command, rt *Runtime) error {
	fmt.Println("Starting auth...")
	force := strings.EqualFold(os.Getenv("FORCE_RESTART"), "1")
	return backend.StartAuth(cmd.Context(), backendOptions(rt, force))
}

func backendOptions(rt *Runtime, force bool) backend.Options {
	return backend.Options{
		RepoRoot:             rt.RepoRoot,
		Force:                force,
		InfisicalEnvironment: rt.Config.Infisical.Environment,
		InfisicalProjectRoot: rt.RepoRoot,
	}
}

func upCRM(cmd *cobra.Command, rt *Runtime, mcpStdio bool) error {
	if mcpStdio {
		return upCRMMCPStdio(cmd, rt)
	}

	st, socket, err := stackWithProcfile(rt, "crm")
	if err != nil {
		return err
	}
	if overmind.SocketAlive(socket) {
		fmt.Println("Stopping existing CRM stack...")
		if err := overmind.Quit(cmd.Context(), rt.RepoRoot, st); err != nil {
			return err
		}
	}

	if err := crmservice.Prepare(cmd.Context(), crmservice.Options{RepoRoot: rt.RepoRoot}); err != nil {
		return err
	}

	fmt.Println("Starting CRM...")
	if err := overmind.EnsureStartedSilent(cmd.Context(), rt.RepoRoot, st, map[string]string{"OT_SKIP_STOP": "1"}); err != nil {
		return err
	}

	port := strings.TrimSpace(os.Getenv("CRM_PORT"))
	if port == "" {
		port = "8006"
	}
	fmt.Println("CRM started:")
	fmt.Printf("  web: http://localhost:%s\n", port)
	fmt.Printf("  mcp: http://localhost:%s/mcp\n", port)
	fmt.Println()
	fmt.Println("Run 'ot logs crm' to stream CRM logs.")
	fmt.Println("Run 'ot down crm' to stop.")
	return nil
}

func upCRMMCPStdio(cmd *cobra.Command, rt *Runtime) error {
	if err := crmservice.Prepare(cmd.Context(), crmservice.Options{
		RepoRoot: rt.RepoRoot,
		Quiet:    true,
	}); err != nil {
		return err
	}

	return run.RunInteractiveWithSignals(cmd.Context(), dotenv.WrapRunSpec(localdev.WrapSpecWithShellExports(run.Spec{
		Program: "uv",
		Args:    []string{"run", "--no-sync", "crm-mcp-stdio"},
		Dir:     filepath.Join(rt.RepoRoot, "crm"),
		Env: map[string]string{
			"PYTHONUNBUFFERED": "1",
		},
	}), dotenv.RunOptions{
		Environment:      rt.Config.Infisical.Environment,
		Path:             "/crm",
		ProjectConfigDir: rt.RepoRoot,
	}))
}

func upFrontend(cmd *cobra.Command, rt *Runtime) error {
	if err := ensureSelfBinary(cmd.Context(), rt.RepoRoot); err != nil {
		return err
	}
	if shouldStopDevProcess() && stopProcessInStack(cmd.Context(), rt, "dev", "frontend") {
		fmt.Println("Stopping existing dev-stack frontend process...")
	}
	st, err := overmind.Stack(rt.Config, "frontend")
	if err != nil {
		return err
	}
	if st.Procfile == "" {
		procfile, err := ensureProcfile(rt, "frontend")
		if err != nil {
			return err
		}
		st.Procfile = procfile
	}
	socket := filepath.Join(rt.RepoRoot, st.Socket)
	if shouldStopDevProcess() && overmind.SocketAlive(socket) {
		fmt.Println("Stopping existing frontend process...")
		if err := overmind.Quit(cmd.Context(), rt.RepoRoot, st); err != nil {
			return err
		}
	}

	if err := cleanFrontendCaches(rt.RepoRoot); err != nil {
		return err
	}

	fmt.Println("Starting frontend...")
	extraEnv := map[string]string{"OT_SKIP_STOP": "1", "OT_FRONTEND_ROUTER": "1"}
	if err := overmind.EnsureStartedSilent(cmd.Context(), rt.RepoRoot, st, extraEnv); err != nil {
		return err
	}
	fmt.Println("Frontend + router started.")
	fmt.Printf("  marketing: http://localhost:%s\n", getenvDefault("MARKETING_PORT", "3005"))
	fmt.Printf("  auth:      http://localhost:%s\n", getenvDefault("AUTH_WEB_PORT", "3006"))
	fmt.Printf("  app:       http://localhost:%s\n", getenvDefault("APP_WEB_PORT", "3007"))
	fmt.Printf("  docs:      http://localhost:%s\n", getenvDefault("DOCS_PORT", "3008"))
	fmt.Printf("  router (use this): http://localhost:%s\n", getenvDefault("LOCAL_ROUTER_PORT", "3000"))
	fmt.Println("Run 'ot logs frontend' to stream frontend logs.")
	return nil
}

func cleanFrontendCaches(repoRoot string) error {
	fmt.Println("Cleaning frontend build cache...")
	dirs := []string{
		filepath.Join(repoRoot, "frontend", "marketing", ".next"),
		filepath.Join(repoRoot, "frontend", "auth", ".next"),
		filepath.Join(repoRoot, "frontend", "app", ".next"),
		filepath.Join(repoRoot, "frontend", "docs", ".next"),
	}
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
	}
	return nil
}

func upDB(cmd *cobra.Command, rt *Runtime, envFlag string) error {
	if !providers.DBTunnelEnabled() {
		return fmt.Errorf("db tunnel provider is disabled (providers.db_tunnel: none in ot/ot.yaml); enable porter or run your own tunnel")
	}
	if isDBStackRunning(rt) {
		fmt.Println("Stopping existing DB tunnels...")
		_ = downDB(cmd, rt)
	}

	if err := ensureSelfBinary(cmd.Context(), rt.RepoRoot); err != nil {
		return err
	}

	env := "nonprod"
	if envFlag != "" {
		env = envFlag
	}

	st, err := overmind.Stack(rt.Config, "db")
	if err != nil {
		return err
	}
	if st.Procfile == "" {
		procfile, err := ensureProcfile(rt, "db")
		if err != nil {
			return err
		}
		st.Procfile = procfile
	}

	fmt.Printf("Starting DB tunnels (%s environment)...\n", env)
	fmt.Println()

	connDir := filepath.Join(rt.RepoRoot, ".ot", "connections")
	_ = os.RemoveAll(connDir)

	if err := overmind.EnsureStartedSilent(cmd.Context(), rt.RepoRoot, st, map[string]string{"OT_ENV": env}); err != nil {
		return err
	}

	fmt.Println("Waiting for tunnels to connect...")
	socket := filepath.Join(rt.RepoRoot, st.Socket)
	connections, alive := waitForConnectionFiles(rt.RepoRoot, []string{"app"}, 30*time.Second, socket)
	if !alive {
		return fmt.Errorf("DB tunnels failed to start; run 'ot up db' again or 'ot logs db' for details")
	}

	fmt.Println()
	fmt.Println("DB tunnels ready:")
	fmt.Println()
	for _, db := range []string{"app"} {
		if conn, ok := connections[db]; ok {
			fmt.Printf("  %s:\n    %s\n\n", db, conn)
		} else {
			fmt.Printf("  %s: localhost:%d (connecting...)\n\n", db, rt.Config.Porter.Ports[db])
		}
	}
	fmt.Println("Run 'ot logs db' to view tunnel logs, 'ot down db' to stop.")
	return nil
}

func waitForConnectionFiles(repoRoot string, dbs []string, timeout time.Duration, socketPath string) (map[string]string, bool) {
	connDir := filepath.Join(repoRoot, ".ot", "connections")
	deadline := time.Now().Add(timeout)
	result := make(map[string]string)

	for time.Now().Before(deadline) {
		if socketPath != "" && !overmind.SocketAlive(socketPath) {
			return result, false
		}
		allFound := true
		for _, db := range dbs {
			if _, ok := result[db]; ok {
				continue
			}
			connFile := filepath.Join(connDir, db+".txt")
			data, err := os.ReadFile(connFile)
			if err != nil {
				allFound = false
				continue
			}
			result[db] = strings.TrimSpace(string(data))
		}
		if allFound {
			return result, true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return result, true
}

func ensureProcfile(rt *Runtime, stackName string) (string, error) {
	var content string
	switch stackName {
	case "dev":
		content = "backend: OT_BACKEND_SKIP_WORKER=1 .ot/bin/ot up backend\n" +
			"backend-worker: OT_BACKEND_WORKER_SKIP_MIGRATE=1 .ot/bin/ot up backend-worker\n" +
			"backend-mcp-tunnel: .ot/bin/ot backend-mcp-tunnel\n" +
			"auth-api: OT_AUTH_SKIP_MIGRATE=1 .ot/bin/ot up auth\n" +
			"frontend-marketing: " + infisicalProcfileCommand(rt, "/frontend", "cd frontend && npm run dev --workspace marketing", true) + "\n" +
			"frontend-auth: " + infisicalProcfileCommand(rt, "/frontend", "cd frontend && npm run dev --workspace auth", true) + "\n" +
			"frontend-app: " + infisicalProcfileCommand(rt, "/frontend", "cd frontend && npm run dev --workspace app", true) + "\n" +
			"frontend-docs: " + infisicalProcfileCommand(rt, "/frontend", "cd frontend && npm run dev --workspace docs", true) + "\n" +
			"frontend-router: .ot/bin/ot up frontend-router\n"
	case "backend":
		content = "backend: OT_BACKEND_SKIP_WORKER=1 .ot/bin/ot up backend\n" +
			"backend-worker: OT_BACKEND_WORKER_SKIP_MIGRATE=1 .ot/bin/ot up backend-worker\n" +
			"backend-mcp-tunnel: .ot/bin/ot backend-mcp-tunnel\n"
	case "frontend":
		content = "frontend: " + infisicalProcfileCommand(rt, "/frontend", "cd frontend && npm run dev:all", true) + "\n" +
			"frontend-router: .ot/bin/ot up frontend-router\n"
	case "db":
		content = "app: .ot/bin/ot db-connect app\n"
	case "crm":
		content = "crm-web: " + infisicalProcfileCommand(rt, "/crm", "cd crm && uv run crm-web", true) + "\n" +
			"crm-worker: " + infisicalProcfileCommand(rt, "/crm", "cd crm && uv run crm-worker", true) + "\n"
	default:
		return "", fmt.Errorf("unknown stack: %s", stackName)
	}

	procDir := filepath.Join(rt.RepoRoot, ".ot", "overmind")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		return "", err
	}
	procfile := filepath.Join(procDir, stackName+".procfile")
	if err := os.WriteFile(procfile, []byte(content), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(procfile, 0o600); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(".ot", "overmind", stackName+".procfile")), nil
}

func printBackendMCPTunnelHint(ctx context.Context) {
	if reason := backendMCPTunnelDisabledReason(); reason != "" {
		fmt.Printf("  mcp:       %s\n", reason)
		return
	}
	tailscale, err := tailscaleBinary()
	if err != nil {
		fmt.Printf("  mcp:       Tailscale unavailable: %v\n", err)
		return
	}
	domain, err := tailscaleHTTPSDomain(ctx, tailscale)
	if err != nil {
		fmt.Printf("  mcp:       Tailscale URL unavailable: %v\n", err)
		return
	}
	fmt.Printf("  mcp:       %s\n", backendMCPTunnelPublicURL(domain, backendMCPTunnelMountPath()))
}

func printBackendMCPTunnelStartupHint() {
	if reason := backendMCPTunnelDisabledReason(); reason != "" {
		fmt.Printf("  mcp:       %s\n", reason)
		return
	}
	fmt.Println("  mcp:       starting Tailscale Funnel via backend-mcp-tunnel")
}

func backendStackExtraEnv(ctx context.Context, repoRoot string) map[string]string {
	env := map[string]string{"OT_SKIP_STOP": "1"}
	if backendMCPTunnelDisabled() {
		return env
	}
	tailscale, err := tailscaleBinary()
	if err != nil {
		return env
	}
	domain, err := tailscaleHTTPSDomain(ctx, tailscale)
	if err != nil {
		return env
	}
	env["OT_BACKEND_MCP_TUNNEL_HOST"] = domain
	origin := "https://" + domain
	mountPath := backendMCPTunnelMountPath()
	publicMCPURL := backendMCPTunnelPublicURL(domain, mountPath)
	publicAssetBaseURL := strings.TrimSuffix(publicMCPURL, "/mcp")
	env["OT_BACKEND_MCP_TUNNEL_PUBLIC_URL"] = publicMCPURL
	env["MCP_PUBLIC_URL"] = publicMCPURL
	if mountPath != "" {
		env["OT_BACKEND_MCP_TUNNEL_PATH"] = mountPath
	}
	env["OT_BACKEND_MCP_TUNNEL_ASSET_BASE_URL"] = publicAssetBaseURL
	env["OT_WIDGET_STATIC_ASSET_BASE_URL"] = publicAssetBaseURL
	env["OT_WIDGET_RESOURCE_DOMAINS"] = origin

	tenantURLs, err := latestBackendMCPCustomerTenantURLs(repoRoot, publicMCPURL)
	if err != nil {
		return env
	}
	env["OT_BACKEND_MCP_TUNNEL_TENANT_URLS"] = backendMCPTenantURLsEnvValue(tenantURLs)
	for _, tenantURL := range tenantURLs {
		env[backendMCPTenantPublicURLEnvName(tenantURL.Slug)] = tenantURL.URL
	}
	return env
}

func waitForBackendMCPTunnelReady(
	ctx context.Context,
	repoRoot string,
	socket string,
	timeout time.Duration,
) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		url, err := readBackendMCPTunnelState(repoRoot)
		if err == nil && url != "" {
			return url, nil
		}
		if message, err := readBackendMCPTunnelError(repoRoot); err == nil && message != "" {
			return "", fmt.Errorf("backend MCP tunnel failed: %s", message)
		}
		if !overmind.SocketAlive(socket) {
			if message, err := readBackendMCPTunnelError(repoRoot); err == nil && message != "" {
				return "", fmt.Errorf("backend MCP tunnel failed: %s", message)
			}
			return "", fmt.Errorf("backend stack exited before the MCP tunnel became ready; run 'ot logs backend' for details")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("backend MCP tunnel did not become ready within %s; run 'ot logs backend' for details", timeout)
}

func infisicalProcfileCommand(rt *Runtime, path string, command string, watch bool) string {
	environment := ""
	if rt != nil && rt.Config != nil {
		environment = strings.TrimSpace(rt.Config.Infisical.Environment)
	}
	if environment == "" {
		environment = "dev"
	}
	command = localdev.ShellExportPrefixFromEnv() + command
	return dotenv.WrapRunShellCommand(command, dotenv.RunOptions{
		Environment:      environment,
		Path:             path,
		ProjectConfigDir: ".",
		Watch:            watch,
	})
}

func getenvDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func waitForBackendStackReady(ctx context.Context, socket string) error {
	appPort := getenvDefault("APP_PORT", "8005")
	healthURL := fmt.Sprintf("http://127.0.0.1:%s/health", appPort)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error

	for time.Now().Before(deadline) {
		if !overmind.SocketAlive(socket) {
			return fmt.Errorf(
				"backend stack exited before %s became healthy; run 'ot logs backend' for details",
				healthURL,
			)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("GET %s returned %s", healthURL, resp.Status)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}

	if lastErr != nil {
		return fmt.Errorf("backend did not become healthy at %s within 2m: %w", healthURL, lastErr)
	}
	return fmt.Errorf("backend did not become healthy at %s within 2m", healthURL)
}

func shouldStopDevProcess() bool {
	return strings.TrimSpace(os.Getenv("OT_SKIP_STOP")) == ""
}

func stopDevProcess(ctx context.Context, rt *Runtime, name string) bool {
	return stopProcessInStack(ctx, rt, "dev", name)
}

func stopProcessInStack(ctx context.Context, rt *Runtime, stackName, name string) bool {
	_, socket, err := stackWithProcfile(rt, stackName)
	if err != nil {
		return false
	}
	if !overmind.SocketAlive(socket) {
		return false
	}
	if err := run.RunInteractive(ctx, run.Spec{
		Program: "overmind",
		Args:    []string{"stop", "-s", socket, name},
		Dir:     rt.RepoRoot,
		Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
	}); err != nil {
		return false
	}
	return true
}

func stackWithProcfile(rt *Runtime, stackName string) (config.OvermindStack, string, error) {
	st, err := overmind.Stack(rt.Config, stackName)
	if err != nil {
		return config.OvermindStack{}, "", err
	}
	if st.Procfile == "" {
		procfile, err := ensureProcfile(rt, stackName)
		if err != nil {
			return config.OvermindStack{}, "", err
		}
		st.Procfile = procfile
	}
	socket := filepath.Join(rt.RepoRoot, st.Socket)
	return st, socket, nil
}

func isDevStackRunning(rt *Runtime) bool {
	st, err := overmind.Stack(rt.Config, "dev")
	if err != nil {
		return false
	}
	socket := filepath.Join(rt.RepoRoot, st.Socket)
	return overmind.SocketAlive(socket)
}

func isDBStackRunning(rt *Runtime) bool {
	st, err := overmind.Stack(rt.Config, "db")
	if err != nil {
		return false
	}
	socket := filepath.Join(rt.RepoRoot, st.Socket)
	return overmind.SocketAlive(socket)
}

func dbConnectCmd() *cobra.Command {
	var envFlag string

	cmd := &cobra.Command{
		Use:    "db-connect <app>",
		Short:  "Run a single datastore tunnel (internal, used by Procfile)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			which := args[0]

			env := "nonprod"
			if envFlag != "" {
				env = envFlag
			} else if v := os.Getenv("OT_ENV"); v != "" {
				env = v
			}

			return porter.ConnectDatastore(cmd.Context(), rt.RepoRoot, rt.Config, env, which)
		},
	}

	cmd.Flags().StringVar(&envFlag, "env", "", "Target environment: nonprod")

	return cmd
}
