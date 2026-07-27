package dbinit

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/run"
)

type Config struct {
	User     string
	Password string
	Database string
	Port     string
}

func RequireExplicitPort(rawURL string, envName string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse %s: %w", envName, err)
	}
	if strings.TrimSpace(parsed.Port()) == "" {
		return fmt.Errorf("%s must include an explicit port", envName)
	}
	return nil
}

func ParseURL(rawURL string, fallbackPort string) (Config, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Config{}, fmt.Errorf("parse postgres url: %w", err)
	}
	if parsed.User == nil {
		return Config{}, errors.New("postgres url missing user info")
	}
	user := parsed.User.Username()
	if user == "" {
		return Config{}, errors.New("postgres url missing user")
	}
	password, hasPassword := parsed.User.Password()
	if !hasPassword || password == "" {
		return Config{}, errors.New("postgres url missing password")
	}
	database := strings.TrimPrefix(parsed.Path, "/")
	if database == "" {
		return Config{}, errors.New("postgres url missing database")
	}
	port := parsed.Port()
	if port == "" {
		port = fallbackPort
	}
	if port == "" {
		return Config{}, errors.New("postgres url missing port")
	}
	return Config{
		User:     user,
		Password: password,
		Database: database,
		Port:     port,
	}, nil
}

func EnsureContainer(ctx context.Context, repoRoot, name, image, hostPort string, initCfg Config, quiet bool) (bool, error) {
	running, err := ContainerRunning(ctx, name)
	if err != nil {
		return false, err
	}
	exists, err := ContainerExists(ctx, name)
	if err != nil {
		return false, err
	}
	if exists && !running {
		if err := runQuietMaybeInteractive(ctx, quiet, run.Spec{
			Program: "docker",
			Args:    []string{"start", name},
			Dir:     repoRoot,
		}, "docker start"); err != nil {
			return false, err
		}
	}
	if exists {
		// Probe readiness instead of sleeping: a restarted postgres in
		// crash recovery can need well over a fixed delay, and callers
		// run psql immediately on the existed path. Returns quickly when
		// the container was already running and ready.
		if err := WaitForPostgres(ctx, name, initCfg.User, initCfg.Database, hostPort); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := runQuietMaybeInteractive(ctx, quiet, run.Spec{
		Program: "docker",
		Args:    postgresRunArgs(name, image, hostPort, initCfg),
		Dir:     repoRoot,
	}, "docker run"); err != nil {
		return false, err
	}
	return false, nil
}

func postgresRunArgs(name, image, hostPort string, initCfg Config) []string {
	args := []string{
		"run",
		"--name", name,
		"-e", "POSTGRES_USER=" + initCfg.User,
		"-e", "POSTGRES_PASSWORD=" + initCfg.Password,
		"-e", "POSTGRES_DB=" + initCfg.Database,
	}
	if useHostNetworkPostgres() {
		return append(args,
			"--network", "host",
			"-d", image,
			"postgres",
			"-c", "listen_addresses=*",
			"-c", "port="+hostPort,
		)
	}
	return append(args,
		"-p", hostPort+":5432",
		"-d", image,
	)
}

func useHostNetworkPostgres() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OT_DOCKER_NETWORK_MODE")), "host")
}

func containerPostgresPort(hostPort string) string {
	if useHostNetworkPostgres() && strings.TrimSpace(hostPort) != "" {
		return hostPort
	}
	return "5432"
}

func WaitForPostgres(ctx context.Context, container, user, database, hostPort string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := run.RunSilent(ctx, run.Spec{
			Program: "docker",
			Args:    []string{"exec", container, "pg_isready", "-U", user, "-d", database, "-p", containerPostgresPort(hostPort)},
		}); err == nil {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return errors.New("postgres did not become ready")
}

func EnsureRdsRolesAndDb(ctx context.Context, container string, initCfg, appCfg Config, quiet bool) error {
	rdsAdminRole := quoteIdent(initCfg.User)
	postgresRole := quoteIdent(appCfg.User)
	quotedDB := quoteIdent(appCfg.Database)
	rdsPassword := quoteLiteral(initCfg.Password)
	postgresPassword := quoteLiteral(appCfg.Password)
	adminDB := initCfg.Database

	if !strings.EqualFold(initCfg.User, "postgres") {
		rdsAdminSQL := fmt.Sprintf(
			"DO $ot_init$BEGIN "+
				"IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %s) THEN "+
				"CREATE ROLE %s LOGIN PASSWORD %s SUPERUSER CREATEROLE CREATEDB REPLICATION BYPASSRLS INHERIT; "+
				"ELSE "+
				"ALTER ROLE %s LOGIN PASSWORD %s SUPERUSER CREATEROLE CREATEDB REPLICATION BYPASSRLS INHERIT; "+
				"END IF; "+
				"END$ot_init$;",
			quoteLiteral(initCfg.User),
			rdsAdminRole,
			rdsPassword,
			rdsAdminRole,
			rdsPassword,
		)
		if err := runPSQL(ctx, container, initCfg.User, adminDB, initCfg.Port, rdsAdminSQL, quiet); err != nil {
			return err
		}
	}

	if strings.EqualFold(appCfg.User, initCfg.User) {
		passwordSQL := fmt.Sprintf(
			"ALTER ROLE %s LOGIN PASSWORD %s;",
			rdsAdminRole,
			quoteLiteral(appCfg.Password),
		)
		if err := runPSQL(ctx, container, initCfg.User, adminDB, initCfg.Port, passwordSQL, quiet); err != nil {
			return err
		}
		exists, err := databaseExists(ctx, container, initCfg.User, adminDB, appCfg.Database, initCfg.Port)
		if err != nil {
			return err
		}
		if !exists {
			return runPSQL(ctx, container, initCfg.User, adminDB, initCfg.Port, createDatabaseSQL(quotedDB, rdsAdminRole), quiet)
		}
		return runPSQL(ctx, container, initCfg.User, adminDB, initCfg.Port, alterDatabaseOwnerSQL(quotedDB, rdsAdminRole), quiet)
	}

	postgresSQL := fmt.Sprintf(
		"DO $ot_init$BEGIN "+
			"IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = %s) THEN "+
			"CREATE ROLE %s LOGIN PASSWORD %s NOSUPERUSER CREATEROLE CREATEDB NOREPLICATION NOBYPASSRLS INHERIT; "+
			"ELSE "+
			"ALTER ROLE %s LOGIN PASSWORD %s NOSUPERUSER CREATEROLE CREATEDB NOREPLICATION NOBYPASSRLS INHERIT; "+
			"END IF; "+
			"END$ot_init$;",
		quoteLiteral(appCfg.User),
		postgresRole,
		postgresPassword,
		postgresRole,
		postgresPassword,
	)
	if err := runPSQL(ctx, container, initCfg.User, adminDB, initCfg.Port, postgresSQL, quiet); err != nil {
		return err
	}

	exists, err := databaseExists(ctx, container, initCfg.User, adminDB, appCfg.Database, initCfg.Port)
	if err != nil {
		return err
	}
	if !exists {
		return runPSQL(ctx, container, initCfg.User, adminDB, initCfg.Port, createDatabaseSQL(quotedDB, postgresRole), quiet)
	}
	return runPSQL(ctx, container, initCfg.User, adminDB, initCfg.Port, alterDatabaseOwnerSQL(quotedDB, postgresRole), quiet)
}

func createDatabaseSQL(dbName, owner string) string {
	return fmt.Sprintf("CREATE DATABASE %s OWNER %s;", dbName, owner)
}

func alterDatabaseOwnerSQL(dbName, owner string) string {
	return fmt.Sprintf("ALTER DATABASE %s OWNER TO %s;", dbName, owner)
}

func runPSQL(ctx context.Context, container, user, database, hostPort, sql string, quiet bool) error {
	return runQuietMaybeInteractive(ctx, quiet, run.Spec{
		Program: "docker",
		Args: []string{
			"exec",
			container,
			"psql",
			"-v",
			"ON_ERROR_STOP=1",
			"-U",
			user,
			"-d",
			database,
			"-p",
			containerPostgresPort(hostPort),
			"-tAc",
			sql,
		},
	}, "docker exec psql")
}

func databaseExists(ctx context.Context, container, user, adminDB, database, hostPort string) (bool, error) {
	if !isSafeDbName(database) {
		return false, fmt.Errorf("unsafe database name %q", database)
	}
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "docker",
		Args: []string{
			"exec",
			container,
			"psql",
			"--set=ON_ERROR_STOP=1",
			"-U",
			user,
			"-d",
			adminDB,
			"-p",
			containerPostgresPort(hostPort),
			"-lqt",
		},
	})
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "|")
		if len(parts) == 0 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if name == database {
			return true, nil
		}
	}
	return false, nil
}

func VectorExtensionAvailable(ctx context.Context, container, user, database, hostPort string) (bool, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "docker",
		Args: []string{
			"exec",
			container,
			"psql",
			"--set=ON_ERROR_STOP=1",
			"-U",
			user,
			"-d",
			database,
			"-p",
			containerPostgresPort(hostPort),
			"-tAc",
			"select exists (select 1 from pg_available_extensions where name = 'vector')",
		},
	})
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(out), "t") ||
		strings.EqualFold(strings.TrimSpace(out), "true"), nil
}

func isSafeDbName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func ContainerExists(ctx context.Context, name string) (bool, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "docker",
		Args:    []string{"ps", "-a", "--format", "{{.Names}}"},
	})
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

func ContainerRunning(ctx context.Context, name string) (bool, error) {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "docker",
		Args:    []string{"ps", "--format", "{{.Names}}"},
	})
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

func runQuietMaybeInteractive(ctx context.Context, quiet bool, spec run.Spec, action string) error {
	if !quiet {
		return run.RunInteractive(ctx, spec)
	}

	out, err := run.RunCapture(ctx, spec)
	if err == nil {
		return nil
	}

	out = strings.TrimSpace(out)
	if out == "" {
		return err
	}
	return fmt.Errorf("%s: %w\n%s", action, err, out)
}
