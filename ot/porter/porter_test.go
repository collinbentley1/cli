package porter

import (
	"context"
	"strings"
	"testing"

	"github.com/collinbentley1/cli/ot/config"
)

func TestResolveReadsPortAndDatastoreFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Porter.Ports = map[string]int{"app": 8150}
	cfg.Porter.Envs = map[string]struct {
		AppDatastore string `yaml:"app_datastore"`
	}{
		"nonprod": {AppDatastore: "nonprod-primary-app-pgsql"},
	}

	datastore, port, err := resolve(cfg, "nonprod", "app")
	if err != nil {
		t.Fatalf("resolve() returned error: %v", err)
	}
	if datastore != "nonprod-primary-app-pgsql" {
		t.Fatalf("datastore = %q", datastore)
	}
	if port != 8150 {
		t.Fatalf("port = %d", port)
	}
}

func TestResolveRejectsUnknownDBAndEnv(t *testing.T) {
	cfg := &config.Config{}
	cfg.Porter.Ports = map[string]int{"app": 8150}

	if _, _, err := resolve(cfg, "nonprod", "mystery"); err == nil {
		t.Fatal("expected unknown-db error")
	}
	if _, _, err := resolve(cfg, "prod", "app"); err == nil || !strings.Contains(err.Error(), "unknown env") {
		t.Fatalf("expected unknown-env error, got %v", err)
	}
}

func TestConnectDatastoreFailsWhenProviderDisabled(t *testing.T) {
	t.Setenv("OT_DB_TUNNEL_PROVIDER", "none")

	err := ConnectDatastore(context.Background(), t.TempDir(), &config.Config{}, "nonprod", "app")
	if err == nil || !strings.Contains(err.Error(), "db tunnel provider is disabled") {
		t.Fatalf("expected disabled-provider error, got %v", err)
	}
}
