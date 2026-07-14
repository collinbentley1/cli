package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesPreCommitAndPostMergeHooks(t *testing.T) {
	repoRoot := t.TempDir()
	hookDir := filepath.Join(repoRoot, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("create hook dir: %v", err)
	}

	preCommitPath, err := Install(repoRoot)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}
	if preCommitPath != filepath.Join(hookDir, "pre-commit") {
		t.Fatalf("Install() path = %q, want pre-commit path", preCommitPath)
	}

	for _, name := range []string{"pre-commit", "post-merge"} {
		path := filepath.Join(hookDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s hook was not installed: %v", name, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("%s hook is not executable: %v", name, info.Mode())
		}
	}

	postMerge, err := os.ReadFile(filepath.Join(hookDir, "post-merge"))
	if err != nil {
		t.Fatalf("read post-merge hook: %v", err)
	}
	content := string(postMerge)
	for _, want := range []string{
		"git diff --quiet \"$base\" HEAD -- ot go.mod go.sum",
		"\"$ot_bin\" selfcheck --if-changed --base \"$base\"",
		"\"$repo_root/ot/scripts/install.sh\"",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("post-merge hook missing %q:\n%s", want, content)
		}
	}
}

func TestInstallUsesLinkedWorktreeGitDir(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), "repo.git", "worktrees", "feature")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("create worktree git dir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(repoRoot, ".git"),
		[]byte("gitdir: "+gitDir+"\n"),
		0o644,
	); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	if _, err := Install(repoRoot); err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	postMergePath := filepath.Join(gitDir, "hooks", "post-merge")
	if _, err := os.Stat(postMergePath); err != nil {
		t.Fatalf("worktree-local post-merge hook was not installed: %v", err)
	}
}
