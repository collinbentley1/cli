package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsGitMarkerAcceptsGitDirectory(t *testing.T) {
	repoRoot := t.TempDir()
	gitPath := filepath.Join(repoRoot, ".git")
	if err := os.Mkdir(gitPath, 0o755); err != nil {
		t.Fatalf("create .git dir: %v", err)
	}
	if !isGitMarker(gitPath) {
		t.Fatal("expected a .git directory to be a git marker")
	}
}

func TestIsGitMarkerResolvesRelativeGitdir(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, "actual-git-dir")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("create gitdir target: %v", err)
	}
	gitPath := filepath.Join(repoRoot, ".git")
	if err := os.WriteFile(gitPath, []byte("gitdir: actual-git-dir\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	if !isGitMarker(gitPath) {
		t.Fatal("expected a .git file with a relative gitdir to be a git marker")
	}
}

// `git worktree add` writes an ABSOLUTE gitdir line; the marker check must
// use it as-is instead of joining it onto the .git file's directory (which
// yields a path like /worktree/abs/main/.git/worktrees/x that never exists).
func TestIsGitMarkerResolvesAbsoluteGitdir(t *testing.T) {
	gitDir := filepath.Join(t.TempDir(), "main", ".git", "worktrees", "feature")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("create gitdir target: %v", err)
	}
	repoRoot := t.TempDir()
	gitPath := filepath.Join(repoRoot, ".git")
	if err := os.WriteFile(gitPath, []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	if !isGitMarker(gitPath) {
		t.Fatal("expected a .git file with an absolute gitdir (linked worktree layout) to be a git marker")
	}
}

func TestIsGitMarkerRejectsDanglingGitdir(t *testing.T) {
	repoRoot := t.TempDir()
	gitPath := filepath.Join(repoRoot, ".git")
	if err := os.WriteFile(gitPath, []byte("gitdir: /nonexistent/git/dir\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	if isGitMarker(gitPath) {
		t.Fatal("expected a dangling gitdir to be rejected")
	}
}
