package crm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/collinbentley1/cli/ot/dbinit"
	"github.com/collinbentley1/cli/ot/run"
)

type Options struct {
	RepoRoot string
	Quiet    bool
}

func Prepare(ctx context.Context, opts Options) error {
	crmDir := filepath.Join(opts.RepoRoot, "crm")
	containerName := getenvDefault("CRM_DB_CONTAINER", "crm-postgres")
	hostPort, err := preferredHostPort(ctx, containerName, opts.Quiet)
	if err != nil {
		return err
	}

	initDBURL := strings.TrimSpace(os.Getenv("LOCAL_PRIMARY_CRM_PGSQL_INIT_DB_URL"))
	if initDBURL == "" {
		logf(opts.Quiet, "[crm] LOCAL_PRIMARY_CRM_PGSQL_INIT_DB_URL not set; using local default.\n")
		initDBURL = defaultInitDBURL(hostPort)
	}

	appDBURL := strings.TrimSpace(os.Getenv("LOCAL_PRIMARY_CRM_PGSQL_DB_URL"))
	if appDBURL == "" {
		logf(opts.Quiet, "[crm] LOCAL_PRIMARY_CRM_PGSQL_DB_URL not set; using local default.\n")
		appDBURL = defaultAppDBURL(hostPort)
	}

	_, err = ensureDatabase(ctx, opts.RepoRoot, initDBURL, appDBURL, opts.Quiet)
	if err != nil {
		return err
	}

	setDefaultEnv("CRM_ENVIRONMENT", "development")
	setDefaultEnv("CRM_HOST", "127.0.0.1")
	setDefaultEnv("CRM_PORT", "8006")
	if strings.TrimSpace(os.Getenv("CRM_PUBLIC_BASE_URL")) == "" {
		host := strings.TrimSpace(os.Getenv("CRM_HOST"))
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		port := strings.TrimSpace(os.Getenv("CRM_PORT"))
		if port == "" {
			port = "8006"
		}
		setDefaultEnv("CRM_PUBLIC_BASE_URL", fmt.Sprintf("http://%s:%s", host, port))
	}
	setDefaultEnv("CRM_MCP_HOST", "127.0.0.1")
	setDefaultEnv("CRM_MCP_PORT", "8007")
	if strings.TrimSpace(os.Getenv("CRM_MCP_PUBLIC_BASE_URL")) == "" {
		host := strings.TrimSpace(os.Getenv("CRM_HOST"))
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		port := strings.TrimSpace(os.Getenv("CRM_PORT"))
		if port == "" {
			port = "8006"
		}
		setDefaultEnv("CRM_MCP_PUBLIC_BASE_URL", fmt.Sprintf("http://%s:%s/mcp", host, port))
	}
	if err := os.Setenv("LOCAL_PRIMARY_CRM_PGSQL_DB_URL", appDBURL); err != nil {
		return fmt.Errorf("set LOCAL_PRIMARY_CRM_PGSQL_DB_URL: %w", err)
	}
	setDefaultEnv("PYTHONUNBUFFERED", "1")

	migrationSpec := run.Spec{
		Program: "uv",
		Args:    []string{"run", "python", "-m", "scripts.migrate"},
		Dir:     crmDir,
	}
	if opts.Quiet {
		out, err := run.RunCapture(ctx, migrationSpec)
		if err != nil {
			out = strings.TrimSpace(out)
			if out == "" {
				return fmt.Errorf("run CRM migrations: %w", err)
			}
			return fmt.Errorf("run CRM migrations: %w\n%s", err, out)
		}
	} else if err := run.RunInteractive(ctx, migrationSpec); err != nil {
		return fmt.Errorf("run CRM migrations: %w", err)
	}

	return nil
}

func ensureDatabase(ctx context.Context, repoRoot string, initDBURL string, appDBURL string, quiet bool) (string, error) {
	name := getenvDefault("CRM_DB_CONTAINER", "crm-postgres")
	hostPort := getenvDefault("CRM_DB_PORT", "5434")
	image := getenvDefault("CRM_DB_IMAGE", "postgres:18")
	if err := dbinit.RequireExplicitPort(initDBURL, "LOCAL_PRIMARY_CRM_PGSQL_INIT_DB_URL"); err != nil {
		return "", err
	}
	if err := dbinit.RequireExplicitPort(appDBURL, "LOCAL_PRIMARY_CRM_PGSQL_DB_URL"); err != nil {
		return "", err
	}
	initCfg, err := dbinit.ParseURL(initDBURL, hostPort)
	if err != nil {
		return "", err
	}
	appCfg, err := dbinit.ParseURL(appDBURL, hostPort)
	if err != nil {
		return "", err
	}
	hostPort = initCfg.Port

	existed, err := dbinit.EnsureContainer(ctx, repoRoot, name, image, hostPort, initCfg, quiet)
	if err != nil {
		return "", err
	}
	if existed {
		if err := dbinit.EnsureRdsRolesAndDb(ctx, name, initCfg, appCfg, quiet); err != nil {
			return "", err
		}
		return hostPort, nil
	}

	if err := dbinit.WaitForPostgres(ctx, name, initCfg.User, initCfg.Database, initCfg.Port); err != nil {
		return "", err
	}

	if err := dbinit.EnsureRdsRolesAndDb(ctx, name, initCfg, appCfg, quiet); err != nil {
		return "", err
	}
	return hostPort, nil
}

func preferredHostPort(ctx context.Context, containerName string, quiet bool) (string, error) {
	if explicit := strings.TrimSpace(os.Getenv("CRM_DB_PORT")); explicit != "" {
		return explicit, nil
	}

	exists, err := dbinit.ContainerExists(ctx, containerName)
	if err != nil {
		return "", err
	}
	if !exists {
		return "5434", nil
	}

	out, err := run.RunCapture(ctx, run.Spec{
		Program: "docker",
		Args: []string{
			"inspect",
			containerName,
			"--format",
			`{{with index .HostConfig.PortBindings "5432/tcp"}}{{(index . 0).HostPort}}{{end}}`,
		},
	})
	if err != nil {
		return "", err
	}
	hostPort := strings.TrimSpace(out)
	if hostPort == "" {
		return "5434", nil
	}
	logf(quiet, "[crm] Reusing existing %s host port %s.\n", containerName, hostPort)
	return hostPort, nil
}

func setDefaultEnv(key, value string) {
	if strings.TrimSpace(os.Getenv(key)) == "" {
		_ = os.Setenv(key, value)
	}
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func logf(quiet bool, format string, args ...any) {
	if quiet {
		return
	}
	fmt.Printf(format, args...)
}

func defaultInitDBURL(port string) string {
	return fmt.Sprintf("postgresql://crm:crm@127.0.0.1:%s/postgres", port)
}

func defaultAppDBURL(port string) string {
	return fmt.Sprintf("postgresql+psycopg://crm:crm@127.0.0.1:%s/crm", port)
}
