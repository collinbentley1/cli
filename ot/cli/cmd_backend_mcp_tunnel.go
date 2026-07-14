package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/collinbentley1/cli/ot/localdev"
	"github.com/collinbentley1/cli/ot/providers"
	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
)

const defaultTailscaleAppPath = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
const backendMCPTunnelStateFile = ".ot/connections/backend-mcp-tunnel.txt"
const backendMCPTunnelErrorFile = ".ot/connections/backend-mcp-tunnel-error.txt"
const backendMCPTunnelMagicDNSFile = ".ot/connections/backend-mcp-tunnel-magicdns.txt"

func backendMCPTunnelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "backend-mcp-tunnel",
		Short:  "Run the backend MCP public tunnel",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}
			appPort := getenvDefault("APP_PORT", "8005")
			return runBackendMCPTunnel(cmd.Context(), rt.RepoRoot, appPort)
		},
	}
	return cmd
}

func runBackendMCPTunnel(ctx context.Context, repoRoot string, appPort string) error {
	ctx, stopSignals := backendMCPTunnelContext(ctx)
	defer stopSignals()

	_ = clearBackendMCPTunnelState(repoRoot)
	if reason := backendMCPTunnelDisabledReason(); reason != "" {
		fmt.Printf("[backend-mcp-tunnel] %s\n", reason)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Hour):
			}
		}
	}

	tailscale, err := tailscaleBinary()
	if err != nil {
		return err
	}

	if err := waitForBackendHTTP(ctx, appPort, 2*time.Minute); err != nil {
		return err
	}

	domain, err := tailscaleHTTPSDomain(ctx, tailscale)
	if err != nil {
		return err
	}

	mountPath := backendMCPTunnelMountPath()
	target := fmt.Sprintf("http://127.0.0.1:%s", appPort)
	publicURL := backendMCPTunnelPublicURL(domain, mountPath)

	// Disable MagicDNS for the lifetime of the tunnel so the local browser
	// resolves the *.ts.net hostname through public DNS (Tailscale Funnel
	// ingress) instead of the CGNAT 100.x peer IP. Chrome's Local Network
	// Access policy blocks third-party iframes (Claude / ChatGPT widget
	// hosts) from fetching subresources off CGNAT addresses, so MagicDNS
	// resolution makes widget images fail in dev. We persist the prior
	// state so the restore-on-shutdown step can leave the engineer's
	// system the way we found it.
	priorAcceptDNS, magicDNSErr := suspendTailscaleMagicDNS(ctx, tailscale, repoRoot)
	if magicDNSErr != nil {
		fmt.Printf("[backend-mcp-tunnel] warning: could not suspend Tailscale MagicDNS: %v\n", magicDNSErr)
	}

	if err := startTailscaleFunnel(ctx, tailscale, target, mountPath); err != nil {
		_ = writeBackendMCPTunnelError(repoRoot, err.Error())
		_ = restoreTailscaleMagicDNS(context.Background(), tailscale, repoRoot, priorAcceptDNS)
		return err
	}
	if err := writeBackendMCPTunnelState(repoRoot, publicURL); err != nil {
		_ = stopTailscaleFunnel(context.Background(), tailscale, mountPath)
		_ = restoreTailscaleMagicDNS(context.Background(), tailscale, repoRoot, priorAcceptDNS)
		return err
	}

	fmt.Printf("[backend-mcp-tunnel] public MCP URL: %s\n", publicURL)
	fmt.Printf("[backend-mcp-tunnel] forwarding Tailscale Funnel to %s\n", target)

	<-ctx.Done()
	_ = stopTailscaleFunnel(context.Background(), tailscale, mountPath)
	_ = restoreTailscaleMagicDNS(context.Background(), tailscale, repoRoot, priorAcceptDNS)
	return nil
}

func backendMCPTunnelContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

func startTailscaleFunnel(ctx context.Context, tailscale string, target string, mountPath string) error {
	_ = stopTailscaleFunnel(ctx, tailscale, mountPath)

	args := []string{"funnel", "--bg", "--yes"}
	if mountPath = normalizeBackendMCPTunnelMountPath(mountPath); mountPath != "" {
		args = append(args, "--set-path="+mountPath)
	}
	args = append(args, target)
	output, err := run.RunCapture(ctx, run.Spec{
		Program: tailscale,
		Args:    args,
	})
	if err == nil {
		return nil
	}

	if strings.Contains(output, "Funnel is not enabled") {
		enableURL := findTailscaleFunnelEnableURL(output)
		if enableURL != "" {
			return fmt.Errorf("tailscale Funnel is not enabled for this tailnet; enable it at %s", enableURL)
		}
		return fmt.Errorf("tailscale Funnel is not enabled for this tailnet")
	}
	if strings.TrimSpace(output) != "" {
		return fmt.Errorf("start Tailscale Funnel: %w: %s", err, strings.TrimSpace(output))
	}
	return fmt.Errorf("start Tailscale Funnel: %w", err)
}

func stopTailscaleFunnel(ctx context.Context, tailscale string, mountPath string) error {
	args := []string{"funnel", "--https=443"}
	if mountPath = normalizeBackendMCPTunnelMountPath(mountPath); mountPath != "" {
		args = append(args, "--set-path="+mountPath)
	}
	args = append(args, "off")
	return run.RunSilent(ctx, run.Spec{
		Program: tailscale,
		Args:    args,
	})
}

func stopBackendMCPTunnel(ctx context.Context) {
	tailscale, err := tailscaleBinary()
	if err != nil {
		return
	}
	_ = stopTailscaleFunnel(ctx, tailscale, backendMCPTunnelMountPath())
	if rt, err := runtimeOrFail(ctx); err == nil {
		_ = restoreTailscaleMagicDNS(ctx, tailscale, rt.RepoRoot, "")
	}
}

// readTailscaleAcceptDNS returns "true" or "false" reflecting the current
// CorpDNS / --accept-dns preference, or an empty string if the value cannot
// be determined.
func readTailscaleAcceptDNS(ctx context.Context, tailscale string) (string, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: tailscale,
		Args:    []string{"debug", "prefs"},
	})
	if err != nil {
		return "", fmt.Errorf("read tailscale prefs: %w", err)
	}
	var prefs struct {
		CorpDNS bool `json:"CorpDNS"`
	}
	if err := json.Unmarshal([]byte(out), &prefs); err != nil {
		return "", fmt.Errorf("parse tailscale prefs: %w", err)
	}
	if prefs.CorpDNS {
		return "true", nil
	}
	return "false", nil
}

func setTailscaleAcceptDNS(ctx context.Context, tailscale string, accept bool) error {
	flag := "--accept-dns=true"
	if !accept {
		flag = "--accept-dns=false"
	}
	out, err := run.RunCapture(ctx, run.Spec{
		Program: tailscale,
		Args:    []string{"set", flag},
	})
	if err != nil {
		if strings.TrimSpace(out) != "" {
			return fmt.Errorf("tailscale set %s: %w: %s", flag, err, strings.TrimSpace(out))
		}
		return fmt.Errorf("tailscale set %s: %w", flag, err)
	}
	return nil
}

// suspendTailscaleMagicDNS records the current MagicDNS preference and
// disables it for the tunnel's lifetime. Returns the prior preference value
// ("true" / "false" / "") so the caller can restore it later.
//
// If a state file from a previous tunnel run still exists (e.g. the prior
// process was hard-killed before the restore step), prefer that value over
// the current pref — otherwise we would overwrite "true" with the now-stale
// "false" we ourselves wrote, and the engineer would never get MagicDNS
// re-enabled automatically.
func suspendTailscaleMagicDNS(ctx context.Context, tailscale string, repoRoot string) (string, error) {
	var prior string
	if stored, err := readBackendMCPTunnelMagicDNS(repoRoot); err == nil && stored != "" {
		prior = stored
	} else {
		current, readErr := readTailscaleAcceptDNS(ctx, tailscale)
		if readErr != nil {
			return "", readErr
		}
		prior = current
		if err := writeBackendMCPTunnelMagicDNS(repoRoot, prior); err != nil {
			return prior, err
		}
	}
	if err := setTailscaleAcceptDNS(ctx, tailscale, false); err != nil {
		return prior, err
	}
	fmt.Println("[backend-mcp-tunnel] disabled Tailscale MagicDNS for this tunnel session")
	return prior, nil
}

// restoreTailscaleMagicDNS re-enables MagicDNS if it was on before the tunnel
// started. The priorAcceptDNS argument is the value returned from
// suspendTailscaleMagicDNS; if empty, the function reads the persisted state
// file (used by stopBackendMCPTunnel which has no in-memory state).
func restoreTailscaleMagicDNS(ctx context.Context, tailscale string, repoRoot string, priorAcceptDNS string) error {
	prior := priorAcceptDNS
	if prior == "" {
		stored, err := readBackendMCPTunnelMagicDNS(repoRoot)
		if err != nil {
			return nil
		}
		prior = stored
	}
	defer func() { _ = clearBackendMCPTunnelMagicDNS(repoRoot) }()
	if prior != "true" {
		return nil
	}
	if err := setTailscaleAcceptDNS(ctx, tailscale, true); err != nil {
		fmt.Printf("[backend-mcp-tunnel] warning: could not re-enable Tailscale MagicDNS: %v\n", err)
		return err
	}
	fmt.Println("[backend-mcp-tunnel] re-enabled Tailscale MagicDNS")
	return nil
}

func findTailscaleFunnelEnableURL(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://login.tailscale.com/f/funnel") {
			return line
		}
	}
	return ""
}

func backendMCPTunnelDisabled() bool {
	return backendMCPTunnelDisabledReason() != ""
}

func backendMCPTunnelDisabledReason() string {
	value := strings.TrimSpace(strings.ToLower(os.Getenv("OT_BACKEND_MCP_TUNNEL")))
	switch value {
	case "0", "false", "off", "no":
		return "disabled by OT_BACKEND_MCP_TUNNEL=0"
	case "1", "true", "on", "yes":
		return ""
	}
	if !providers.MCPTunnelEnabled() {
		return "disabled by provider selection (providers.mcp_tunnel: none)"
	}
	return ""
}

func backendMCPTunnelMountPath() string {
	if prefix := normalizeBackendMCPTunnelMountPath(os.Getenv("OT_BACKEND_MCP_TUNNEL_PATH")); prefix != "" {
		return prefix
	}
	if prefix := normalizeBackendMCPTunnelMountPath(os.Getenv("OT_PUBLIC_PATH_PREFIX")); prefix != "" {
		return prefix
	}
	if localdev.Active() {
		if id := strings.TrimSpace(os.Getenv("OT_LOCAL_INSTANCE_ID")); id != "" {
			return "/_ot/" + id
		}
	}
	return ""
}

func normalizeBackendMCPTunnelMountPath(value string) string {
	path := strings.TrimSpace(value)
	if path == "" || path == "/" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return strings.TrimRight(path, "/")
}

func backendMCPTunnelPublicURL(domain string, mountPath string) string {
	origin := fmt.Sprintf("https://%s", strings.TrimRight(strings.TrimSpace(domain), "."))
	if path := normalizeBackendMCPTunnelMountPath(mountPath); path != "" {
		return origin + path + "/mcp"
	}
	return origin + "/mcp"
}

func tailscaleBinary() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("TAILSCALE_BIN")); configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("TAILSCALE_BIN is not usable: %w", err)
		}
		return configured, nil
	}
	if path, err := exec.LookPath("tailscale"); err == nil {
		return path, nil
	}
	if _, err := os.Stat(defaultTailscaleAppPath); err == nil {
		return defaultTailscaleAppPath, nil
	}
	return "", fmt.Errorf("tailscale CLI not found; install Tailscale or set TAILSCALE_BIN")
}

func waitForBackendHTTP(ctx context.Context, appPort string, timeout time.Duration) error {
	healthURL := fmt.Sprintf("http://127.0.0.1:%s/health", appPort)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
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
		return fmt.Errorf("backend did not become healthy for MCP tunnel at %s within %s: %w", healthURL, timeout, lastErr)
	}
	return fmt.Errorf("backend did not become healthy for MCP tunnel at %s within %s", healthURL, timeout)
}

func tailscaleHTTPSDomain(ctx context.Context, tailscale string) (string, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: tailscale,
		Args:    []string{"status", "--json"},
	})
	if err != nil {
		return "", fmt.Errorf("read tailscale status: %w", err)
	}

	var status struct {
		CertDomains []string `json:"CertDomains"`
		Self        struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		return "", fmt.Errorf("parse tailscale status: %w", err)
	}

	if len(status.CertDomains) > 0 {
		if domain := normalizeTailscaleDomain(status.CertDomains[0]); domain != "" {
			return domain, nil
		}
	}
	if domain := normalizeTailscaleDomain(status.Self.DNSName); domain != "" {
		return domain, nil
	}
	return "", fmt.Errorf("tailscale status did not include a DNS name for this machine")
}

func normalizeTailscaleDomain(domain string) string {
	return strings.TrimSuffix(strings.TrimSpace(domain), ".")
}

func backendMCPTunnelStatePath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(backendMCPTunnelStateFile))
}

func backendMCPTunnelErrorPath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(backendMCPTunnelErrorFile))
}

func writeBackendMCPTunnelState(repoRoot string, publicURL string) error {
	path := backendMCPTunnelStatePath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(publicURL+"\n"), 0o644)
}

func readBackendMCPTunnelState(repoRoot string) (string, error) {
	data, err := os.ReadFile(backendMCPTunnelStatePath(repoRoot))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func writeBackendMCPTunnelError(repoRoot string, message string) error {
	path := backendMCPTunnelErrorPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(message)+"\n"), 0o644)
}

func readBackendMCPTunnelError(repoRoot string) (string, error) {
	data, err := os.ReadFile(backendMCPTunnelErrorPath(repoRoot))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func clearBackendMCPTunnelState(repoRoot string) error {
	var firstErr error
	for _, path := range []string{
		backendMCPTunnelStatePath(repoRoot),
		backendMCPTunnelErrorPath(repoRoot),
	} {
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func backendMCPTunnelMagicDNSPath(repoRoot string) string {
	return filepath.Join(repoRoot, filepath.FromSlash(backendMCPTunnelMagicDNSFile))
}

func writeBackendMCPTunnelMagicDNS(repoRoot string, prior string) error {
	path := backendMCPTunnelMagicDNSPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.TrimSpace(prior)+"\n"), 0o644)
}

func readBackendMCPTunnelMagicDNS(repoRoot string) (string, error) {
	data, err := os.ReadFile(backendMCPTunnelMagicDNSPath(repoRoot))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func clearBackendMCPTunnelMagicDNS(repoRoot string) error {
	err := os.Remove(backendMCPTunnelMagicDNSPath(repoRoot))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// tailscaleFunnel is the shipped mcp-tunnel provider implementation: it
// publishes the local backend on the machine's public *.ts.net domain via
// Tailscale Funnel, path-mounted per worktree so concurrent worktrees each
// get their own live MCP URL.
type tailscaleFunnel struct{}

var _ providers.MCPTunnel = tailscaleFunnel{}

func (tailscaleFunnel) Name() string { return providers.MCPTunnelTailscale }

func (tailscaleFunnel) Domain(ctx context.Context) (string, error) {
	binary, err := tailscaleBinary()
	if err != nil {
		return "", err
	}
	return tailscaleHTTPSDomain(ctx, binary)
}

func (tailscaleFunnel) Start(ctx context.Context, target string, mountPath string) error {
	binary, err := tailscaleBinary()
	if err != nil {
		return err
	}
	return startTailscaleFunnel(ctx, binary, target, mountPath)
}

func (tailscaleFunnel) Stop(ctx context.Context, mountPath string) error {
	binary, err := tailscaleBinary()
	if err != nil {
		return err
	}
	return stopTailscaleFunnel(ctx, binary, mountPath)
}
