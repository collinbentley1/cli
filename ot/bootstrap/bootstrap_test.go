package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBrewDryRunIndicatesUpgrade(t *testing.T) {
	t.Run("outdated package", func(t *testing.T) {
		output := "==> Would upgrade 1 outdated package:\ngh 2.87.2 -> 2.89.0\n"
		if !brewDryRunIndicatesUpgrade(output) {
			t.Fatal("expected dry-run output to indicate an upgrade")
		}
	})

	t.Run("latest already installed", func(t *testing.T) {
		output := "Warning: Not upgrading entire, the latest version is already installed\n"
		if brewDryRunIndicatesUpgrade(output) {
			t.Fatal("expected latest-installed output to skip upgrade")
		}
	})

	t.Run("empty output", func(t *testing.T) {
		if brewDryRunIndicatesUpgrade("") {
			t.Fatal("expected empty output to skip upgrade")
		}
	})
}

func TestBrewUpgradeArgs(t *testing.T) {
	t.Run("formula", func(t *testing.T) {
		spec := brewPackageSpec{Name: "gh", Kind: brewPackageFormula}
		want := []string{"upgrade", "--formula", "gh", "--dry-run"}
		if got := brewUpgradeArgs(spec, true); !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected formula args: got %v want %v", got, want)
		}
	})

	t.Run("cask", func(t *testing.T) {
		spec := brewPackageSpec{Name: "claude-code", Kind: brewPackageCask}
		want := []string{"upgrade", "--cask", "claude-code", "--dry-run"}
		if got := brewUpgradeArgs(spec, true); !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected cask args: got %v want %v", got, want)
		}
	})
}

func TestBrewPackageInstalledTreatsFailedListAsNotInstalled(t *testing.T) {
	t.Helper()

	tmpDir := t.TempDir()
	brewPath := filepath.Join(tmpDir, "brew")
	script := "#!/bin/sh\n" +
		"echo 'Warning: diagnostics on stderr' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(brewPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake brew: %v", err)
	}

	installed, err := brewPackageInstalled(context.Background(), brewPath, brewPackageSpec{
		Name: "gh",
		Kind: brewPackageFormula,
	})
	if err != nil {
		t.Fatalf("brewPackageInstalled returned error: %v", err)
	}
	if installed {
		t.Fatal("expected failed brew list probe to report package as not installed")
	}
}

func TestCloudAgentDetection(t *testing.T) {
	t.Setenv("OT_CLOUD_AGENT", "1")
	if !IsCloudAgent() {
		t.Fatal("expected OT_CLOUD_AGENT=1 to enable cloud agent mode")
	}
}

func TestCloudAgentHostDetectsCodex(t *testing.T) {
	t.Setenv("CODEX_ENV_GO_VERSION", "1.25")
	if got := CloudAgentHost(); got != "codex" {
		t.Fatalf("CloudAgentHost() = %q, want codex", got)
	}
}

func TestAgentRuntimeBackendDoesNotRequirePreCommit(t *testing.T) {
	tools := agentRuntimeTools("backend")
	for _, tool := range tools {
		if tool.Name == "pre-commit" {
			t.Fatal("backend runtime preflight should not require pre-commit")
		}
	}
}

func TestAgentRuntimeBackendRequiresOvermind(t *testing.T) {
	tools := agentRuntimeTools("backend")
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"uv", "docker", "tmux", "overmind"} {
		if !names[want] {
			t.Fatalf("agentRuntimeTools(backend) missing %q: %+v", want, tools)
		}
	}
}

func TestAgentRuntimeDockerTargets(t *testing.T) {
	for _, target := range []string{"all", "backend", "auth", "crm", "wordpress"} {
		if !agentRuntimeNeedsDocker(target) {
			t.Fatalf("agentRuntimeNeedsDocker(%q) = false, want true", target)
		}
	}
	for _, target := range []string{"ot", "frontend", "db", "skills", "datadog"} {
		if agentRuntimeNeedsDocker(target) {
			t.Fatalf("agentRuntimeNeedsDocker(%q) = true, want false", target)
		}
	}
}

func TestAgentBootstrapBackendCheckDoesNotRequireDocker(t *testing.T) {
	tools := agentBootstrapTools("backend")
	for _, tool := range tools {
		if tool.Name == "docker" {
			t.Fatal("backend check preflight should not require Docker")
		}
	}
}

func TestAgentBootstrapOtRequiresGoToolchain(t *testing.T) {
	tools := agentBootstrapTools("ot")
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"git", "infisical", "go"} {
		if !names[want] {
			t.Fatalf("agentBootstrapTools(ot) missing %q: %+v", want, tools)
		}
	}
	if names["golangci-lint"] {
		t.Fatal("ot cloud check runs golangci-lint through the active Go toolchain")
	}
}
