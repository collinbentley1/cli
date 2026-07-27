package overmind

import (
	"context"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/config"
	"github.com/collinbentley1/cli/ot/run"
)

func SocketAlive(socketPath string) bool {
	c, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func EnsureStarted(ctx context.Context, repoRoot string, st config.OvermindStack, extraEnv map[string]string) error {
	return ensureStartedInternal(ctx, repoRoot, st, extraEnv, false)
}

func EnsureStartedSilent(ctx context.Context, repoRoot string, st config.OvermindStack, extraEnv map[string]string) error {
	return ensureStartedInternal(ctx, repoRoot, st, extraEnv, true)
}

func ensureStartedInternal(ctx context.Context, repoRoot string, st config.OvermindStack, extraEnv map[string]string, silent bool) error {
	procfile := filepath.Join(repoRoot, filepath.FromSlash(st.Procfile))
	socket := filepath.Join(repoRoot, filepath.FromSlash(st.Socket))

	if SocketAlive(socket) {
		return nil
	}

	if _, err := os.Stat(socket); err == nil {
		_ = os.Remove(socket)
	}
	cleanupStaleSessions(ctx, StackTitle(st, repoRoot))

	args := []string{"start", "-D", "-N", "-d", repoRoot, "-f", procfile, "-s", socket, "-w", StackTitle(st, repoRoot)}
	procName := strings.TrimSuffix(filepath.Base(procfile), ".procfile")
	if strings.HasPrefix(procName, "frontend") {
		args = append(args, "-r", "all")
	}
	if len(st.CanDie) > 0 {
		args = append(args, "-c", strings.Join(st.CanDie, ","))
	}

	env := map[string]string{
		"OVERMIND_SKIP_ENV": "1",
	}
	maps.Copy(env, extraEnv)

	spec := run.Spec{
		Program: "overmind",
		Args:    args,
		Dir:     repoRoot,
		Env:     env,
	}

	var err error
	if silent {
		err = run.RunSilent(ctx, spec)
	} else {
		err = run.RunInteractive(ctx, spec)
	}
	if err != nil {
		return err
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if SocketAlive(socket) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !SocketAlive(socket) {
		return fmt.Errorf("overmind socket not ready after 10s: %s", socket)
	}

	for range 4 {
		time.Sleep(500 * time.Millisecond)
		if !SocketAlive(socket) {
			return fmt.Errorf("stack started but crashed immediately; run 'ot up backend' or 'ot up frontend' to debug")
		}
	}

	return nil
}

func StackTitle(st config.OvermindStack, repoRoot string) string {
	title := filepath.Base(repoRoot)
	if strings.TrimSpace(os.Getenv("OT_WORKTREE_ISOLATION_ACTIVE")) == "1" {
		if id := strings.TrimSpace(os.Getenv("OT_LOCAL_INSTANCE_ID")); id != "" {
			title = title + "-" + id
		}
	}
	if st.Procfile != "" {
		if name := strings.TrimSuffix(filepath.Base(filepath.FromSlash(st.Procfile)), ".procfile"); name != "" && name != "dev" {
			title = title + "-" + name
		}
	}
	return title
}

func cleanupStaleSessions(ctx context.Context, title string) {
	tmuxDir := fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
	entries, err := os.ReadDir(tmuxDir)
	if err != nil {
		return
	}
	prefix := "overmind-" + strings.ToLower(title) + "-"
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		_ = run.RunSilent(ctx, run.Spec{
			Program: "tmux",
			Args:    []string{"-L", e.Name(), "kill-session", "-t", title},
		})
	}
}

func Quit(ctx context.Context, repoRoot string, st config.OvermindStack) error {
	socket := filepath.Join(repoRoot, filepath.FromSlash(st.Socket))
	if !SocketAlive(socket) {
		if _, err := os.Stat(socket); err == nil {
			_ = os.Remove(socket)
		}
		return nil
	}

	return run.RunInteractive(ctx, run.Spec{
		Program: "overmind",
		Args:    []string{"quit", "-s", socket},
		Dir:     repoRoot,
		Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
	})
}

func Connect(ctx context.Context, repoRoot string, st config.OvermindStack, process string) error {
	socket := filepath.Join(repoRoot, filepath.FromSlash(st.Socket))
	args := []string{"connect", "-s", socket}
	if process != "" {
		args = append(args, process)
	}

	return run.RunInteractive(ctx, run.Spec{
		Program: "overmind",
		Args:    args,
		Dir:     repoRoot,
		Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
	})
}

func Stack(cfg *config.Config, name string) (config.OvermindStack, error) {
	st, ok := cfg.Overmind.Stacks[name]
	if !ok {
		return config.OvermindStack{}, fmt.Errorf("unknown overmind stack '%s' in ot/ot.yaml", name)
	}
	return st, nil
}
