#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$repo_root"

skill_dirs=()
if [[ $# -gt 0 ]]; then
  for dir in "$@"; do
    [[ -d "$dir" ]] || continue
    skill_dirs+=("$dir")
  done
else
  for dir in skills/*; do
    [[ -d "$dir" ]] || continue
    skill_dirs+=("$dir")
  done
fi

if [[ ${#skill_dirs[@]} -eq 0 ]]; then
  exit 0
fi

for dir in "${skill_dirs[@]}"; do
  uvx --from skills-ref agentskills validate "$dir"
done

uvx --from skills-ref agentskills to-prompt "${skill_dirs[@]}" >/dev/null
