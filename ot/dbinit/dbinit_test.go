package dbinit

import (
	"slices"
	"testing"
)

func TestPostgresRunArgsUsePortMappingByDefault(t *testing.T) {
	t.Setenv("OT_DOCKER_NETWORK_MODE", "")

	args := postgresRunArgs("app-postgres", "pgvector/pgvector:pg18", "5433", Config{
		User:     "postgres",
		Password: "postgres",
		Database: "postgres",
	})

	if !slices.Contains(args, "-p") || !slices.Contains(args, "5433:5432") {
		t.Fatalf("postgresRunArgs() missing default port mapping: %#v", args)
	}
	if slices.Contains(args, "--network") || slices.Contains(args, "host") {
		t.Fatalf("postgresRunArgs() unexpectedly used host networking: %#v", args)
	}
}

func TestPostgresRunArgsUseHostNetworkPortForCodexDocker(t *testing.T) {
	t.Setenv("OT_DOCKER_NETWORK_MODE", "host")

	args := postgresRunArgs("app-postgres", "pgvector/pgvector:pg18", "5433", Config{
		User:     "postgres",
		Password: "postgres",
		Database: "postgres",
	})

	for _, want := range []string{"--network", "host", "postgres", "-c", "listen_addresses=*", "port=5433"} {
		if !slices.Contains(args, want) {
			t.Fatalf("postgresRunArgs() missing %q: %#v", want, args)
		}
	}
	if slices.Contains(args, "5433:5432") {
		t.Fatalf("postgresRunArgs() should not publish bridge ports in host mode: %#v", args)
	}
	if got := containerPostgresPort("5433"); got != "5433" {
		t.Fatalf("containerPostgresPort() = %q, want 5433", got)
	}
}
