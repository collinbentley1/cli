package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseNpmOutdatedEntriesFiltersToDirectDependents(t *testing.T) {
	raw, err := json.Marshal([]map[string]string{
		{
			"current":   "9.39.4",
			"wanted":    "10.2.0",
			"latest":    "10.2.0",
			"dependent": "@typescript-eslint/parser",
		},
		{
			"current":   "9.39.4",
			"wanted":    "9.39.4",
			"latest":    "10.2.0",
			"dependent": "app",
		},
	})
	if err != nil {
		t.Fatalf("marshal outdated payload: %v", err)
	}

	entries, err := parseNpmOutdatedEntries(raw)
	if err != nil {
		t.Fatalf("parseNpmOutdatedEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}

	allowed := map[string]struct{}{"app": {}}
	if isDirectNpmOutdatedEntry(entries[0], allowed) {
		t.Fatalf("expected transitive dependent %q to be ignored", entries[0].Dependent)
	}
	if !isDirectNpmOutdatedEntry(entries[1], allowed) {
		t.Fatalf("expected direct dependent %q to be accepted", entries[1].Dependent)
	}
}

func TestFrontendWorkspaceHelpersIncludeDocsAndRootDependents(t *testing.T) {
	repoRoot := t.TempDir()
	frontendDir := filepath.Join(repoRoot, "frontend")
	for _, dir := range []string{
		filepath.Join(frontendDir, "app"),
		filepath.Join(frontendDir, "docs"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	writeJSON := func(path string, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	writeJSON(
		filepath.Join(frontendDir, "package.json"),
		`{"name":"frontend","workspaces":["app","docs"]}`,
	)
	writeJSON(
		filepath.Join(frontendDir, "app", "package.json"),
		`{"name":"frontend-app","dependencies":{"eslint":"^9.39.4"}}`,
	)
	writeJSON(
		filepath.Join(frontendDir, "docs", "package.json"),
		`{"name":"frontend-docs","dependencies":{"eslint":"^9.39.4"}}`,
	)

	workspaces, err := frontendWorkspacePackages(repoRoot)
	if err != nil {
		t.Fatalf("frontendWorkspacePackages: %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("len(workspaces) = %d, want 2", len(workspaces))
	}
	if workspaces[0].RelPath != "app" || workspaces[1].RelPath != "docs" {
		t.Fatalf("unexpected workspace relpaths: %+v", workspaces)
	}

	dependents, err := frontendDirectDependents(repoRoot)
	if err != nil {
		t.Fatalf("frontendDirectDependents: %v", err)
	}
	for _, dependent := range []string{"frontend", "app", "docs", "frontend-app", "frontend-docs"} {
		if _, ok := dependents[dependent]; !ok {
			t.Fatalf("expected dependent %q to be included; got=%v", dependent, dependents)
		}
	}
}

func TestOtInstallSymlinkPathUsesLocalBinForCloudAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OT_CLOUD_AGENT", "1")

	got, err := otInstallSymlinkPath(context.Background())
	if err != nil {
		t.Fatalf("otInstallSymlinkPath: %v", err)
	}
	want := filepath.Join(home, ".local", "bin", "ot")
	if got != want {
		t.Fatalf("otInstallSymlinkPath() = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Dir(got)); err != nil {
		t.Fatalf("expected install dir to exist: %v", err)
	}
}
