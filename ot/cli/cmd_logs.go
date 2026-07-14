package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/config"
	"github.com/collinbentley1/cli/ot/overmind"
	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
)

func logsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [process]",
		Short: "Stream logs from dev stack or DB tunnels",
		Long: `Stream logs from the dev stack or DB tunnel processes.

Without arguments, streams all dev stack logs interleaved.
With a process name, streams only that process's logs.

Examples:
  ot logs           # all dev stack logs
  ot logs backend        # backend web + worker logs
  ot logs backend-worker # just backend worker logs
  ot logs crm            # CRM web + worker logs
  ot logs frontend       # frontend logs (all frontends)
  ot logs wordpress      # local WordPress fixture logs
  ot logs db             # DB tunnel logs`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			if len(args) > 0 && args[0] == "db" {
				return streamDBLogs(cmd.Context(), rt)
			}

			if len(args) > 0 && args[0] == "frontend" {
				return logsFrontend(cmd, rt)
			}

			if len(args) > 0 && args[0] == "wordpress" {
				return logsWordpress(cmd, rt)
			}

			if len(args) > 0 && (args[0] == "crm" || args[0] == "crm-web" || args[0] == "crm-worker") {
				return logsCRM(cmd, rt, args[0])
			}

			if len(args) > 0 && (args[0] == "backend" || args[0] == "backend-worker") {
				return logsBackend(cmd, rt, args[0])
			}

			if len(args) > 0 {
				return fmt.Errorf("unknown process: %s (valid: backend, backend-worker, crm, frontend, wordpress, db)", args[0])
			}

			st, err := overmind.Stack(rt.Config, "dev")
			if err != nil {
				return err
			}
			if st.Procfile == "" {
				procfile, err := ensureProcfile(rt, "dev")
				if err != nil {
					return err
				}
				st.Procfile = procfile
			}

			socket := filepath.Join(rt.RepoRoot, st.Socket)
			if !overmind.SocketAlive(socket) {
				return fmt.Errorf("dev stack not running; run 'ot up' first")
			}

			sessionName := overmind.StackTitle(st, rt.RepoRoot)
			tmuxSocket := findTmuxSocket(sessionName)
			if tmuxSocket != "" {
				for _, proc := range []string{"backend", "backend-worker", "frontend"} {
					showRecentLogs(tmuxSocket, sessionName, proc)
				}
				fmt.Println("--- live logs (Ctrl-C to stop) ---")
				fmt.Println()
			}

			if len(args) > 0 {
				return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
					Program: "bash",
					Args: []string{
						"-c",
						fmt.Sprintf(
							"overmind echo -s %s | grep --line-buffered '^%s '",
							socket,
							args[0],
						),
					},
					Dir: rt.RepoRoot,
					Env: map[string]string{"OVERMIND_SKIP_ENV": "1"},
				})
			}

			return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
				Program: "overmind",
				Args:    []string{"echo", "-s", socket},
				Dir:     rt.RepoRoot,
				Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
			})
		},
	}
	return cmd
}

func logsBackend(cmd *cobra.Command, rt *Runtime, target string) error {
	st, socket, err := stackWithProcfile(rt, "backend")
	if err == nil && overmind.SocketAlive(socket) {
		return streamBackendLogs(cmd, rt, st, socket, target)
	}

	st, err = overmind.Stack(rt.Config, "dev")
	if err != nil {
		return err
	}
	if st.Procfile == "" {
		procfile, err := ensureProcfile(rt, "dev")
		if err != nil {
			return err
		}
		st.Procfile = procfile
	}
	socket = filepath.Join(rt.RepoRoot, st.Socket)
	if !overmind.SocketAlive(socket) {
		return fmt.Errorf("backend not running; run 'ot up backend' or 'ot up' first")
	}
	return streamBackendLogs(cmd, rt, st, socket, target)
}

func streamBackendLogs(
	cmd *cobra.Command,
	rt *Runtime,
	st config.OvermindStack,
	socket string,
	target string,
) error {
	if target == "backend-worker" {
		return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
			Program: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("overmind echo -s %s | grep --line-buffered '^backend-worker '", socket),
			},
			Dir: rt.RepoRoot,
			Env: map[string]string{"OVERMIND_SKIP_ENV": "1"},
		})
	}

	if strings.TrimSuffix(filepath.Base(filepath.FromSlash(st.Procfile)), ".procfile") == "backend" {
		return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
			Program: "overmind",
			Args:    []string{"echo", "-s", socket},
			Dir:     rt.RepoRoot,
			Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
		})
	}

	return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
		Program: "bash",
		Args: []string{
			"-c",
			fmt.Sprintf(
				"overmind echo -s %s | grep --line-buffered -E '^(backend|backend-worker|backend-mcp-tunnel) '",
				socket,
			),
		},
		Dir: rt.RepoRoot,
		Env: map[string]string{"OVERMIND_SKIP_ENV": "1"},
	})
}

func logsFrontend(cmd *cobra.Command, rt *Runtime) error {
	st, err := overmind.Stack(rt.Config, "frontend")
	if err != nil {
		return err
	}
	if st.Procfile == "" {
		procfile, err := ensureProcfile(rt, "frontend")
		if err != nil {
			return err
		}
		st.Procfile = procfile
	}
	socket := filepath.Join(rt.RepoRoot, st.Socket)
	if !overmind.SocketAlive(socket) {
		return fmt.Errorf("frontend not running; run 'ot up frontend' first")
	}

	sessionName := overmind.StackTitle(st, rt.RepoRoot)
	tmuxSocket := findTmuxSocket(sessionName)
	if tmuxSocket != "" {
		showRecentLogs(tmuxSocket, sessionName, "frontend")
		fmt.Println("--- live logs (Ctrl-C to stop) ---")
		fmt.Println()
	}

	return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
		Program: "bash",
		Args: []string{
			"-c",
			fmt.Sprintf(
				"overmind echo -s %s | grep --line-buffered '^frontend '",
				socket,
			),
		},
		Dir: rt.RepoRoot,
		Env: map[string]string{"OVERMIND_SKIP_ENV": "1"},
	})
}

func logsWordpress(cmd *cobra.Command, rt *Runtime) error {
	return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
		Program: "bash",
		Args:    []string{"ot/scripts/wordpress_dev.sh", "logs"},
		Dir:     rt.RepoRoot,
	})
}

func logsCRM(cmd *cobra.Command, rt *Runtime, target string) error {
	st, err := overmind.Stack(rt.Config, "crm")
	if err != nil {
		return err
	}
	if st.Procfile == "" {
		procfile, err := ensureProcfile(rt, "crm")
		if err != nil {
			return err
		}
		st.Procfile = procfile
	}
	socket := filepath.Join(rt.RepoRoot, st.Socket)
	if !overmind.SocketAlive(socket) {
		return fmt.Errorf("CRM not running; run 'ot up crm' first")
	}

	if target == "crm" {
		return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
			Program: "overmind",
			Args:    []string{"echo", "-s", socket},
			Dir:     rt.RepoRoot,
			Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
		})
	}

	return run.RunInteractiveWithSignals(cmd.Context(), run.Spec{
		Program: "bash",
		Args: []string{
			"-c",
			fmt.Sprintf("overmind echo -s %s | grep --line-buffered '^%s '", socket, target),
		},
		Dir: rt.RepoRoot,
		Env: map[string]string{"OVERMIND_SKIP_ENV": "1"},
	})
}

func streamDBLogs(ctx context.Context, rt *Runtime) error {
	st, err := overmind.Stack(rt.Config, "db")
	if err != nil {
		return err
	}

	socket := filepath.Join(rt.RepoRoot, st.Socket)
	if !overmind.SocketAlive(socket) {
		return fmt.Errorf("DB tunnels not running; run 'ot up db' first")
	}

	sessionName := overmind.StackTitle(st, rt.RepoRoot)
	tmuxSocket := findTmuxSocket(sessionName)
	if tmuxSocket != "" {
		for _, proc := range []string{"app"} {
			showRecentLogs(tmuxSocket, sessionName, proc)
		}
		fmt.Println("--- live logs (Ctrl-C to stop) ---")
		fmt.Println()
	}

	return run.RunInteractiveWithSignals(ctx, run.Spec{
		Program: "overmind",
		Args:    []string{"echo", "-s", socket},
		Dir:     rt.RepoRoot,
		Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
	})
}

func findTmuxSocket(sessionPrefix string) string {
	tmuxDir := fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
	entries, err := os.ReadDir(tmuxDir)
	if err != nil {
		return ""
	}
	prefix := "overmind-" + strings.ToLower(sessionPrefix) + "-"
	var newest string
	var newestTime int64
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().UnixNano() > newestTime {
				newestTime = info.ModTime().UnixNano()
				newest = e.Name()
			}
		}
	}
	if newest != "" {
		return newest
	}
	return ""
}

func showRecentLogs(tmuxSocket, sessionName, process string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "tmux",
		Args:    []string{"-L", tmuxSocket, "capture-pane", "-p", "-t", sessionName + ":" + process, "-S", "-50"},
	})
	if err != nil {
		return
	}
	lines := strings.TrimSpace(out)
	if lines != "" {
		fmt.Println(lines)
	}
}
