package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunBackendMCPTunnelWaitsForCancellationAfterStartingFunnel(t *testing.T) {
	repoRoot := t.TempDir()
	tailscaleLog := filepath.Join(t.TempDir(), "tailscale.log")
	tailscaleBin := filepath.Join(t.TempDir(), "tailscale")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" = "status" ] && [ "$2" = "--json" ]; then
  echo '{"CertDomains":["demo-machine.example.ts.net."]}'
  exit 0
fi
if [ "$1" = "debug" ] && [ "$2" = "prefs" ]; then
  echo '{"CorpDNS":true}'
  exit 0
fi
if [ "$1" = "set" ]; then
  echo "set $2" >> %q
  exit 0
fi
if [ "$1" = "funnel" ] && [ "$2" = "--https=443" ] && [ "$3" = "off" ]; then
  echo "off" >> %q
  exit 0
fi
if [ "$1" = "funnel" ] && [ "$2" = "--bg" ] && [ "$3" = "--yes" ]; then
  echo "start $4" >> %q
  exit 0
fi
echo "unexpected tailscale args: $*" >&2
exit 1
`, tailscaleLog, tailscaleLog, tailscaleLog)
	if err := os.WriteFile(tailscaleBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tailscale: %v", err)
	}
	t.Setenv("TAILSCALE_BIN", tailscaleBin)
	t.Setenv("OT_BACKEND_MCP_TUNNEL", "")
	t.Setenv("OT_WORKTREE_ISOLATION_ACTIVE", "")
	t.Setenv("OT_LOCAL_INSTANCE_ID", "")
	t.Setenv("OT_PUBLIC_PATH_PREFIX", "")
	t.Setenv("OT_BACKEND_MCP_TUNNEL_PATH", "")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
		}),
	}
	go func() {
		_ = server.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runBackendMCPTunnel(ctx, repoRoot, fmt.Sprint(port))
	}()

	var publicURL string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		publicURL, err = readBackendMCPTunnelState(repoRoot)
		if err == nil && publicURL != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if publicURL == "" {
		cancel()
		t.Fatalf("backend MCP tunnel state was not written before timeout")
	}
	if publicURL != "https://demo-machine.example.ts.net/mcp" {
		cancel()
		t.Fatalf("public URL = %q, want %q", publicURL, "https://demo-machine.example.ts.net/mcp")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runBackendMCPTunnel() returned error after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runBackendMCPTunnel() did not return after context cancellation")
	}

	logData, err := os.ReadFile(tailscaleLog)
	if err != nil {
		t.Fatalf("read fake tailscale log: %v", err)
	}
	logText := string(logData)
	if !strings.Contains(logText, fmt.Sprintf("start http://127.0.0.1:%d", port)) {
		t.Fatalf("fake tailscale log missing start command:\n%s", logText)
	}
	if strings.Count(logText, "off") < 2 {
		t.Fatalf("fake tailscale log should include stop before and after running:\n%s", logText)
	}

	// MagicDNS lifecycle: suspended before the funnel starts (so the browser
	// resolves the funnel through public DNS), restored after cancellation
	// (prefs stub reports CorpDNS=true as the prior state).
	suspendIdx := strings.Index(logText, "set --accept-dns=false")
	startIdx := strings.Index(logText, "start http://127.0.0.1:")
	restoreIdx := strings.LastIndex(logText, "set --accept-dns=true")
	if suspendIdx == -1 {
		t.Fatalf("fake tailscale log missing MagicDNS suspend:\n%s", logText)
	}
	if restoreIdx == -1 {
		t.Fatalf("fake tailscale log missing MagicDNS restore:\n%s", logText)
	}
	if suspendIdx >= startIdx {
		t.Fatalf("MagicDNS must be suspended before the funnel starts:\n%s", logText)
	}
	if startIdx >= restoreIdx {
		t.Fatalf("MagicDNS must be restored after cancellation:\n%s", logText)
	}
	if _, err := os.Stat(backendMCPTunnelMagicDNSPath(repoRoot)); !os.IsNotExist(err) {
		t.Fatalf("MagicDNS state file should be cleared after restore, stat err = %v", err)
	}
}

func TestBackendMCPTunnelUsesIsolatedWorktreePathByDefault(t *testing.T) {
	t.Setenv("OT_WORKTREE_ISOLATION_ACTIVE", "1")
	t.Setenv("OT_LOCAL_INSTANCE_ID", "e43efd2031")
	t.Setenv("OT_BACKEND_MCP_TUNNEL", "")
	t.Setenv("OT_PUBLIC_PATH_PREFIX", "")
	t.Setenv("OT_BACKEND_MCP_TUNNEL_PATH", "")

	if backendMCPTunnelDisabled() {
		t.Fatal("expected backend MCP tunnel to be enabled for isolated worktrees by default")
	}
	if got, want := backendMCPTunnelMountPath(), "/_ot/e43efd2031"; got != want {
		t.Fatalf("backendMCPTunnelMountPath() = %q, want %q", got, want)
	}
	if got, want := backendMCPTunnelPublicURL("demo-machine.example.ts.net", backendMCPTunnelMountPath()), "https://demo-machine.example.ts.net/_ot/e43efd2031/mcp"; got != want {
		t.Fatalf("backendMCPTunnelPublicURL() = %q, want %q", got, want)
	}

	t.Setenv("OT_BACKEND_MCP_TUNNEL", "0")
	if !backendMCPTunnelDisabled() {
		t.Fatal("expected explicit OT_BACKEND_MCP_TUNNEL=0 to disable the tunnel")
	}
}

func TestStartTailscaleFunnelUsesSetPathForWorktreeMount(t *testing.T) {
	tailscaleLog := filepath.Join(t.TempDir(), "tailscale.log")
	tailscaleBin := filepath.Join(t.TempDir(), "tailscale")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
exit 0
`, tailscaleLog)
	if err := os.WriteFile(tailscaleBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tailscale: %v", err)
	}

	err := startTailscaleFunnel(
		context.Background(),
		tailscaleBin,
		"http://127.0.0.1:28065",
		"/_ot/e43efd2031",
	)
	if err != nil {
		t.Fatalf("startTailscaleFunnel() returned error: %v", err)
	}

	logData, err := os.ReadFile(tailscaleLog)
	if err != nil {
		t.Fatalf("read fake tailscale log: %v", err)
	}
	logText := string(logData)
	for _, want := range []string{
		"funnel --https=443 --set-path=/_ot/e43efd2031 off",
		"funnel --bg --yes --set-path=/_ot/e43efd2031 http://127.0.0.1:28065",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("fake tailscale log missing %q:\n%s", want, logText)
		}
	}
}

func TestBackendMCPTunnelDisabledByProviderSelection(t *testing.T) {
	t.Setenv("OT_BACKEND_MCP_TUNNEL", "")
	t.Setenv("OT_MCP_TUNNEL_PROVIDER", "none")

	if !backendMCPTunnelDisabled() {
		t.Fatal("expected providers.mcp_tunnel none to disable the backend MCP tunnel")
	}
	if reason := backendMCPTunnelDisabledReason(); !strings.Contains(reason, "provider selection") {
		t.Fatalf("backendMCPTunnelDisabledReason() = %q, want provider-selection reason", reason)
	}

	// Selecting the shipped implementation re-enables the tunnel.
	t.Setenv("OT_MCP_TUNNEL_PROVIDER", "tailscale")
	if backendMCPTunnelDisabled() {
		t.Fatal("expected tailscale provider selection to leave the tunnel enabled")
	}
}
