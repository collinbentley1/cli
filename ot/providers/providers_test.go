package providers

import (
	"strings"
	"testing"
)

func resetSelection(t *testing.T) {
	t.Helper()
	mu.Lock()
	configured = Selection{}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		configured = Selection{}
		mu.Unlock()
	})
	t.Setenv(SecretsEnvVar, "")
	t.Setenv(DBTunnelEnvVar, "")
	t.Setenv(MCPTunnelEnvVar, "")
}

func TestDefaultsAreTheShippedProviders(t *testing.T) {
	resetSelection(t)

	if got := SecretsName(); got != SecretsInfisical {
		t.Fatalf("SecretsName() = %q, want %q", got, SecretsInfisical)
	}
	if got := DBTunnelName(); got != DBTunnelPorter {
		t.Fatalf("DBTunnelName() = %q, want %q", got, DBTunnelPorter)
	}
	if got := MCPTunnelName(); got != MCPTunnelTailscale {
		t.Fatalf("MCPTunnelName() = %q, want %q", got, MCPTunnelTailscale)
	}
	if !SecretsEnabled() || !DBTunnelEnabled() || !MCPTunnelEnabled() {
		t.Fatal("expected all provider slots enabled by default")
	}
}

func TestConfigureRecordsYAMLSelection(t *testing.T) {
	resetSelection(t)

	if err := Configure(Selection{Secrets: None, DBTunnel: None, MCPTunnel: None}); err != nil {
		t.Fatalf("Configure() returned error: %v", err)
	}
	if SecretsEnabled() {
		t.Fatal("expected secrets disabled after Configure(none)")
	}
	if DBTunnelEnabled() {
		t.Fatal("expected db tunnel disabled after Configure(none)")
	}
	if MCPTunnelEnabled() {
		t.Fatal("expected mcp tunnel disabled after Configure(none)")
	}
}

func TestEnvOverrideWinsOverConfigured(t *testing.T) {
	resetSelection(t)

	if err := Configure(Selection{Secrets: None}); err != nil {
		t.Fatalf("Configure() returned error: %v", err)
	}
	t.Setenv(SecretsEnvVar, "infisical")
	if got := SecretsName(); got != SecretsInfisical {
		t.Fatalf("SecretsName() = %q, want env override %q", got, SecretsInfisical)
	}
}

func TestConfigureRejectsUnknownProviderNames(t *testing.T) {
	resetSelection(t)

	err := Configure(Selection{Secrets: "vault"})
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("expected unknown-provider error, got %v", err)
	}
}

func TestConfigureRejectsInvalidEnvOverride(t *testing.T) {
	resetSelection(t)
	t.Setenv(MCPTunnelEnvVar, "cloudflare")

	err := Configure(Selection{})
	if err == nil || !strings.Contains(err.Error(), MCPTunnelEnvVar) {
		t.Fatalf("expected env-override validation error, got %v", err)
	}
}
