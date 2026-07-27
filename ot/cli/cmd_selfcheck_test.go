package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOtBinaryNeedsUpgradeOnlyWhenCLISourcesChanged(t *testing.T) {
	repoRoot := initSelfCheckGitRepo(t)
	writeSelfCheckFile(t, repoRoot, "go.mod", "module example.com/selfcheck\n\ngo 1.22\n")
	writeSelfCheckFile(t, repoRoot, "ot/main.go", "package main\n")
	writeSelfCheckFile(t, repoRoot, "backend/server/main.py", "print('base')\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "base")
	baseSHA := gitOutput(t, repoRoot, "rev-parse", "HEAD")

	writeSelfCheckFile(t, repoRoot, "backend/server/main.py", "print('backend only')\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "backend only")
	backendOnlySHA := gitOutput(t, repoRoot, "rev-parse", "HEAD")

	needsUpgrade, err := otBinaryNeedsUpgrade(
		context.Background(),
		repoRoot,
		baseSHA,
		"",
	)
	if err != nil {
		t.Fatalf("otBinaryNeedsUpgrade() returned error: %v", err)
	}
	if needsUpgrade {
		t.Fatal("expected backend-only changes to skip ot rebuild")
	}

	writeSelfCheckFile(t, repoRoot, "ot/cli/new_feature.go", "package cli\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "ot cli change")

	needsUpgrade, err = otBinaryNeedsUpgrade(
		context.Background(),
		repoRoot,
		backendOnlySHA,
		"",
	)
	if err != nil {
		t.Fatalf("otBinaryNeedsUpgrade() returned error: %v", err)
	}
	if !needsUpgrade {
		t.Fatal("expected ot source changes to require ot rebuild")
	}

	needsUpgrade, err = otBinaryNeedsUpgrade(
		context.Background(),
		repoRoot,
		"dev",
		baseSHA,
	)
	if err != nil {
		t.Fatalf("otBinaryNeedsUpgrade() returned error: %v", err)
	}
	if !needsUpgrade {
		t.Fatal("expected --base comparison with ot changes to require rebuild")
	}
}

func TestOtSourceChangedBetweenTreatsGoModuleChangesAsCLISources(t *testing.T) {
	repoRoot := initSelfCheckGitRepo(t)
	writeSelfCheckFile(t, repoRoot, "go.mod", "module example.com/selfcheck\n\ngo 1.22\n")
	writeSelfCheckFile(t, repoRoot, "ot/main.go", "package main\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "base")
	baseSHA := gitOutput(t, repoRoot, "rev-parse", "HEAD")

	writeSelfCheckFile(t, repoRoot, "go.sum", "example.com/dep v1.0.0 h1:abc\n")
	git(t, repoRoot, "add", ".")
	git(t, repoRoot, "commit", "-m", "go sum")
	headSHA := gitOutput(t, repoRoot, "rev-parse", "HEAD")

	changed, err := otSourceChangedBetween(context.Background(), repoRoot, baseSHA, headSHA)
	if err != nil {
		t.Fatalf("otSourceChangedBetween() returned error: %v", err)
	}
	if !changed {
		t.Fatal("expected go.sum changes to count as CLI build inputs")
	}
}

func initSelfCheckGitRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	git(t, repoRoot, "init")
	git(t, repoRoot, "config", "user.email", "tests@example.com")
	git(t, repoRoot, "config", "user.name", "Tests")
	return repoRoot
}

func writeSelfCheckFile(t *testing.T, repoRoot string, name string, content string) {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func git(t *testing.T, repoRoot string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func gitOutput(t *testing.T, repoRoot string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}
