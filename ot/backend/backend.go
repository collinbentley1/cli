package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/dbinit"
	dotenv "github.com/collinbentley1/cli/ot/env"
	"github.com/collinbentley1/cli/ot/localdev"
	"github.com/collinbentley1/cli/ot/run"
	"golang.org/x/sys/unix"
)

type Options struct {
	RepoRoot             string
	Force                bool
	InfisicalEnvironment string
	InfisicalProjectRoot string
}

const (
	backendAppModule = "server.main:app"
	authAppModule    = "server.auth_main:app"
)

func Start(ctx context.Context, opts Options) error {
	backendDir, initDBURL, err := prepareRuntime(ctx, opts, "backend")
	if err != nil {
		return err
	}
	if err := run.RunInteractive(ctx, localBackendRuntimeSpec(compileMCPAppContractsSpec(backendDir), opts.RepoRoot)); err != nil {
		return fmt.Errorf("compile MCP app contracts: %w", err)
	}
	if err := run.RunInteractive(ctx, localBackendRuntimeSpec(migrateSpec(backendDir, initDBURL), opts.RepoRoot)); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	if err := run.RunInteractive(ctx, localBackendRuntimeSpec(syncKnowledgeSpec(backendDir), opts.RepoRoot)); err != nil {
		return fmt.Errorf("sync operator knowledge: %w", err)
	}
	if err := run.RunInteractive(ctx, localBackendRuntimeSpec(syncWeatherSpec(backendDir), opts.RepoRoot)); err != nil {
		return fmt.Errorf("sync operator weather: %w", err)
	}
	runStartupWordpressSync(ctx, backendDir, opts.RepoRoot)

	appHost := getenvDefault("APP_HOST", "127.0.0.1")
	appPort := getenvDefault("APP_PORT", "8005")

	setDefaultEnv("CORS_ALLOW_ORIGINS", defaultBackendCORSAllowOrigins())
	appendCSVEnv("MCP_ALLOWED_HOSTS", os.Getenv("OT_BACKEND_MCP_TUNNEL_HOST"))

	alreadyRunning, err := maybeRestart(ctx, appPort, backendAppModule, "Backend", opts.Force)
	if err != nil {
		return err
	}
	if alreadyRunning {
		return nil
	}

	return run.RunInteractive(ctx, wrapBackendInfisical(localdev.WrapSpecWithShellExports(serveSpec(backendDir, appHost, appPort)), opts, true))
}

func Prepare(ctx context.Context, opts Options) error {
	_, _, err := prepareRuntime(ctx, opts, "backend")
	return err
}

func StartWorker(ctx context.Context, opts Options) error {
	backendDir, initDBURL, err := prepareRuntime(ctx, opts, "backend-worker")
	if err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("OT_BACKEND_WORKER_SKIP_MIGRATE")) == "" {
		if err := run.RunInteractive(ctx, localBackendRuntimeSpec(migrateSpec(backendDir, initDBURL), opts.RepoRoot)); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}
	return run.RunInteractiveWithSignals(ctx, wrapBackendInfisical(localdev.WrapSpecWithShellExports(workerSpec(backendDir)), opts, true))
}

func StartAuth(ctx context.Context, opts Options) error {
	backendDir := filepath.Join(opts.RepoRoot, "backend")
	hostPort := getenvDefault("APP_DB_PORT", "5433")
	initDBURL := strings.TrimSpace(os.Getenv("LOCAL_PRIMARY_APP_PGSQL_INIT_DB_URL"))
	if initDBURL == "" {
		fmt.Println("[auth] LOCAL_PRIMARY_APP_PGSQL_INIT_DB_URL not set; using local default.")
		initDBURL = defaultInitDBURL(hostPort)
	}
	appDBURL := strings.TrimSpace(os.Getenv("LOCAL_PRIMARY_APP_PGSQL_DB_URL"))
	if appDBURL == "" {
		fmt.Println("[auth] LOCAL_PRIMARY_APP_PGSQL_DB_URL not set; using local default.")
		appDBURL = defaultAppDBURL(hostPort)
	}
	_, err := ensureDatabaseSerialized(ctx, opts.RepoRoot, initDBURL, appDBURL)
	if err != nil {
		return err
	}
	if err := os.Setenv("DB_URL", appDBURL); err != nil {
		return fmt.Errorf("set DB_URL: %w", err)
	}
	if strings.TrimSpace(os.Getenv("OT_AUTH_SKIP_MIGRATE")) == "" {
		if err := run.RunInteractive(ctx, migrateSpec(backendDir, initDBURL)); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}

	appHost := getenvDefault("AUTH_HOST", "127.0.0.1")
	appPort := getenvDefault("AUTH_PORT", "8004")

	setDefaultEnv("CORS_ALLOW_ORIGINS", defaultAuthCORSAllowOrigins())
	setDefaultEnv("FRONTEND_BASE_URL", defaultAuthFrontendBaseURL())
	setDefaultEnv("AUTH_ALLOWED_RETURN_TO_ORIGINS", defaultAuthAllowedReturnToOrigins())
	setDefaultEnv(
		"WORKOS_REDIRECT_URI",
		fmt.Sprintf("http://127.0.0.1:%s/api/auth/oauth/callback", appPort),
	)
	setDefaultEnv("AUTH_COOKIE_SECURE", "false")

	alreadyRunning, err := maybeRestart(ctx, appPort, authAppModule, "Auth", opts.Force)
	if err != nil {
		return err
	}
	if alreadyRunning {
		return nil
	}

	return run.RunInteractive(ctx, wrapAuthInfisical(localdev.WrapSpecWithShellExports(serveAuthSpec(backendDir, appHost, appPort)), opts, true))
}

func prepareRuntime(ctx context.Context, opts Options, serviceName string) (string, string, error) {
	backendDir := filepath.Join(opts.RepoRoot, "backend")
	hostPort := getenvDefault("APP_DB_PORT", "5433")
	initDBURL := strings.TrimSpace(os.Getenv("LOCAL_PRIMARY_APP_PGSQL_INIT_DB_URL"))
	if initDBURL == "" {
		fmt.Printf("[%s] LOCAL_PRIMARY_APP_PGSQL_INIT_DB_URL not set; using local default.\n", serviceName)
		initDBURL = defaultInitDBURL(hostPort)
	}
	appDBURL := strings.TrimSpace(os.Getenv("LOCAL_PRIMARY_APP_PGSQL_DB_URL"))
	if appDBURL == "" {
		fmt.Printf("[%s] LOCAL_PRIMARY_APP_PGSQL_DB_URL not set; using local default.\n", serviceName)
		appDBURL = defaultAppDBURL(hostPort)
	}
	if _, err := ensureDatabaseSerialized(ctx, opts.RepoRoot, initDBURL, appDBURL); err != nil {
		return "", "", err
	}
	if err := os.Setenv("DB_URL", appDBURL); err != nil {
		return "", "", fmt.Errorf("set DB_URL: %w", err)
	}
	return backendDir, initDBURL, nil
}

func Stop(ctx context.Context, repoRoot string) error {
	port := getenvDefault("APP_PORT", "8005")
	return stopService(ctx, repoRoot, port, backendAppModule, "backend")
}

func StopAuth(ctx context.Context, repoRoot string) error {
	port := getenvDefault("AUTH_PORT", "8004")
	return stopService(ctx, repoRoot, port, authAppModule, "auth")
}

func ensureDatabaseSerialized(ctx context.Context, repoRoot string, initDBURL string, appDBURL string) (string, error) {
	lockPath := filepath.Join(repoRoot, ".ot", "locks", "backend-db-init.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return "", fmt.Errorf("create db-init lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return "", fmt.Errorf("open db-init lock: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return "", fmt.Errorf("flock db-init lock: %w", err)
	}
	defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()
	return ensureDatabase(ctx, repoRoot, initDBURL, appDBURL)
}

func ensureDatabase(ctx context.Context, repoRoot string, initDBURL string, appDBURL string) (string, error) {
	name := getenvDefault("APP_DB_CONTAINER", "local-primary-app-pgsql")
	hostPort := getenvDefault("APP_DB_PORT", "5433")
	image := getenvDefault("APP_DB_IMAGE", defaultDBImage())
	if err := dbinit.RequireExplicitPort(initDBURL, "LOCAL_PRIMARY_APP_PGSQL_INIT_DB_URL"); err != nil {
		return "", err
	}
	if err := dbinit.RequireExplicitPort(appDBURL, "LOCAL_PRIMARY_APP_PGSQL_DB_URL"); err != nil {
		return "", err
	}
	initCfg, err := dbinit.ParseURL(initDBURL, hostPort)
	if err != nil {
		return "", err
	}
	appCfg, err := dbinit.ParseURL(appDBURL, hostPort)
	if err != nil {
		return "", err
	}
	hostPort = initCfg.Port

	existed, err := dbinit.EnsureContainer(ctx, repoRoot, name, image, hostPort, initCfg, false)
	if err != nil {
		return "", err
	}
	if existed {
		if err := dbinit.EnsureRdsRolesAndDb(ctx, name, initCfg, appCfg, false); err != nil {
			return "", err
		}
		if err := requireVectorExtension(ctx, name, image, initCfg); err != nil {
			return "", err
		}
		return hostPort, nil
	}

	if err := dbinit.WaitForPostgres(ctx, name, initCfg.User, initCfg.Database, initCfg.Port); err != nil {
		return "", err
	}

	if err := dbinit.EnsureRdsRolesAndDb(ctx, name, initCfg, appCfg, false); err != nil {
		return "", err
	}
	if err := requireVectorExtension(ctx, name, image, initCfg); err != nil {
		return "", err
	}
	return hostPort, nil
}

func setDefaultEnv(key, value string) {
	if strings.TrimSpace(os.Getenv(key)) == "" {
		_ = os.Setenv(key, value)
	}
}

func appendCSVEnv(key string, values ...string) {
	seen := map[string]bool{}
	merged := make([]string, 0, len(values)+1)
	for _, entry := range strings.Split(os.Getenv(key), ",") {
		normalized := normalizeHostEnv(entry)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		merged = append(merged, normalized)
	}
	for _, value := range values {
		normalized := normalizeHostEnv(value)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		merged = append(merged, normalized)
	}
	if len(merged) > 0 {
		_ = os.Setenv(key, strings.Join(merged, ","))
	}
}

func normalizeHostEnv(value string) string {
	candidate := strings.TrimSpace(value)
	candidate = strings.TrimPrefix(candidate, "https://")
	candidate = strings.TrimPrefix(candidate, "http://")
	if slash := strings.Index(candidate, "/"); slash >= 0 {
		candidate = candidate[:slash]
	}
	return strings.TrimSpace(candidate)
}

func migrateSpec(backendDir string, initDBURL string) run.Spec {
	return run.Spec{
		Program: "uv",
		Args:    []string{"run", "python", "-m", "scripts.migrate"},
		Dir:     backendDir,
		Env: map[string]string{
			"DB_INIT_URL": initDBURL,
		},
	}
}

func syncKnowledgeSpec(backendDir string) run.Spec {
	return run.Spec{
		Program: "uv",
		Args:    []string{"run", "python", "-m", "scripts.sync_knowledge"},
		Dir:     backendDir,
	}
}

func syncWeatherSpec(backendDir string) run.Spec {
	return run.Spec{
		Program: "uv",
		Args:    []string{"run", "python", "-m", "scripts.sync_weather"},
		Dir:     backendDir,
	}
}

func syncWordpressSpec(backendDir string) run.Spec {
	return run.Spec{
		Program: "uv",
		Args:    []string{"run", "python", "-m", "scripts.sync_wordpress"},
		Dir:     backendDir,
	}
}

func syncOperatorCatalogsSpec(backendDir string) run.Spec {
	return run.Spec{
		Program: "uv",
		Args:    []string{"run", "python", "-m", "scripts.sync_operator_catalogs"},
		Dir:     backendDir,
	}
}

func runStartupWordpressSync(ctx context.Context, backendDir string, repoRoot string) {
	if err := run.RunInteractive(ctx, localBackendRuntimeSpec(syncOperatorCatalogsSpec(backendDir), repoRoot)); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", wordpressStartupSyncFailureMessage(err))
	}
}

func wordpressStartupSyncFailureMessage(err error) string {
	return fmt.Sprintf(
		"[backend] Operator catalog sync failed; continuing startup. "+
			"The backend worker will retry the WordPress/booking-vendor sync when it is running. Error: %v",
		err,
	)
}

func compileMCPAppContractsSpec(backendDir string) run.Spec {
	return run.Spec{
		Program: "uv",
		Args:    []string{"run", "python", "scripts/compile_mcp_app_contracts.py", "--quiet"},
		Dir:     backendDir,
	}
}

func workerSpec(backendDir string) run.Spec {
	return run.Spec{
		Program: "uv",
		Args:    []string{"run", "python", "-m", "server.worker.main"},
		Dir:     backendDir,
		Env: map[string]string{
			"PYTHONUNBUFFERED": "1",
		},
	}
}

func serveSpec(backendDir string, appHost string, appPort string) run.Spec {
	appSpec := serveAppSpec(backendDir, appHost, appPort, backendAppModule)
	return run.Spec{
		Program: "bash",
		Args:    []string{"-lc", backendServeShellCommand(appSpec)},
		Dir:     backendDir,
		Env: map[string]string{
			"OT_BACKEND_COMPILE_MCP_CONTRACTS_ON_STARTUP": "1",
		},
	}
}

func serveAuthSpec(backendDir string, appHost string, appPort string) run.Spec {
	return serveAppSpec(backendDir, appHost, appPort, authAppModule)
}

func wrapBackendInfisical(spec run.Spec, opts Options, watch bool) run.Spec {
	spec = applyLocalWordpressEnvOverlay(spec, opts.RepoRoot)
	return wrapInfisical(spec, opts, "/backend", watch)
}

func localBackendRuntimeSpec(spec run.Spec, repoRoot string) run.Spec {
	return applyLocalWordpressEnvOverlay(localdev.WrapSpecWithShellExports(spec), repoRoot)
}

// applyLocalWordpressEnvOverlay wraps `spec` with a bash shim that sources
// `.ot/wordpress/backend-env.sh` (when present) before exec'ing the original
// command. The project-provided WordPress fixture script (`ot up wordpress`)
// writes that file with the fixture-site credentials, which change every time
// wp-env is reset. The secrets provider's `dev` environment may also carry
// those names but with stale fixture values; the locally-generated values
// must win on this developer's machine.
//
// In nonprod/prod the file does not exist, so the shim is a no-op and the
// spec runs unchanged.
func applyLocalWordpressEnvOverlay(spec run.Spec, repoRoot string) run.Spec {
	envFile := filepath.Join(repoRoot, ".ot", "wordpress", "backend-env.sh")
	quoted := append([]string{shellSingleQuote(spec.Program)}, shellSingleQuoteAll(spec.Args)...)
	script := fmt.Sprintf(
		"if [ -f %s ]; then set -a; . %s; set +a; fi; exec %s",
		shellSingleQuote(envFile),
		shellSingleQuote(envFile),
		strings.Join(quoted, " "),
	)
	return run.Spec{
		Program: "bash",
		Args:    []string{"-lc", script},
		Dir:     spec.Dir,
		Env:     spec.Env,
	}
}

func shellSingleQuoteAll(items []string) []string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, shellSingleQuote(item))
	}
	return quoted
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func wrapAuthInfisical(spec run.Spec, opts Options, watch bool) run.Spec {
	return wrapInfisical(spec, opts, "/auth", watch)
}

func wrapInfisical(spec run.Spec, opts Options, path string, watch bool) run.Spec {
	projectRoot := strings.TrimSpace(opts.InfisicalProjectRoot)
	if projectRoot == "" {
		projectRoot = opts.RepoRoot
	}
	return dotenv.WrapRunSpec(spec, dotenv.RunOptions{
		Environment:      opts.InfisicalEnvironment,
		Path:             path,
		ProjectConfigDir: projectRoot,
		Watch:            watch,
	})
}

func serveAppSpec(backendDir string, appHost string, appPort string, appModule string) run.Spec {
	return run.Spec{
		Program: "uv",
		Args: []string{
			"run",
			"uvicorn",
			appModule,
			"--host",
			appHost,
			"--port",
			appPort,
			"--reload",
			"--reload-dir",
			filepath.Join(backendDir, "server"),
			"--reload-dir",
			filepath.Join(backendDir, "app"),
			"--reload-include",
			"*.json",
			"--reload-include",
			"*.html",
			"--reload-include",
			"*.toml",
		},
		Dir: backendDir,
	}
}

func backendServeShellCommand(spec run.Spec) string {
	parts := append([]string{spec.Program}, spec.Args...)
	lines := append([]string{}, backendMCPTunnelShellSnippet()...)
	lines = append(lines, `exec `+shellJoin(parts))
	return strings.Join(lines, "\n")
}

func backendMCPTunnelShellSnippet() []string {
	return []string{
		`if [ -n "${OT_BACKEND_MCP_TUNNEL_HOST:-}" ]; then`,
		`  MCP_ALLOWED_HOSTS="$(printf '%s' "${MCP_ALLOWED_HOSTS:-},${OT_BACKEND_MCP_TUNNEL_HOST}" | awk '`,
		`    BEGIN { RS=","; ORS="" }`,
		`    {`,
		`      gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0)`,
		`      sub(/^https:\/\//, "", $0)`,
		`      sub(/^http:\/\//, "", $0)`,
		`      sub(/\/.*$/, "", $0)`,
		`      gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0)`,
		`      sub(/\.$/, "", $0)`,
		`      if ($0 != "" && !seen[$0]++) {`,
		`        if (out != "") out = out "," $0`,
		`        else out = $0`,
		`      }`,
		`    }`,
		`    END { print out }`,
		`  ')"`,
		`  export MCP_ALLOWED_HOSTS`,
		`  OT_BACKEND_MCP_TUNNEL_ORIGIN="https://${OT_BACKEND_MCP_TUNNEL_HOST%.}"`,
		`  MCP_PUBLIC_URL="${OT_BACKEND_MCP_TUNNEL_PUBLIC_URL:-${OT_BACKEND_MCP_TUNNEL_ORIGIN}/mcp}"`,
		`  OT_WIDGET_STATIC_ASSET_BASE_URL="${OT_BACKEND_MCP_TUNNEL_ASSET_BASE_URL:-${OT_BACKEND_MCP_TUNNEL_ORIGIN}}"`,
		`  OT_WIDGET_RESOURCE_DOMAINS="$(printf '%s' "${OT_WIDGET_RESOURCE_DOMAINS:-},${OT_WIDGET_STATIC_ASSET_BASE_URL}" | awk '`,
		`    BEGIN { RS=","; ORS="" }`,
		`    {`,
		`      gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0)`,
		`      if ($0 == "") next`,
		`      value = $0`,
		`      if (value !~ /^[A-Za-z][A-Za-z0-9+.-]*:\/\//) value = "https://" value`,
		`      split(value, parts, "://")`,
		`      scheme = tolower(parts[1])`,
		`      rest = parts[2]`,
		`      sub(/\/.*$/, "", rest)`,
		`      sub(/\.$/, "", rest)`,
		`      origin = scheme "://" rest`,
		`      if (rest != "" && !seen[origin]++) {`,
		`        if (out != "") out = out "," origin`,
		`        else out = origin`,
		`      }`,
		`    }`,
		`    END { print out }`,
		`  ')"`,
		`  export MCP_PUBLIC_URL OT_WIDGET_STATIC_ASSET_BASE_URL OT_WIDGET_RESOURCE_DOMAINS`,
		`  if [ -n "${OT_BACKEND_MCP_TUNNEL_TENANT_URLS:-}" ]; then`,
		`    IFS='|' read -r -a _ot_backend_mcp_tenant_pairs <<< "${OT_BACKEND_MCP_TUNNEL_TENANT_URLS}"`,
		`    for _ot_backend_mcp_pair in "${_ot_backend_mcp_tenant_pairs[@]}"; do`,
		`      _ot_backend_mcp_slug="${_ot_backend_mcp_pair%%=*}"`,
		`      _ot_backend_mcp_url="${_ot_backend_mcp_pair#*=}"`,
		`      if [ -n "${_ot_backend_mcp_slug}" ] && [ "${_ot_backend_mcp_slug}" != "${_ot_backend_mcp_url}" ]; then`,
		`        _ot_backend_mcp_env_name="$(printf '%s' "${_ot_backend_mcp_slug}" | tr 'abcdefghijklmnopqrstuvwxyz-' 'ABCDEFGHIJKLMNOPQRSTUVWXYZ_')_MCP_PUBLIC_URL"`,
		`        export "${_ot_backend_mcp_env_name}=${_ot_backend_mcp_url}"`,
		`      fi`,
		`    done`,
		`    unset _ot_backend_mcp_tenant_pairs _ot_backend_mcp_pair _ot_backend_mcp_slug _ot_backend_mcp_url _ot_backend_mcp_env_name`,
		`  fi`,
		`  unset OT_BACKEND_MCP_TUNNEL_ORIGIN`,
		`fi`,
	}
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func maybeRestart(
	ctx context.Context,
	port string,
	appModule string,
	serviceName string,
	force bool,
) (bool, error) {
	if !portInUse(port) {
		return false, nil
	}
	pids := findUvicornPids(port, appModule)
	if len(pids) == 0 {
		return false, fmt.Errorf("port %s already in use by another process", port)
	}
	if !force {
		fmt.Printf("[%s] %s already running on port %s.\n", strings.ToLower(serviceName), serviceName, port)
		return true, nil
	}
	for _, pid := range pids {
		_ = run.RunInteractive(ctx, run.Spec{Program: "kill", Args: []string{"-TERM", pid}})
	}
	time.Sleep(2 * time.Second)
	if portInUse(port) {
		for _, pid := range pids {
			_ = run.RunInteractive(ctx, run.Spec{Program: "kill", Args: []string{"-KILL", pid}})
		}
	}
	return false, nil
}

func portInUse(port string) bool {
	_, err := run.RunCapture(context.Background(), run.Spec{
		Program: "lsof",
		Args:    []string{"-nP", "-iTCP:" + port, "-sTCP:LISTEN", "-t"},
	})
	return err == nil
}

func stopService(
	ctx context.Context,
	repoRoot string,
	port string,
	appModule string,
	serviceName string,
) error {
	pids := findUvicornPids(port, appModule)
	if len(pids) == 0 {
		return fmt.Errorf("%s not running", serviceName)
	}
	for _, pid := range pids {
		_ = run.RunInteractive(ctx, run.Spec{Program: "kill", Args: []string{"-TERM", pid}, Dir: repoRoot})
	}
	time.Sleep(2 * time.Second)
	if portInUse(port) {
		for _, pid := range pids {
			_ = run.RunInteractive(ctx, run.Spec{Program: "kill", Args: []string{"-KILL", pid}, Dir: repoRoot})
		}
	}
	return nil
}

func findUvicornPids(port string, appModule string) []string {
	out, err := run.RunCapture(context.Background(), run.Spec{
		Program: "lsof",
		Args:    []string{"-nP", "-iTCP:" + port, "-sTCP:LISTEN", "-t"},
	})
	if err != nil {
		return nil
	}
	lines := strings.Fields(out)
	if len(lines) == 0 {
		return nil
	}
	var pids []string
	for _, pid := range lines {
		cmdline, err := run.RunCapture(context.Background(), run.Spec{
			Program: "ps",
			Args:    []string{"-o", "command=", "-p", pid},
		})
		if err != nil {
			continue
		}
		if strings.Contains(cmdline, "uvicorn") && strings.Contains(cmdline, appModule) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func defaultInitDBURL(port string) string {
	return fmt.Sprintf("postgresql://postgres:postgres@127.0.0.1:%s/postgres", port)
}

func defaultAppDBURL(port string) string {
	return fmt.Sprintf("postgresql://postgres:postgres@127.0.0.1:%s/app", port)
}

func defaultDBImage() string {
	return "pgvector/pgvector:pg18"
}

func defaultBackendCORSAllowOrigins() string {
	return strings.Join(
		[]string{
			localOrigin("localhost", getenvDefault("LOCAL_ROUTER_PORT", "3000")),
			localOrigin("127.0.0.1", getenvDefault("LOCAL_ROUTER_PORT", "3000")),
			localOrigin("localhost", getenvDefault("MARKETING_PORT", "3005")),
			localOrigin("127.0.0.1", getenvDefault("MARKETING_PORT", "3005")),
			localOrigin("localhost", getenvDefault("AUTH_WEB_PORT", "3006")),
			localOrigin("127.0.0.1", getenvDefault("AUTH_WEB_PORT", "3006")),
			localOrigin("localhost", getenvDefault("APP_WEB_PORT", "3007")),
			localOrigin("127.0.0.1", getenvDefault("APP_WEB_PORT", "3007")),
		},
		",",
	)
}

func defaultAuthFrontendBaseURL() string {
	return localOrigin("localhost", getenvDefault("AUTH_WEB_PORT", "3006"))
}

func defaultAuthCORSAllowOrigins() string {
	return strings.Join(
		[]string{
			localOrigin("localhost", getenvDefault("LOCAL_ROUTER_PORT", "3000")),
			localOrigin("127.0.0.1", getenvDefault("LOCAL_ROUTER_PORT", "3000")),
			localOrigin("localhost", getenvDefault("AUTH_WEB_PORT", "3006")),
			localOrigin("127.0.0.1", getenvDefault("AUTH_WEB_PORT", "3006")),
			localOrigin("localhost", getenvDefault("CRM_PORT", "8006")),
			localOrigin("127.0.0.1", getenvDefault("CRM_PORT", "8006")),
		},
		",",
	)
}

func defaultAuthAllowedReturnToOrigins() string {
	return strings.Join(
		[]string{
			localOrigin("localhost", getenvDefault("LOCAL_ROUTER_PORT", "3000")),
			localOrigin("127.0.0.1", getenvDefault("LOCAL_ROUTER_PORT", "3000")),
			localOrigin("localhost", getenvDefault("AUTH_WEB_PORT", "3006")),
			localOrigin("127.0.0.1", getenvDefault("AUTH_WEB_PORT", "3006")),
			localOrigin("localhost", getenvDefault("CRM_PORT", "8006")),
			localOrigin("127.0.0.1", getenvDefault("CRM_PORT", "8006")),
		},
		",",
	)
}

func localOrigin(host string, port string) string {
	return "http://" + host + ":" + port
}

func requireVectorExtension(ctx context.Context, container string, image string, initCfg dbinit.Config) error {
	available, err := dbinit.VectorExtensionAvailable(ctx, container, initCfg.User, initCfg.Database, initCfg.Port)
	if err != nil {
		return fmt.Errorf("check pgvector availability: %w", err)
	}
	if available {
		return nil
	}
	return errors.New(
		"local backend database container " + container +
			" does not have the pgvector extension installed. " +
			"Use a pgvector-capable image such as " + image +
			" and recreate the local backend DB container before rerunning `ot up backend`",
	)
}
