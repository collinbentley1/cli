package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version int `yaml:"version"`
	// Providers selects the pluggable integrations (see the providers
	// package). Empty values fall back to the shipped defaults:
	// infisical / porter / tailscale. "none" disables a slot.
	Providers struct {
		Secrets   string `yaml:"secrets"`
		DBTunnel  string `yaml:"db_tunnel"`
		MCPTunnel string `yaml:"mcp_tunnel"`
	} `yaml:"providers"`
	Infisical struct {
		Environment string `yaml:"environment"`
		Paths       struct {
			Up    map[string][]string `yaml:"up"`
			Check map[string][]string `yaml:"check"`
			Hook  map[string][]string `yaml:"hook"`
		} `yaml:"paths"`
	} `yaml:"infisical"`
	Overmind struct {
		Stacks map[string]OvermindStack `yaml:"stacks"`
	} `yaml:"overmind"`

	Docker struct {
		StopByDefault []string `yaml:"stop_by_default"`
	} `yaml:"docker"`

	Porter struct {
		Ports map[string]int `yaml:"ports"`
		Envs  map[string]struct {
			AppDatastore string `yaml:"app_datastore"`
		} `yaml:"envs"`
	} `yaml:"porter"`
}

type OvermindStack struct {
	Procfile       string   `yaml:"procfile"`
	Socket         string   `yaml:"socket"`
	ConnectProcess string   `yaml:"connect_process"`
	CanDie         []string `yaml:"can_die"`
}

func Load(repoRoot string) (*Config, error) {
	path := filepath.Join(repoRoot, "ot", "ot.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Version == 0 {
		return nil, fmt.Errorf("ot/ot.yaml is missing the required `version: 1` field")
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported ot/ot.yaml version: %d (expected 1)", cfg.Version)
	}
	return &cfg, nil
}
