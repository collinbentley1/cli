package porter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/collinbentley1/cli/ot/config"
	"github.com/collinbentley1/cli/ot/providers"
	"github.com/collinbentley1/cli/ot/run"
)

func ConnectDatastore(ctx context.Context, repoRoot string, cfg *config.Config, env string, which string) error {
	if !providers.DBTunnelEnabled() {
		return errors.New("db tunnel provider is disabled (db_tunnel resolves to none — check OT_DB_TUNNEL_PROVIDER and providers.db_tunnel in ot/ot.yaml); enable porter or run your own tunnel")
	}
	datastore, port, err := resolve(cfg, env, which)
	if err != nil {
		return err
	}

	out, err := runConnect(ctx, repoRoot, datastore, port, which)
	if err == nil {
		return nil
	}
	if !isAuthError(out) {
		return err
	}

	if err := ensureLoggedIn(ctx, repoRoot); err != nil {
		return err
	}
	_, err = runConnect(ctx, repoRoot, datastore, port, which)
	return err
}

func resolve(cfg *config.Config, env string, which string) (string, int, error) {
	if cfg == nil {
		return "", 0, errors.New("nil config")
	}
	p, ok := cfg.Porter.Ports[which]
	if !ok || p <= 0 {
		return "", 0, fmt.Errorf("unknown db '%s' in ot/ot.yaml porter.ports", which)
	}
	envCfg, ok := cfg.Porter.Envs[env]
	if !ok {
		return "", 0, fmt.Errorf("unknown env '%s' (expected one of: %s)", env, strings.Join(keys(cfg.Porter.Envs), ", "))
	}
	switch which {
	case "app":
		if envCfg.AppDatastore == "" {
			return "", 0, fmt.Errorf("missing porter.envs.%s.app_datastore in ot/ot.yaml", env)
		}
		return envCfg.AppDatastore, p, nil
	default:
		return "", 0, fmt.Errorf("unknown db '%s'", which)
	}
}

func runConnect(ctx context.Context, repoRoot string, datastore string, port int, which string) (string, error) {
	spec := run.Spec{
		Program: "porter",
		Args:    []string{"datastore", "connect", datastore, "--port", fmt.Sprintf("%d", port)},
		Dir:     repoRoot,
	}

	connWritten := false
	onLine := func(line string) {
		if !connWritten && strings.Contains(line, "PGPASSWORD=") && strings.Contains(line, "psql") {
			writeConnectionInfo(repoRoot, which, line)
			connWritten = true
		}
	}

	out, err := run.RunTeeWithCallback(ctx, spec, 64*1024, onLine)
	return out, err
}

func writeConnectionInfo(repoRoot string, which string, line string) {
	// The captured line contains live datastore credentials (PGPASSWORD=...),
	// so keep the file and its directory owner-only, and tighten any
	// pre-existing world-readable file from earlier versions.
	connDir := filepath.Join(repoRoot, ".ot", "connections")
	if err := os.MkdirAll(connDir, 0o700); err != nil {
		return
	}
	connFile := filepath.Join(connDir, which+".txt")
	if err := os.WriteFile(connFile, []byte(strings.TrimSpace(line)+"\n"), 0o600); err != nil {
		return
	}
	_ = os.Chmod(connFile, 0o600)
}

func isAuthError(output string) bool {
	s := strings.ToLower(output)
	patterns := []string{
		"not logged in",
		"unauthorized",
		"forbidden",
		"authentication",
		"authenticate",
		"auth token",
		"token expired",
		"please login",
		"please log in",
		"run porter auth login",
	}
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func ensureLoggedIn(ctx context.Context, repoRoot string) error {
	lockPath := filepath.Join(repoRoot, ".ot", "locks", "porter-auth.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open auth lock: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock auth lock: %w", err)
	}
	defer func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }()

	if alreadyLoggedIn(ctx) {
		return nil
	}
	return run.RunInteractive(ctx, run.Spec{
		Program: "porter",
		Args:    []string{"auth", "login"},
		Dir:     repoRoot,
	})
}

func alreadyLoggedIn(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := run.RunCapture(cctx, run.Spec{Program: "porter", Args: []string{"projects", "list"}})
	return err == nil
}

func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Porter is the shipped db-tunnel provider implementation: it uses
// `porter datastore connect` to open an authenticated local tunnel to the
// remote datastore named in ot/ot.yaml.
type Porter struct{}

var _ providers.DBTunnel = Porter{}

func (Porter) Name() string { return providers.DBTunnelPorter }

func (Porter) Connect(ctx context.Context, repoRoot string, cfg *config.Config, env string, which string) error {
	return ConnectDatastore(ctx, repoRoot, cfg, env, which)
}
