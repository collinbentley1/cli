package paths

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrRepoRootNotFound = errors.New("repo root not found")

func FindRepoRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	if root, ok := findRepoRootViaGit(dir); ok {
		return root, nil
	}

	for {
		gitPath := filepath.Join(dir, ".git")
		otCfgPath := filepath.Join(dir, "ot", "ot.yaml")

		gitOK := isGitMarker(gitPath)
		cfgOK := isFile(otCfgPath)
		if gitOK && cfgOK {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("%w: could not find .git and ot/ot.yaml walking up from %s", ErrRepoRootNotFound, startDir)
}

func findRepoRootViaGit(startDir string) (string, bool) {
	cmd := exec.Command("git", "-C", startDir, "rev-parse", "--show-toplevel")
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	if isFile(filepath.Join(root, "ot", "ot.yaml")) {
		return root, true
	}
	return "", false
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	return st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !st.IsDir()
}

func isGitMarker(gitPath string) bool {
	if isDir(gitPath) {
		return true
	}
	st, err := os.Stat(gitPath)
	if err != nil || st.IsDir() {
		return false
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return false
	}
	line := string(data)
	const prefix = "gitdir:"
	if len(line) < len(prefix) {
		return false
	}
	if line[:len(prefix)] != prefix {
		return false
	}
	gitdir := filepath.Clean(filepath.FromSlash(trimSpace(line[len(prefix):])))
	if !filepath.IsAbs(gitdir) {
		// Relative gitdir values are resolved against the directory that
		// holds the .git file; absolute values (what `git worktree add`
		// writes) are used as-is.
		gitdir = filepath.Clean(filepath.Join(filepath.Dir(gitPath), gitdir))
	}
	if gitdir == "" || gitdir == "." {
		return false
	}
	return isDir(gitdir)
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
