package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "ot"), 0o755); err != nil {
		t.Fatalf("create ot dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "ot", "ot.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write ot.yaml: %v", err)
	}
	return repoRoot
}

func TestLoadRoundTripsVersionOneConfig(t *testing.T) {
	repoRoot := writeConfig(t, `version: 1
providers:
  secrets: infisical
  db_tunnel: porter
  mcp_tunnel: tailscale
infisical:
  environment: dev
  paths:
    up:
      all: ["/backend"]
      frontend: ["/frontend"]
    check:
      mcp: ["/backend"]
    hook:
      datadog: ["/datadog"]
overmind:
  stacks:
    dev:
      procfile: Procfile.dev
      socket: .ot/overmind/dev.sock
      connect_process: backend
      can_die: [frontend-router]
docker:
  stop_by_default: [local-primary-app-pgsql]
porter:
  ports:
    app: 8150
  envs:
    nonprod:
      app_datastore: nonprod-primary-app-pgsql
`)

	cfg, err := Load(repoRoot)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Version != 1 {
		t.Fatalf("Version = %d, want 1", cfg.Version)
	}
	if cfg.Providers.Secrets != "infisical" || cfg.Providers.DBTunnel != "porter" || cfg.Providers.MCPTunnel != "tailscale" {
		t.Fatalf("Providers = %+v", cfg.Providers)
	}
	if cfg.Infisical.Environment != "dev" {
		t.Fatalf("Infisical.Environment = %q, want dev", cfg.Infisical.Environment)
	}
	if got := cfg.Infisical.Paths.Up["all"]; len(got) != 1 || got[0] != "/backend" {
		t.Fatalf("Infisical.Paths.Up[all] = %v", got)
	}
	if got := cfg.Infisical.Paths.Up["frontend"]; len(got) != 1 || got[0] != "/frontend" {
		t.Fatalf("Infisical.Paths.Up[frontend] = %v", got)
	}
	if got := cfg.Infisical.Paths.Check["mcp"]; len(got) != 1 || got[0] != "/backend" {
		t.Fatalf("Infisical.Paths.Check[mcp] = %v", got)
	}
	if got := cfg.Infisical.Paths.Hook["datadog"]; len(got) != 1 || got[0] != "/datadog" {
		t.Fatalf("Infisical.Paths.Hook[datadog] = %v", got)
	}
	stack, ok := cfg.Overmind.Stacks["dev"]
	if !ok {
		t.Fatalf("Overmind.Stacks missing dev: %+v", cfg.Overmind.Stacks)
	}
	if stack.Procfile != "Procfile.dev" || stack.Socket != ".ot/overmind/dev.sock" ||
		stack.ConnectProcess != "backend" || len(stack.CanDie) != 1 || stack.CanDie[0] != "frontend-router" {
		t.Fatalf("dev stack = %+v", stack)
	}
	if got := cfg.Docker.StopByDefault; len(got) != 1 || got[0] != "local-primary-app-pgsql" {
		t.Fatalf("Docker.StopByDefault = %v", got)
	}
	if got := cfg.Porter.Ports["app"]; got != 8150 {
		t.Fatalf("Porter.Ports[app] = %d, want 8150", got)
	}
	if got := cfg.Porter.Envs["nonprod"].AppDatastore; got != "nonprod-primary-app-pgsql" {
		t.Fatalf("Porter.Envs[nonprod].AppDatastore = %q", got)
	}
}

func TestLoadRejectsUnsupportedVersion(t *testing.T) {
	repoRoot := writeConfig(t, "version: 2\n")
	_, err := Load(repoRoot)
	if err == nil || !strings.Contains(err.Error(), "unsupported ot/ot.yaml version: 2 (expected 1)") {
		t.Fatalf("Load(version: 2) error = %v, want unsupported-version error", err)
	}
}

func TestLoadRejectsMissingVersion(t *testing.T) {
	repoRoot := writeConfig(t, "providers:\n  secrets: none\n")
	_, err := Load(repoRoot)
	if err == nil || !strings.Contains(err.Error(), "missing the required `version: 1` field") {
		t.Fatalf("Load(no version) error = %v, want missing-version error", err)
	}
}

func TestLoadWrapsReadError(t *testing.T) {
	repoRoot := t.TempDir() // no ot/ot.yaml at all
	_, err := Load(repoRoot)
	if err == nil || !strings.Contains(err.Error(), "read ") || !strings.Contains(err.Error(), filepath.Join("ot", "ot.yaml")) {
		t.Fatalf("Load(missing file) error = %v, want wrapped read error naming the path", err)
	}
}

func TestLoadWrapsParseError(t *testing.T) {
	repoRoot := writeConfig(t, "version: [1\n")
	_, err := Load(repoRoot)
	if err == nil || !strings.Contains(err.Error(), "parse ") || !strings.Contains(err.Error(), filepath.Join("ot", "ot.yaml")) {
		t.Fatalf("Load(malformed yaml) error = %v, want wrapped parse error naming the path", err)
	}
}
