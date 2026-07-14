package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLatestBackendMCPCustomerTenantURLs(t *testing.T) {
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
        {"template_version": "v1.0.0", "state": "retired"},
        {"template_version": "v1.0.9", "state": "candidate"},
        {"template_version": "v1.0.10", "state": "candidate"}
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

	got, err := latestBackendMCPCustomerTenantURLs(repoRoot, "https://demo-machine.example.ts.net/mcp")
	if err != nil {
		t.Fatalf("latestBackendMCPCustomerTenantURLs() returned error: %v", err)
	}

	want := []backendMCPTenantURL{
		{
			Slug:    "demo-destination",
			Title:   "Demo Destination",
			Version: "v1.0.10",
			URL:     "https://demo-machine.example.ts.net/demo-destination/v1.0.10/mcp",
		},
		{
			Slug:    "demo-outfitter",
			Title:   "Demo Outfitter",
			Version: "v1.0.1",
			URL:     "https://demo-machine.example.ts.net/demo-outfitter/v1.0.1/mcp",
		},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tenant URLs, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tenant URL %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestLatestBackendMCPCustomerTenantURLsRejectsMissingPublicHost(t *testing.T) {
	_, err := latestBackendMCPCustomerTenantURLs(t.TempDir(), "/mcp")
	if err == nil || !strings.Contains(err.Error(), "scheme and host") {
		t.Fatalf("expected scheme/host error, got %v", err)
	}
}

func TestLatestBackendMCPCustomerTenantURLsPreservesWorktreePathPrefix(t *testing.T) {
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
        {"template_version": "v1.0.1", "state": "published"}
      ]
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(catalogDir, "customer_apps.json"), []byte(catalog), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	got, err := latestBackendMCPCustomerTenantURLs(
		repoRoot,
		"https://demo-machine.example.ts.net/_ot/e43efd2031/mcp",
	)
	if err != nil {
		t.Fatalf("latestBackendMCPCustomerTenantURLs() returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d tenant URLs, want 1: %#v", len(got), got)
	}
	want := "https://demo-machine.example.ts.net/_ot/e43efd2031/demo-destination/v1.0.1/mcp"
	if got[0].URL != want {
		t.Fatalf("tenant URL = %q, want %q", got[0].URL, want)
	}
}

func TestBackendMCPTenantURLSEnvValue(t *testing.T) {
	tenantURLs := []backendMCPTenantURL{
		{
			Slug: "demo-destination",
			URL:  "https://demo-machine.example.ts.net/demo-destination/v1.0.1/mcp",
		},
		{
			Slug: "demo-outfitter",
			URL:  "https://demo-machine.example.ts.net/demo-outfitter/v1.0.1/mcp",
		},
	}

	got := backendMCPTenantURLsEnvValue(tenantURLs)
	want := strings.Join(
		[]string{
			"demo-destination=https://demo-machine.example.ts.net/demo-destination/v1.0.1/mcp",
			"demo-outfitter=https://demo-machine.example.ts.net/demo-outfitter/v1.0.1/mcp",
		},
		"|",
	)
	if got != want {
		t.Fatalf("backendMCPTenantURLsEnvValue() = %q, want %q", got, want)
	}

	envName := backendMCPTenantPublicURLEnvName("demo-outfitter")
	if envName != "DEMO_OUTFITTER_MCP_PUBLIC_URL" {
		t.Fatalf("backendMCPTenantPublicURLEnvName() = %q", envName)
	}
}

func TestLatestBackendMCPCustomerVersionRequiresNonRetiredVersion(t *testing.T) {
	_, err := latestBackendMCPCustomerVersion(backendMCPCustomerApp{
		Slug: "demo-destination",
		Versions: []backendMCPCustomerAppVersion{
			{TemplateVersion: "v1.0.0", State: "retired"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "no non-retired versions") {
		t.Fatalf("expected non-retired version error, got %v", err)
	}
}

func TestMCPVersionSortKeyMatchesPythonTupleOrdering(t *testing.T) {
	left, err := mcpVersionSortKey("v1.0.10")
	if err != nil {
		t.Fatalf("mcpVersionSortKey(left) returned error: %v", err)
	}
	right, err := mcpVersionSortKey("v1.0.9")
	if err != nil {
		t.Fatalf("mcpVersionSortKey(right) returned error: %v", err)
	}
	if compareMCPVersionSortKeys(left, right) <= 0 {
		t.Fatalf("expected v1.0.10 to sort after v1.0.9")
	}
}
