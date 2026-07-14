package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/bootstrap"
	"github.com/collinbentley1/cli/ot/localdev"
	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
)

const defaultHarnessBackendURL = "http://127.0.0.1:8005"

type mcpHarnessTargetEnv struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Version     string `json:"version,omitempty"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

type mcpHarnessOptions struct {
	Target           string
	TargetVersion    string
	BackendURL       string
	MCPURL           string
	OpenAIModel      string
	OpenAIBaseURL    string
	Host             string
	Port             int
	SystemPromptFile string
	NoBrowser        bool
	ListTargets      bool
	Verbose          bool
}

func checkCmd() *cobra.Command {
	var mcpOptions mcpHarnessOptions

	cmd := &cobra.Command{
		Use:   "check [target]",
		Short: "Run pre-commit hooks for a target",
		Long: `Run pre-commit hooks. Targets:
  datadog
  auth
  backend
  crm
  frontend
  wordpress
  skills
  ot
  mcp     start the local MCP test harness (root app + tenant apps)

Without a target, runs all pre-commit hooks.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			if len(args) == 0 {
				return runPreCommit(cmd, rt, []string{"run", "--all-files"})
			}

			target := strings.TrimSpace(args[0])
			switch target {
			case "datadog":
				return runOT(cmd, rt, []string{"datadog-hook"})
			case "auth":
				if len(args) > 1 {
					return fmt.Errorf("unknown auth target: %s", strings.Join(args[1:], " "))
				}
				if err := runAuthFixes(cmd, rt); err != nil {
					return err
				}
				return runPreCommitHooks(
					cmd,
					rt,
					[]string{
						"backend-requirements",
						"auth-ruff",
						"auth-black-check",
						"auth-ty",
						"auth-pytest",
						"auth-porter-validate",
					},
				)
			case "backend":
				if err := runBackendFixes(cmd, rt); err != nil {
					return err
				}
				return runPreCommitHooks(cmd, rt, backendCheckHooks())
			case "crm":
				if len(args) > 1 {
					return fmt.Errorf("unknown crm target: %s", strings.Join(args[1:], " "))
				}
				if err := runCRMFixes(cmd, rt); err != nil {
					return err
				}
				return runPreCommitHooks(cmd, rt, []string{"crm-ruff", "crm-black-check", "crm-ty", "crm-pytest"})
			case "ot":
				if err := runOTFixes(cmd, rt); err != nil {
					return err
				}
				return runOTLint(cmd, rt)
			case "frontend":
				if len(args) > 1 {
					return fmt.Errorf("unknown frontend target: %s", strings.Join(args[1:], " "))
				}
				if err := runFrontendFixes(cmd, rt); err != nil {
					return err
				}
				if err := runPreCommitHooks(cmd, rt, []string{"frontend-app-lint", "frontend-app-prettier-check", "frontend-auth-lint", "frontend-auth-prettier-check", "frontend-marketing-lint", "frontend-marketing-prettier-check", "frontend-docs-lint", "frontend-docs-prettier-check"}); err != nil {
					return err
				}
				if err := runFrontendBuilds(cmd, rt); err != nil {
					return err
				}
				return runFrontendSmoke(cmd, rt)
			case "wordpress":
				if len(args) > 1 {
					return fmt.Errorf("unknown wordpress target: %s", strings.Join(args[1:], " "))
				}
				return runPreCommitHooks(cmd, rt, []string{"wordpress-plugin-validate"})
			case "skills":
				if len(args) > 1 {
					return fmt.Errorf("unknown skills target: %s", strings.Join(args[1:], " "))
				}
				return runSkillsChecks(cmd, rt)
			case "mcp":
				if len(args) > 1 {
					return fmt.Errorf("unknown mcp target: %s", strings.Join(args[1:], " "))
				}
				return runMCPHarness(cmd, rt, mcpOptions)
			default:
				return fmt.Errorf("unknown check target: %s", target)
			}
		},
	}
	cmd.Flags().StringVar(&mcpOptions.Target, "target", "", "MCP harness target slug for `ot check mcp`")
	cmd.Flags().StringVar(&mcpOptions.TargetVersion, "target-version", "", "MCP harness target version for `ot check mcp`")
	cmd.Flags().StringVar(&mcpOptions.BackendURL, "backend-url", "", "MCP harness backend base URL for `ot check mcp`")
	cmd.Flags().StringVar(&mcpOptions.MCPURL, "mcp-url", "", "MCP harness full MCP endpoint override for `ot check mcp`")
	cmd.Flags().StringVar(&mcpOptions.OpenAIModel, "openai-model", "", "MCP harness OpenAI model for `ot check mcp`")
	cmd.Flags().StringVar(&mcpOptions.OpenAIBaseURL, "openai-base-url", "", "MCP harness OpenAI base URL for `ot check mcp`")
	cmd.Flags().StringVar(&mcpOptions.Host, "host", "", "MCP harness UI bind host for `ot check mcp`")
	cmd.Flags().IntVar(&mcpOptions.Port, "port", 0, "MCP harness UI bind port for `ot check mcp`")
	cmd.Flags().StringVar(&mcpOptions.SystemPromptFile, "system-prompt-file", "", "MCP harness system prompt file for `ot check mcp`")
	cmd.Flags().BoolVar(&mcpOptions.NoBrowser, "no-browser", false, "Do not auto-open the MCP harness browser UI")
	cmd.Flags().BoolVar(&mcpOptions.ListTargets, "list-targets", false, "List MCP harness targets and exit")
	cmd.Flags().BoolVarP(&mcpOptions.Verbose, "verbose", "v", false, "Enable MCP harness debug logging")

	return cmd
}

func backendCheckHooks() []string {
	return []string{
		"datadog-static-analyzer",
		"backend-requirements",
		"backend-migration-smoke",
		"backend-mcp-app-contracts",
		"backend-mcp-app-release-catalog",
		"backend-mcp-widget-compatibility",
		"backend-mcp-contract-import-boundaries",
		"backend-ruff",
		"backend-black-check",
		"backend-ty",
	}
}

func runOTLint(cmd *cobra.Command, rt *Runtime) error {
	if bootstrap.IsCloudAgent() {
		return run.RunInteractive(cmd.Context(), run.Spec{
			Program: "go",
			Args: []string{
				"run",
				"github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4",
				"run",
				"--allow-serial-runners",
				"./ot/...",
			},
			Dir: rt.RepoRoot,
		})
	}
	return run.RunInteractive(cmd.Context(), run.Spec{
		Program: "golangci-lint",
		Args:    []string{"run", "--allow-serial-runners", "./ot/..."},
		Dir:     rt.RepoRoot,
	})
}

func runPreCommit(cmd *cobra.Command, rt *Runtime, args []string) error {
	if err := run.RunInteractive(cmd.Context(), run.Spec{
		Program: "pre-commit",
		Args:    args,
		Dir:     rt.RepoRoot,
	}); err != nil {
		return fmt.Errorf("pre-commit run failed: %w", err)
	}
	return nil
}

func runPreCommitHooks(cmd *cobra.Command, rt *Runtime, hooks []string) error {
	for _, hook := range hooks {
		if err := runPreCommit(cmd, rt, []string{"run", hook, "--all-files"}); err != nil {
			return err
		}
	}
	return nil
}

func runFrontendFixes(cmd *cobra.Command, rt *Runtime) error {
	commands := [][]string{
		{"bash", "-c", "cd frontend/app && npx prettier --write ."},
		{"bash", "-c", "cd frontend/auth && npx prettier --write ."},
		{"bash", "-c", "cd frontend/marketing && npx prettier --write ."},
		{"bash", "-c", "cd frontend/docs && npx prettier --write ."},
	}
	for _, args := range commands {
		if err := run.RunInteractive(cmd.Context(), run.Spec{
			Program: args[0],
			Args:    args[1:],
			Dir:     rt.RepoRoot,
		}); err != nil {
			return err
		}
	}
	return nil
}

func runFrontendBuilds(cmd *cobra.Command, rt *Runtime) error {
	commands := [][]string{
		{"bash", "-c", "cd frontend && NODE_ENV=production npm run build --workspace app"},
		{"bash", "-c", "cd frontend && NODE_ENV=production npm run build --workspace auth"},
		{"bash", "-c", "cd frontend && NODE_ENV=production npm run build --workspace marketing"},
		{"bash", "-c", "cd frontend && NODE_ENV=production npm run build --workspace docs"},
	}
	for _, args := range commands {
		if err := run.RunInteractive(cmd.Context(), run.Spec{
			Program: args[0],
			Args:    args[1:],
			Dir:     rt.RepoRoot,
		}); err != nil {
			return err
		}
	}
	return nil
}

func runFrontendSmoke(cmd *cobra.Command, rt *Runtime) error {
	return run.RunInteractive(cmd.Context(), run.Spec{
		Program: "bash",
		Args:    []string{"-c", "cd frontend && npm run smoke:dev"},
		Dir:     rt.RepoRoot,
	})
}

func runBackendFixes(cmd *cobra.Command, rt *Runtime) error {
	commands := [][]string{
		{"bash", "-c", "cd backend && uv run python scripts/compile_mcp_app_contracts.py"},
		{"bash", "-c", "cd backend && uv run black ."},
	}
	for _, args := range commands {
		if err := run.RunInteractive(cmd.Context(), run.Spec{
			Program: args[0],
			Args:    args[1:],
			Dir:     rt.RepoRoot,
		}); err != nil {
			return err
		}
	}
	return nil
}

func runAuthFixes(cmd *cobra.Command, rt *Runtime) error {
	return run.RunInteractive(cmd.Context(), run.Spec{
		Program: "bash",
		Args: []string{
			"-c",
			"cd backend && uv run black server/auth_main.py server/config.py server/routers/auth.py server/services/workos_oauth.py server/services/workos_sessions.py tests/test_auth_login.py tests/test_auth_magic.py",
		},
		Dir: rt.RepoRoot,
	})
}

func runCRMFixes(cmd *cobra.Command, rt *Runtime) error {
	return run.RunInteractive(cmd.Context(), run.Spec{
		Program: "bash",
		Args:    []string{"-c", "cd crm && uv run black ."},
		Dir:     rt.RepoRoot,
	})
}

func runOTFixes(cmd *cobra.Command, rt *Runtime) error {
	commands := [][]string{
		{"bash", "-c", "gofmt -w ot"},
	}
	for _, args := range commands {
		if err := run.RunInteractive(cmd.Context(), run.Spec{
			Program: args[0],
			Args:    args[1:],
			Dir:     rt.RepoRoot,
		}); err != nil {
			return err
		}
	}
	return nil
}

func runSkillsChecks(cmd *cobra.Command, rt *Runtime) error {
	return run.RunInteractive(cmd.Context(), run.Spec{
		Program: "bash",
		Args:    []string{"ot/scripts/check_skills.sh"},
		Dir:     rt.RepoRoot,
	})
}

// runMCPHarness boots the MCP test harness against the locally-running
// `ot up backend`. It is an interactive command — the harness opens a web UI
// for chatting with the backend's MCP apps (the root app plus any tenant
// apps in the catalog) under a real OpenAI tool-calling loop and renders the
// resulting widget cards inline.
//
// Wrapped via the `check` -> `mcp` secrets path mapping in ot.yaml so
// `OPENAI_API_KEY` is injected from the same `/backend` secret scope the
// backend itself uses.
func runMCPHarness(cmd *cobra.Command, rt *Runtime, options mcpHarnessOptions) error {
	harnessDir := filepath.Join(rt.RepoRoot, "harness")

	harnessArgs := []string{}
	if extra := strings.TrimSpace(os.Getenv("OT_MCP_HARNESS_ARGS")); extra != "" {
		harnessArgs = strings.Fields(extra)
	}
	harnessArgs = mcpHarnessArgsWithOptions(harnessArgs, options)
	harnessEnv := mcpHarnessDefaultEnv(rt.RepoRoot, harnessArgs)
	if backendURL, ok := mcpHarnessBackendURLWithEnv(harnessArgs, harnessEnv); ok {
		if err := ensureMCPBackendReachable(cmd.Context(), backendURL); err != nil {
			return err
		}
	}

	if err := run.RunInteractive(cmd.Context(), run.Spec{
		Program: "uv",
		Args:    []string{"sync", "--quiet"},
		Dir:     harnessDir,
	}); err != nil {
		return fmt.Errorf("uv sync (harness): %w", err)
	}

	args := []string{"run", "mcp-test-harness"}
	args = append(args, harnessArgs...)

	return run.RunInteractiveWithSignals(cmd.Context(), localdev.WrapSpecWithShellExports(run.Spec{
		Program: "uv",
		Args:    args,
		Dir:     harnessDir,
		Env:     harnessEnv,
	}))
}

func mcpHarnessArgsWithFlags(args []string, target string, targetVersion string) []string {
	return mcpHarnessArgsWithOptions(args, mcpHarnessOptions{
		Target:        target,
		TargetVersion: targetVersion,
	})
}

func mcpHarnessArgsWithOptions(args []string, options mcpHarnessOptions) []string {
	merged := append([]string{}, args...)
	appendString := func(flag string, value string) {
		if value = strings.TrimSpace(value); value != "" {
			merged = append(merged, flag, value)
		}
	}
	appendString("--target", options.Target)
	appendString("--target-version", options.TargetVersion)
	appendString("--backend-url", options.BackendURL)
	appendString("--mcp-url", options.MCPURL)
	appendString("--openai-model", options.OpenAIModel)
	appendString("--openai-base-url", options.OpenAIBaseURL)
	appendString("--host", options.Host)
	if options.Port != 0 {
		merged = append(merged, "--port", fmt.Sprintf("%d", options.Port))
	}
	appendString("--system-prompt-file", options.SystemPromptFile)
	if options.NoBrowser {
		merged = append(merged, "--no-browser")
	}
	if options.ListTargets {
		merged = append(merged, "--list-targets")
	}
	if options.Verbose {
		merged = append(merged, "--verbose")
	}
	return merged
}

func mcpHarnessBackendURL(args []string) (string, bool) {
	return mcpHarnessBackendURLWithEnv(args, nil)
}

func mcpHarnessBackendURLWithEnv(args []string, env map[string]string) (string, bool) {
	backendURL := strings.TrimSpace(harnessEnvValue(env, "HARNESS_BACKEND_URL"))
	if backendURL == "" {
		backendURL = defaultHarnessBackendURL
	}

	if mcpHarnessArgsHaveListTargets(args) {
		return "", false
	}
	if mcpHarnessArgsHaveMCPURL(args) {
		return "", false
	}
	if argURL, ok := mcpHarnessBackendURLArg(args); ok {
		backendURL = argURL
	}

	return strings.TrimRight(backendURL, "/"), true
}

func mcpHarnessBackendURLArg(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case arg == "--backend-url":
			if i+1 < len(args) {
				return strings.TrimSpace(args[i+1]), true
			}
		case strings.HasPrefix(arg, "--backend-url="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "--backend-url=")), true
		}
	}
	return "", false
}

func mcpHarnessArgsHaveListTargets(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "--list-targets" {
			return true
		}
	}
	return false
}

func harnessEnvValue(env map[string]string, key string) string {
	if env != nil {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(os.Getenv(key))
}

func mcpHarnessDefaultEnv(repoRoot string, args []string) map[string]string {
	env := map[string]string{}
	if mcpHarnessArgsHaveMCPURL(args) {
		return env
	}

	backendURL := strings.TrimSpace(os.Getenv("HARNESS_BACKEND_URL"))
	if backendURL == "" {
		backendURL = defaultHarnessBackendURL
	}
	if argURL, ok := mcpHarnessBackendURLArg(args); ok {
		backendURL = strings.TrimRight(argURL, "/")
	}
	publicMCPURL := strings.TrimRight(backendURL, "/") + "/mcp"
	if !mcpHarnessArgsHaveBackendURL(args) {
		if tunnelURL, err := readBackendMCPTunnelState(repoRoot); err == nil && tunnelURL != "" {
			if baseURL, err := publicMCPBaseURL(tunnelURL); err == nil {
				publicMCPURL = tunnelURL
				env["HARNESS_BACKEND_URL"] = baseURL
				env["MCP_PUBLIC_URL"] = tunnelURL
			}
		}
	}

	if !mcpHarnessArgsHaveTarget(args) && strings.TrimSpace(os.Getenv("HARNESS_TARGET")) == "" {
		env["HARNESS_TARGET"] = "root-app"
	}
	if targetsJSON, err := mcpHarnessTargetsJSON(repoRoot, publicMCPURL); err == nil && targetsJSON != "" {
		env["HARNESS_MCP_TARGETS_JSON"] = targetsJSON
	}
	return env
}

func mcpHarnessTargetsJSON(repoRoot string, publicMCPURL string) (string, error) {
	targets := []mcpHarnessTargetEnv{
		{
			Slug:        "root-app",
			Title:       "Root MCP app",
			Version:     "root",
			URL:         strings.TrimRight(publicMCPURL, "/"),
			Description: "Root MCP app served by the backend.",
		},
	}
	tenantURLs, err := latestBackendMCPCustomerTenantURLs(repoRoot, publicMCPURL)
	if err != nil {
		return "", err
	}
	for _, tenantURL := range tenantURLs {
		title := strings.TrimSpace(tenantURL.Title)
		if title == "" {
			title = tenantURL.Slug
		}
		targets = append(targets, mcpHarnessTargetEnv{
			Slug:    tenantURL.Slug,
			Title:   title,
			Version: tenantURL.Version,
			URL:     tenantURL.URL,
		})
	}
	data, err := json.Marshal(targets)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func mcpHarnessArgsHaveMCPURL(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "--mcp-url" || strings.HasPrefix(arg, "--mcp-url=") {
			return true
		}
	}
	return false
}

func mcpHarnessArgsHaveBackendURL(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "--backend-url" || strings.HasPrefix(arg, "--backend-url=") {
			return true
		}
	}
	return false
}

func mcpHarnessArgsHaveTarget(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "--target" || strings.HasPrefix(arg, "--target=") {
			return true
		}
	}
	return false
}

func ensureMCPBackendReachable(ctx context.Context, backendURL string) error {
	healthURL, err := backendHealthURL(backendURL)
	if err != nil {
		return fmt.Errorf("invalid MCP backend URL %q: %w", backendURL, err)
	}

	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return fmt.Errorf("build MCP backend health request: %w", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return mcpBackendUnavailableError(ctx, backendURL, err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return mcpBackendUnavailableError(ctx, backendURL, fmt.Errorf("health check returned HTTP %d", res.StatusCode))
	}
	return nil
}

func backendHealthURL(backendURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(backendURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("expected absolute URL with scheme and host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/health"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func mcpBackendUnavailableError(ctx context.Context, backendURL string, cause error) error {
	message := fmt.Sprintf(
		"MCP backend is not reachable at %s: %v\nStart the backend from this checkout with `ot up backend`, then rerun `ot check mcp`.",
		backendURL,
		cause,
	)
	if hint := sharedBackendHint(ctx, backendURL); hint != "" {
		message += "\n" + hint
	}
	if localdev.Active() {
		message += "\nWorktree isolation is active, so this harness expects the backend for this checkout rather than a backend from another checkout."
	}
	return fmt.Errorf("%s", message)
}

func sharedBackendHint(ctx context.Context, backendURL string) string {
	if strings.TrimRight(backendURL, "/") == defaultHarnessBackendURL {
		return ""
	}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	healthURL, err := backendHealthURL(defaultHarnessBackendURL)
	if err != nil {
		return ""
	}
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return ""
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return "A backend is reachable at http://127.0.0.1:8005, but this worktree is configured for " + backendURL + "."
	}
	return ""
}

func runOT(cmd *cobra.Command, rt *Runtime, args []string) error {
	return run.RunInteractive(cmd.Context(), run.Spec{
		Program: "ot",
		Args:    args,
		Dir:     rt.RepoRoot,
		Env:     map[string]string{"OT_SKIP_SELF_CHECK": "1"},
	})
}
