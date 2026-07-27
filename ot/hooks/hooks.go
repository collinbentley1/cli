package hooks

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Install(repoRoot string) (string, error) {
	hookDir, err := resolveHookDir(repoRoot)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return "", fmt.Errorf("create hook dir: %w", err)
	}

	hooks := map[string]string{
		"pre-commit": preCommitScript(),
		"post-merge": postMergeScript(),
	}
	for name, content := range hooks {
		hookPath := filepath.Join(hookDir, name)
		if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
			return "", fmt.Errorf("write %s hook: %w", name, err)
		}
		if err := os.Chmod(hookPath, 0o755); err != nil {
			return "", fmt.Errorf("chmod %s hook: %w", name, err)
		}
	}
	return filepath.Join(hookDir, "pre-commit"), nil
}

func resolveHookDir(repoRoot string) (string, error) {
	// Ask git first: in linked worktrees git resolves hooks from the COMMON
	// dir (<main>/.git/hooks), not the per-worktree gitdir the .git file
	// points at, and --git-path also honors core.hooksPath. The manual
	// resolution below stays as the fallback for hosts without git.
	if out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--git-path", "hooks").Output(); err == nil {
		if dir := strings.TrimSpace(string(out)); dir != "" {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(repoRoot, dir)
			}
			return dir, nil
		}
	}

	gitMarker := filepath.Join(repoRoot, ".git")
	if st, err := os.Stat(gitMarker); err != nil {
		return "", fmt.Errorf(".git not found: %w", err)
	} else if st.IsDir() {
		return filepath.Join(gitMarker, "hooks"), nil
	}

	data, err := os.ReadFile(gitMarker)
	if err != nil {
		return "", fmt.Errorf("read .git: %w", err)
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("unexpected .git file format")
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitDir == "" {
		return "", fmt.Errorf("empty gitdir in .git")
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoRoot, gitDir)
	}
	return filepath.Join(gitDir, "hooks"), nil
}

func preCommitScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

if command -v pre-commit >/dev/null 2>&1; then
  exec pre-commit run
fi

echo "[pre-commit] ERROR: pre-commit is not installed."
echo "[pre-commit] Run 'ot bootstrap' to install dependencies."
exit 1
`
}

func postMergeScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
if [[ -z "$repo_root" ]]; then
  exit 0
fi
cd "$repo_root"

base="${OT_POST_MERGE_BASE:-}"
if [[ -z "$base" ]]; then
  base="$(git rev-parse -q --verify ORIG_HEAD 2>/dev/null || true)"
fi
if [[ -z "$base" ]]; then
  exit 0
fi

if git diff --quiet "$base" HEAD -- ot go.mod go.sum; then
  exit 0
fi

ot_bin="$repo_root/.ot/bin/ot"
if [[ ! -x "$ot_bin" ]]; then
  echo "[post-merge] ot CLI changed and no repo-local ot binary exists; installing ot..."
  exec "$repo_root/ot/scripts/install.sh"
fi

echo "[post-merge] ot CLI changed; checking installed ot binary..."
exec "$ot_bin" selfcheck --if-changed --base "$base"
`
}
