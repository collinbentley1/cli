package hooks

import (
	"os"
	"os/exec"
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

// In a real linked worktree git resolves hooks from the COMMON dir
// (<main>/.git/hooks), not from the per-worktree gitdir the .git file points
// at — hooks written to $GIT_DIR/worktrees/<name>/hooks are silently inert.
// Install must place them where `git rev-parse --git-path hooks` says.
func TestInstallInRealLinkedWorktreeMatchesGitPathHooks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	mainRepo := t.TempDir()
	git(mainRepo, "init", "-b", "main", ".")
	git(mainRepo, "commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(t.TempDir(), "linked")
	git(mainRepo, "worktree", "add", worktree)

	preCommitPath, err := Install(worktree)
	if err != nil {
		t.Fatalf("Install() returned error: %v", err)
	}

	wantHookDir := git(worktree, "rev-parse", "--git-path", "hooks")
	if !filepath.IsAbs(wantHookDir) {
		wantHookDir = filepath.Join(worktree, wantHookDir)
	}
	wantPreCommit, err := filepath.EvalSymlinks(filepath.Join(wantHookDir, "pre-commit"))
	if err != nil {
		t.Fatalf("pre-commit hook not found where git looks for it (%s): %v", wantHookDir, err)
	}
	gotPreCommit, err := filepath.EvalSymlinks(preCommitPath)
	if err != nil {
		t.Fatalf("resolve installed hook path: %v", err)
	}
	if gotPreCommit != wantPreCommit {
		t.Fatalf("Install() wrote %s, but git resolves hooks at %s", gotPreCommit, wantPreCommit)
	}
	for _, name := range []string{"pre-commit", "post-merge"} {
		if _, err := os.Stat(filepath.Join(wantHookDir, name)); err != nil {
			t.Fatalf("%s hook missing from git's hook dir: %v", name, err)
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
