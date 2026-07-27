package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBackendStackExtraEnvSetsTailnetMCPResourceURLs(t *testing.T) {
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

	tailscaleBin := filepath.Join(t.TempDir(), "tailscale")
	script := `#!/bin/sh
set -eu
if [ "$1" = "status" ] && [ "$2" = "--json" ]; then
  echo '{"CertDomains":["demo-machine.example.ts.net."]}'
  exit 0
fi
echo "unexpected tailscale args: $*" >&2
exit 1
`
	if err := os.WriteFile(tailscaleBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tailscale: %v", err)
	}
	t.Setenv("TAILSCALE_BIN", tailscaleBin)
	t.Setenv("OT_BACKEND_MCP_TUNNEL", "")
	t.Setenv("OT_WORKTREE_ISOLATION_ACTIVE", "")
	t.Setenv("OT_LOCAL_INSTANCE_ID", "")
	t.Setenv("OT_PUBLIC_PATH_PREFIX", "")
	t.Setenv("OT_BACKEND_MCP_TUNNEL_PATH", "")

	got := backendStackExtraEnv(context.Background(), repoRoot)

	want := map[string]string{
		"OT_SKIP_STOP":                         "1",
		"OT_BACKEND_MCP_TUNNEL_HOST":           "demo-machine.example.ts.net",
		"OT_BACKEND_MCP_TUNNEL_PUBLIC_URL":     "https://demo-machine.example.ts.net/mcp",
		"MCP_PUBLIC_URL":                       "https://demo-machine.example.ts.net/mcp",
		"OT_BACKEND_MCP_TUNNEL_ASSET_BASE_URL": "https://demo-machine.example.ts.net",
		"OT_WIDGET_STATIC_ASSET_BASE_URL":      "https://demo-machine.example.ts.net",
		"OT_WIDGET_RESOURCE_DOMAINS":           "https://demo-machine.example.ts.net",
		"DEMO_DESTINATION_MCP_PUBLIC_URL":      "https://demo-machine.example.ts.net/demo-destination/v1.0.1/mcp",
		"DEMO_OUTFITTER_MCP_PUBLIC_URL":        "https://demo-machine.example.ts.net/demo-outfitter/v1.0.1/mcp",
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("%s = %q, want %q; full env: %#v", key, got[key], wantValue, got)
		}
	}

	tenantPairs := got["OT_BACKEND_MCP_TUNNEL_TENANT_URLS"]
	for _, wantPair := range []string{
		"demo-destination=https://demo-machine.example.ts.net/demo-destination/v1.0.1/mcp",
		"demo-outfitter=https://demo-machine.example.ts.net/demo-outfitter/v1.0.1/mcp",
	} {
		if !strings.Contains(tenantPairs, wantPair) {
			t.Fatalf("OT_BACKEND_MCP_TUNNEL_TENANT_URLS missing %q in %q", wantPair, tenantPairs)
		}
	}
}

func TestBackendStackExtraEnvUsesWorktreeFunnelPath(t *testing.T) {
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
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(catalogDir, "customer_apps.json"), []byte(catalog), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}

	tailscaleBin := filepath.Join(t.TempDir(), "tailscale")
	script := `#!/bin/sh
set -eu
if [ "$1" = "status" ] && [ "$2" = "--json" ]; then
  echo '{"CertDomains":["demo-machine.example.ts.net."]}'
  exit 0
fi
echo "unexpected tailscale args: $*" >&2
exit 1
`
	if err := os.WriteFile(tailscaleBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tailscale: %v", err)
	}
	t.Setenv("TAILSCALE_BIN", tailscaleBin)
	t.Setenv("OT_BACKEND_MCP_TUNNEL", "")
	t.Setenv("OT_WORKTREE_ISOLATION_ACTIVE", "1")
	t.Setenv("OT_LOCAL_INSTANCE_ID", "e43efd2031")
	t.Setenv("OT_PUBLIC_PATH_PREFIX", "/_ot/e43efd2031")
	t.Setenv("OT_BACKEND_MCP_TUNNEL_PATH", "")

	got := backendStackExtraEnv(context.Background(), repoRoot)

	want := map[string]string{
		"OT_BACKEND_MCP_TUNNEL_PUBLIC_URL":     "https://demo-machine.example.ts.net/_ot/e43efd2031/mcp",
		"MCP_PUBLIC_URL":                       "https://demo-machine.example.ts.net/_ot/e43efd2031/mcp",
		"OT_BACKEND_MCP_TUNNEL_PATH":           "/_ot/e43efd2031",
		"OT_BACKEND_MCP_TUNNEL_ASSET_BASE_URL": "https://demo-machine.example.ts.net/_ot/e43efd2031",
		"OT_WIDGET_STATIC_ASSET_BASE_URL":      "https://demo-machine.example.ts.net/_ot/e43efd2031",
		"OT_WIDGET_RESOURCE_DOMAINS":           "https://demo-machine.example.ts.net",
		"DEMO_DESTINATION_MCP_PUBLIC_URL":      "https://demo-machine.example.ts.net/_ot/e43efd2031/demo-destination/v1.0.1/mcp",
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("%s = %q, want %q; full env: %#v", key, got[key], wantValue, got)
		}
	}
}

func TestShouldBootstrapWordpressForBackendDefaultsToTrue(t *testing.T) {
	cases := map[string]struct {
		envValue string
		want     bool
	}{
		"unset":      {envValue: "", want: true},
		"zero":       {envValue: "0", want: true},
		"false":      {envValue: "false", want: true},
		"mixed case": {envValue: "False", want: true},
		"one":        {envValue: "1", want: false},
		"true":       {envValue: "true", want: false},
		"yes":        {envValue: "yes", want: false},
		"random":     {envValue: "anything", want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OT_BACKEND_SKIP_WORDPRESS", tc.envValue)
			if got := shouldBootstrapWordpressForBackend(); got != tc.want {
				t.Fatalf("env=%q got %v want %v", tc.envValue, got, tc.want)
			}
		})
	}
}

func TestWordpressFixtureAliveProbesPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test url: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse test port: %v", err)
	}

	if !wordpressFixtureAlive(context.Background(), port) {
		t.Fatalf("expected live probe to succeed against test server on port %d", port)
	}

	server.Close()
	if wordpressFixtureAlive(context.Background(), port) {
		t.Fatalf("expected probe to fail after server close")
	}

	if wordpressFixtureAlive(context.Background(), 0) {
		t.Fatalf("port 0 should never be considered alive")
	}
}
