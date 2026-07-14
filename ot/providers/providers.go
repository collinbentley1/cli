// Package providers selects the pluggable integrations behind ot's three
// external touch points:
//
//   - secrets:    wrapping commands so they run inside a scoped secret
//     environment (shipped implementation: Infisical)
//   - db_tunnel:  authenticated tunnels to remote datastores (shipped
//     implementation: Porter)
//   - mcp_tunnel: publishing the local backend MCP endpoint on a public
//     HTTPS URL (shipped implementation: Tailscale Funnel)
//
// The active provider for each slot is chosen, in order of precedence, by:
//
//  1. an environment variable (OT_SECRETS_PROVIDER, OT_DB_TUNNEL_PROVIDER,
//     OT_MCP_TUNNEL_PROVIDER),
//  2. the `providers:` block in ot/ot.yaml (recorded via Configure), or
//  3. the shipped default (infisical / porter / tailscale).
//
// "none" disables the slot: secrets wrapping becomes a no-op, DB tunnels
// refuse to start with a clear error, and the MCP tunnel is skipped.
package providers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/collinbentley1/cli/ot/config"
	"github.com/collinbentley1/cli/ot/run"
)

const (
	// SecretsEnvVar overrides the secrets provider selection.
	SecretsEnvVar = "OT_SECRETS_PROVIDER"
	// DBTunnelEnvVar overrides the DB tunnel provider selection.
	DBTunnelEnvVar = "OT_DB_TUNNEL_PROVIDER"
	// MCPTunnelEnvVar overrides the MCP tunnel provider selection.
	MCPTunnelEnvVar = "OT_MCP_TUNNEL_PROVIDER"

	// SecretsInfisical is the shipped secrets implementation.
	SecretsInfisical = "infisical"
	// DBTunnelPorter is the shipped DB tunnel implementation.
	DBTunnelPorter = "porter"
	// MCPTunnelTailscale is the shipped MCP tunnel implementation.
	MCPTunnelTailscale = "tailscale"
	// None disables a provider slot.
	None = "none"
)

// Secrets wraps commands so they execute inside a scoped secret environment.
// The shipped implementation lives in the env package (Infisical).
type Secrets interface {
	Name() string
	// WrapRunSpec returns a spec that runs the original command with the
	// secret scope in opts injected into its environment.
	WrapRunSpec(spec run.Spec, opts SecretsRunOptions) run.Spec
	// WrapShellCommand returns a shell command line that runs `command`
	// with the secret scope in opts injected into its environment.
	WrapShellCommand(command string, opts SecretsRunOptions) string
}

// SecretsRunOptions selects the secret scope a command runs under.
type SecretsRunOptions struct {
	// Environment is the provider-side environment name (for example "dev").
	Environment string
	// Path is the secret scope (folder) to inject, e.g. "/backend".
	Path string
	// ProjectConfigDir is the directory holding the provider's project
	// config (usually the repo root).
	ProjectConfigDir string
	// Watch restarts the wrapped process when secrets change.
	Watch bool
}

// DBTunnel opens an authenticated tunnel from a local port to a remote
// datastore. The shipped implementation lives in the porter package.
type DBTunnel interface {
	Name() string
	// Connect blocks while the tunnel for datastore `which` (a key of
	// porter.ports in ot/ot.yaml) is up.
	Connect(ctx context.Context, repoRoot string, cfg *config.Config, env string, which string) error
}

// MCPTunnel publishes the local backend MCP endpoint on a public HTTPS URL.
// The shipped implementation (Tailscale Funnel) lives in the cli package.
type MCPTunnel interface {
	Name() string
	// Domain returns the public HTTPS domain the tunnel will serve.
	Domain(ctx context.Context) (string, error)
	// Start publishes `target` (a local http://host:port) at mountPath.
	Start(ctx context.Context, target string, mountPath string) error
	// Stop removes the tunnel mounted at mountPath.
	Stop(ctx context.Context, mountPath string) error
}

// Selection carries the provider names chosen in ot/ot.yaml.
type Selection struct {
	Secrets   string
	DBTunnel  string
	MCPTunnel string
}

var (
	mu         sync.RWMutex
	configured Selection
)

// Configure records the ot/ot.yaml provider selection and validates both the
// configured values and any environment overrides. Call it once after
// loading config; unknown provider names fail loudly here so a typo cannot
// silently disable secrets injection.
func Configure(sel Selection) error {
	checks := []struct {
		slot    string
		value   string
		envVar  string
		allowed []string
	}{
		{"providers.secrets", sel.Secrets, SecretsEnvVar, []string{SecretsInfisical, None}},
		{"providers.db_tunnel", sel.DBTunnel, DBTunnelEnvVar, []string{DBTunnelPorter, None}},
		{"providers.mcp_tunnel", sel.MCPTunnel, MCPTunnelEnvVar, []string{MCPTunnelTailscale, None}},
	}
	for _, check := range checks {
		if err := validateName(check.slot+" in ot/ot.yaml", check.value, check.allowed); err != nil {
			return err
		}
		if err := validateName(check.envVar, os.Getenv(check.envVar), check.allowed); err != nil {
			return err
		}
	}
	mu.Lock()
	defer mu.Unlock()
	configured = sel
	return nil
}

func validateName(source string, value string, allowed []string) error {
	name := normalize(value)
	if name == "" {
		return nil
	}
	for _, candidate := range allowed {
		if name == candidate {
			return nil
		}
	}
	return fmt.Errorf("unknown provider %q for %s (valid: %s)", value, source, strings.Join(allowed, ", "))
}

// SecretsName returns the active secrets provider name.
func SecretsName() string {
	return resolve(SecretsEnvVar, func(sel Selection) string { return sel.Secrets }, SecretsInfisical)
}

// DBTunnelName returns the active DB tunnel provider name.
func DBTunnelName() string {
	return resolve(DBTunnelEnvVar, func(sel Selection) string { return sel.DBTunnel }, DBTunnelPorter)
}

// MCPTunnelName returns the active MCP tunnel provider name.
func MCPTunnelName() string {
	return resolve(MCPTunnelEnvVar, func(sel Selection) string { return sel.MCPTunnel }, MCPTunnelTailscale)
}

// SecretsEnabled reports whether a secrets provider is active.
func SecretsEnabled() bool { return SecretsName() != None }

// DBTunnelEnabled reports whether a DB tunnel provider is active.
func DBTunnelEnabled() bool { return DBTunnelName() != None }

// MCPTunnelEnabled reports whether an MCP tunnel provider is active.
func MCPTunnelEnabled() bool { return MCPTunnelName() != None }

func resolve(envVar string, fromSelection func(Selection) string, fallback string) string {
	if value := normalize(os.Getenv(envVar)); value != "" {
		return value
	}
	mu.RLock()
	value := normalize(fromSelection(configured))
	mu.RUnlock()
	if value != "" {
		return value
	}
	return fallback
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
