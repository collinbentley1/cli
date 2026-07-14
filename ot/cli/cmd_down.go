package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/collinbentley1/cli/ot/backend"
	"github.com/collinbentley1/cli/ot/docker"
	"github.com/collinbentley1/cli/ot/localdev"
	"github.com/collinbentley1/cli/ot/overmind"
	"github.com/collinbentley1/cli/ot/run"
	"github.com/spf13/cobra"
)

func downCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down [service]",
		Short: "Stop dev stack or specific services",
		Long: `Stop the dev stack or specific services.

Without arguments, stops the dev stack and Docker containers.
With a service name, stops just that service.

Services:
  auth            Stop auth process
  backend         Stop backend web + worker processes
  backend-worker  Stop backend worker process
  crm             Stop CRM stack
  frontend        Stop frontend process
  wordpress       Stop local WordPress fixture site
  db              Stop DB tunnels

Examples:
  ot down                 # stop dev stack
  ot down auth            # stop auth process
  ot down backend         # stop backend web + worker
  ot down backend-worker  # stop backend worker
  ot down db              # stop DB tunnels
  ot down wordpress       # stop local WordPress fixture site
  ot down frontend        # stop frontend`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeOrFail(cmd.Context())
			if err != nil {
				return err
			}

			if len(args) == 0 {
				return downDevStack(cmd, rt)
			}

			switch args[0] {
			case "auth":
				if err := backend.StopAuth(cmd.Context(), rt.RepoRoot); err != nil {
					return err
				}
				fmt.Println("auth stopped.")
				return nil
			case "backend":
				_ = clearBackendMCPTunnelState(rt.RepoRoot)
				stopBackendMCPTunnel(cmd.Context())
				_ = downProcess(cmd, rt, "backend-worker")
				_ = downBackendStack(cmd, rt)
				_ = downProcess(cmd, rt, "backend")
				if err := backend.Stop(cmd.Context(), rt.RepoRoot); err != nil &&
					!strings.Contains(err.Error(), "not running") {
					return err
				}
				fmt.Println("backend stopped.")
				return nil
			case "backend-worker":
				if err := downProcess(cmd, rt, "backend-worker"); err == nil {
					return nil
				}
				if err := downBackendStackProcess(cmd, rt, "backend-worker"); err != nil {
					return err
				}
				fmt.Println("backend-worker stopped.")
				return nil
			case "crm":
				if len(args) > 1 {
					return fmt.Errorf("unknown service: %s (valid: auth, backend, backend-worker, crm, frontend, wordpress, db)", strings.Join(args, " "))
				}
				return downCRM(cmd, rt)
			case "frontend":
				if len(args) > 1 {
					return fmt.Errorf("unknown service: %s (valid: auth, backend, backend-worker, crm, frontend, wordpress, db)", strings.Join(args, " "))
				}
				return downFrontend(cmd, rt)
			case "wordpress":
				if len(args) > 1 {
					return fmt.Errorf("unknown service: %s (valid: auth, backend, backend-worker, crm, frontend, wordpress, db)", strings.Join(args, " "))
				}
				return downWordpress(cmd, rt)
			case "db":
				return downDB(cmd, rt)
			default:
				return fmt.Errorf("unknown service: %s (valid: auth, backend, backend-worker, crm, frontend, wordpress, db)", args[0])
			}
		},
	}
	return cmd
}

func downDevStack(cmd *cobra.Command, rt *Runtime) error {
	_ = clearBackendMCPTunnelState(rt.RepoRoot)
	stopBackendMCPTunnel(cmd.Context())
	for _, stackName := range []string{"dev", "backend", "crm"} {
		st, err := overmind.Stack(rt.Config, stackName)
		if err != nil {
			return err
		}
		if st.Procfile == "" {
			procfile, err := ensureProcfile(rt, stackName)
			if err != nil {
				return err
			}
			st.Procfile = procfile
		}
		socket := filepath.Join(rt.RepoRoot, st.Socket)
		if !overmind.SocketAlive(socket) {
			continue
		}
		if err := overmind.Quit(cmd.Context(), rt.RepoRoot, st); err != nil {
			return err
		}
	}
	if err := docker.StopContainers(cmd.Context(), rt.RepoRoot, dockerContainersForRuntime(rt)); err != nil {
		return err
	}
	fmt.Println("Dev stack stopped.")
	return nil
}

func dockerContainersForRuntime(rt *Runtime) []string {
	if localdev.Active() {
		return compactStrings([]string{
			os.Getenv("APP_DB_CONTAINER"),
			os.Getenv("CRM_DB_CONTAINER"),
		})
	}
	if rt == nil || rt.Config == nil {
		return nil
	}
	return rt.Config.Docker.StopByDefault
}

func compactStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	return out
}

func downDB(cmd *cobra.Command, rt *Runtime) error {
	st, err := overmind.Stack(rt.Config, "db")
	if err != nil {
		return err
	}
	if st.Procfile == "" {
		procfile, err := ensureProcfile(rt, "db")
		if err != nil {
			return err
		}
		st.Procfile = procfile
	}
	socket := filepath.Join(rt.RepoRoot, st.Socket)
	if !overmind.SocketAlive(socket) {
		return nil
	}
	if err := overmind.Quit(cmd.Context(), rt.RepoRoot, st); err != nil {
		return err
	}
	fmt.Println("DB tunnels stopped.")
	return nil
}

func downCRM(cmd *cobra.Command, rt *Runtime) error {
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
		return nil
	}
	if err := overmind.Quit(cmd.Context(), rt.RepoRoot, st); err != nil {
		return err
	}
	fmt.Println("CRM stopped.")
	return nil
}

func downBackendStack(cmd *cobra.Command, rt *Runtime) error {
	st, socket, err := stackWithProcfile(rt, "backend")
	if err != nil {
		return err
	}
	if !overmind.SocketAlive(socket) {
		return nil
	}
	if err := overmind.Quit(cmd.Context(), rt.RepoRoot, st); err != nil {
		return err
	}
	return nil
}

func downBackendStackProcess(cmd *cobra.Command, rt *Runtime, name string) error {
	_, socket, err := stackWithProcfile(rt, "backend")
	if err != nil {
		return err
	}
	if !overmind.SocketAlive(socket) {
		return fmt.Errorf("backend stack not running; run 'ot up backend' first")
	}
	if err := run.RunInteractive(cmd.Context(), run.Spec{
		Program: "overmind",
		Args:    []string{"stop", "-s", socket, name},
		Dir:     rt.RepoRoot,
		Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
	}); err != nil {
		return err
	}
	return nil
}

func downProcess(cmd *cobra.Command, rt *Runtime, name string) error {
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
	if err := run.RunInteractive(cmd.Context(), run.Spec{
		Program: "overmind",
		Args:    []string{"stop", "-s", socket, name},
		Dir:     rt.RepoRoot,
		Env:     map[string]string{"OVERMIND_SKIP_ENV": "1"},
	}); err != nil {
		return err
	}
	fmt.Printf("%s stopped.\n", name)
	return nil
}

func downFrontend(cmd *cobra.Command, rt *Runtime) error {
	st, socket, err := stackWithProcfile(rt, "frontend")
	if err != nil {
		return err
	}
	if !overmind.SocketAlive(socket) {
		return fmt.Errorf("frontend not running; run 'ot up frontend' first")
	}
	if err := overmind.Quit(cmd.Context(), rt.RepoRoot, st); err != nil {
		return err
	}
	fmt.Println("frontend stopped.")
	return nil
}

func downWordpress(cmd *cobra.Command, rt *Runtime) error {
	if err := run.RunInteractive(cmd.Context(), run.Spec{
		Program: "bash",
		Args:    []string{"ot/scripts/wordpress_dev.sh", "stop"},
		Dir:     rt.RepoRoot,
	}); err != nil {
		return err
	}
	return nil
}
