package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/collinbentley1/cli/ot/bootstrap"
	"github.com/collinbentley1/cli/ot/config"
	"github.com/collinbentley1/cli/ot/env"
	"github.com/collinbentley1/cli/ot/localdev"
	"github.com/collinbentley1/cli/ot/paths"
	"github.com/collinbentley1/cli/ot/providers"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "ot",
	Short:         "ot — per-worktree dev runtime (isolated DB, ports, secrets scope, MCP tunnel)",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	if err := buildCommands().Execute(); err != nil {
		if errors.Is(err, env.ErrReexecuted) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

var skipBootstrap = map[string]bool{
	"help":               true,
	"completion":         true,
	"version":            true,
	"upgrade":            true,
	"bootstrap":          true,
	"selfcheck":          true,
	"reset":              true,
	"down":               true,
	"logs":               true,
	"db-connect":         true,
	"backend-mcp-tunnel": true,
}

var quietBootstrap = map[string]bool{
	"up": true,
}

func buildCommands() *cobra.Command {
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		repoRoot, err := paths.FindRepoRoot(wd)
		if err != nil {
			return err
		}
		cfg, err := config.Load(repoRoot)
		if err != nil {
			return err
		}
		if err := providers.Configure(providers.Selection{
			Secrets:   cfg.Providers.Secrets,
			DBTunnel:  cfg.Providers.DBTunnel,
			MCPTunnel: cfg.Providers.MCPTunnel,
		}); err != nil {
			return err
		}
		if _, err := localdev.Apply(repoRoot); err != nil {
			return err
		}
		secretPaths := inferInfisicalPathsForArgs(cmd, cfg, args)

		if skipBootstrap[cmd.Name()] || (cmd.Parent() != nil && skipBootstrap[cmd.Parent().Name()]) {
			cmd.SetContext(withRuntime(cmd.Context(), &Runtime{
				RepoRoot: repoRoot,
				Config:   cfg,
			}))
			return nil
		}

		if !skipSelfCheck(cmd) {
			if err := runSelfCheck(cmd.Context(), repoRoot); err != nil {
				return err
			}
		}

		if err := env.RunSelfIfNeeded(cmd.Context(), env.Options{
			Environment:      cfg.Infisical.Environment,
			Paths:            secretPaths,
			ProjectConfigDir: repoRoot,
			Args:             os.Args[1:],
		}); err != nil {
			return err
		}
		if _, err := localdev.Apply(repoRoot); err != nil {
			return err
		}

		if !skipBootstrapRun(cmd) {
			if err := bootstrap.Run(cmd.Context(), bootstrapOptionsForCommandForArgs(cmd, repoRoot, args)); err != nil {
				return err
			}
		}

		cmd.SetContext(withRuntime(cmd.Context(), &Runtime{
			RepoRoot: repoRoot,
			Config:   cfg,
		}))
		return nil
	}

	rootCmd.AddCommand(
		bootstrapCmd(),
		checkCmd(),
		datadogHookCmd(),
		selfcheckCmd(),
		upCmd(),
		downCmd(),
		logsCmd(),
		resetCmd(),
		upgradeCmd(),
		versionCmd(),
		dbConnectCmd(),
		backendMCPTunnelCmd(),
	)

	return rootCmd
}

func inferInfisicalPaths(cmd *cobra.Command, cfg *config.Config) []string {
	return inferInfisicalPathsForArgs(cmd, cfg, cmd.Flags().Args())
}

func inferInfisicalPathsForArgs(cmd *cobra.Command, cfg *config.Config, args []string) []string {
	name, positionalArgs := effectiveCommandNameAndArgs(cmd, args)
	switch name {
	case "up":
		return inferUpInfisicalPathsForArgs(cfg, positionalArgs)
	case "datadog-hook":
		return resolveConfiguredInfisicalPaths(cfg.Infisical.Paths.Hook, "datadog")
	case "check":
		if cmd.Flags().Changed("help") {
			return nil
		}
		return inferCheckInfisicalPathsForArgs(cfg, positionalArgs)
	default:
		return nil
	}
}

func inferUpInfisicalPathsForArgs(cfg *config.Config, args []string) []string {
	if len(args) == 0 {
		return resolveConfiguredInfisicalPaths(cfg.Infisical.Paths.Up, "all")
	}

	service := strings.TrimSpace(args[0])
	if service == "" {
		return nil
	}
	return resolveConfiguredInfisicalPaths(cfg.Infisical.Paths.Up, service)
}

func inferCheckInfisicalPathsForArgs(cfg *config.Config, args []string) []string {
	if len(args) == 0 {
		return resolveConfiguredInfisicalPaths(cfg.Infisical.Paths.Check, "all")
	}
	target := strings.TrimSpace(args[0])
	if target == "" {
		return nil
	}
	return resolveConfiguredInfisicalPaths(cfg.Infisical.Paths.Check, target)
}

func resolveConfiguredInfisicalPaths(mapping map[string][]string, key string) []string {
	if len(mapping) == 0 {
		return nil
	}
	entries, ok := mapping[key]
	if !ok || len(entries) == 0 {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		value := strings.TrimSpace(entry)
		if value == "" {
			continue
		}
		ids = append(ids, value)
	}
	return ids
}

func bootstrapOptionsForCommand(cmd *cobra.Command, repoRoot string) bootstrap.Options {
	return bootstrapOptionsForCommandForArgs(cmd, repoRoot, cmd.Flags().Args())
}

func bootstrapOptionsForCommandForArgs(cmd *cobra.Command, repoRoot string, args []string) bootstrap.Options {
	name, positionalArgs := effectiveCommandNameAndArgs(cmd, args)
	quiet := quietBootstrap[name]
	opts := bootstrap.Options{
		RepoRoot: repoRoot,
		Quiet:    quiet,
		Mode:     bootstrap.ModeFull,
		Target:   commandBootstrapTargetForName(name, positionalArgs),
	}
	if name == "up" {
		opts.Mode = bootstrap.ModeRuntime
	}
	return opts
}

func upBootstrapTargetForName(name string, args []string) string {
	if name != "up" {
		return ""
	}
	if len(args) == 0 {
		return "all"
	}
	return strings.TrimSpace(args[0])
}

func commandBootstrapTargetForName(name string, args []string) string {
	if name == "up" {
		return upBootstrapTargetForName(name, args)
	}
	if name != "check" {
		return ""
	}
	if len(args) == 0 {
		return "all"
	}
	return strings.TrimSpace(args[0])
}

func effectiveCommandNameAndArgs(cmd *cobra.Command, args []string) (string, []string) {
	if cmd == nil {
		return "", args
	}
	name := cmd.Name()
	if cmd.Parent() != nil && cmd.Parent().Name() != rootCmd.Name() {
		name = cmd.Parent().Name()
	}
	if name == "" || name == rootCmd.Name() {
		if len(args) > 0 {
			return strings.TrimSpace(args[0]), args[1:]
		}
		if len(os.Args) > 1 {
			return strings.TrimSpace(os.Args[1]), os.Args[2:]
		}
	}
	return name, args
}

func runtimeOrFail(ctx context.Context) (*Runtime, error) {
	rt := getRuntime(ctx)
	if rt == nil || rt.Config == nil || rt.RepoRoot == "" {
		return nil, fmt.Errorf("internal error: runtime not initialized")
	}
	return rt, nil
}

func runSelfCheck(ctx context.Context, repoRoot string) error {
	needsUpgrade, err := otBinaryNeedsUpgrade(ctx, repoRoot, Version, "")
	if err != nil {
		return err
	}
	if !needsUpgrade {
		return nil
	}
	if err := execUpgrade(&Runtime{RepoRoot: repoRoot}); err != nil {
		return err
	}
	return reexecInstalledOt(ctx)
}

func reexecInstalledOt(ctx context.Context) error {
	otBin, err := otInstallSymlinkPath(ctx)
	if err != nil {
		return err
	}
	env := append(os.Environ(), "OT_SKIP_SELF_CHECK=1")
	return syscall.Exec(otBin, append([]string{otBin}, os.Args[1:]...), env)
}

func skipSelfCheck(cmd *cobra.Command) bool {
	if os.Getenv("OT_SKIP_SELF_CHECK") != "" {
		return true
	}
	return isCRMMCPStdioStart(cmd) || isDatadogHookCommand(cmd)
}

func skipBootstrapRun(cmd *cobra.Command) bool {
	return isCRMStart(cmd) || isDatadogHookCommand(cmd)
}

func isCRMMCPStdioStart(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Name() != "up" {
		return false
	}

	flag := cmd.Flags().Lookup("mcp-stdio")
	if flag == nil || flag.Value.String() != "true" {
		return false
	}

	args := cmd.Flags().Args()
	return len(args) > 0 && strings.TrimSpace(args[0]) == "crm"
}

func isCRMStart(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Name() != "up" {
		return false
	}
	args := cmd.Flags().Args()
	return len(args) > 0 && strings.TrimSpace(args[0]) == "crm"
}

func isDatadogHookCommand(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Name() == "datadog-hook"
}
