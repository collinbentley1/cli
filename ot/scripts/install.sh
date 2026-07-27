#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$REPO_ROOT"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "[install] Error: macOS only."
  exit 1
fi

if [[ "$(uname -m)" != "arm64" ]]; then
  echo "[install] Error: Apple Silicon (arm64) only."
  exit 1
fi

BREW_BIN=""
if command -v brew >/dev/null 2>&1; then
  BREW_BIN="$(command -v brew)"
elif [[ -x /opt/homebrew/bin/brew ]]; then
  BREW_BIN="/opt/homebrew/bin/brew"
fi

if [[ -z "$BREW_BIN" ]]; then
  echo "[install] Homebrew not found. Installing Homebrew..."
  NONINTERACTIVE=1 /bin/bash -c \
    "$(/usr/bin/curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  BREW_BIN="/opt/homebrew/bin/brew"
fi

eval "$("$BREW_BIN" shellenv)"

ensure_shellenv() {
  local profile_file="$1"
  if [[ -f "$profile_file" ]]; then
    if ! grep -q 'brew shellenv' "$profile_file"; then
      echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> "$profile_file"
    fi
  else
    echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> "$profile_file"
  fi
}

ensure_shellenv "$HOME/.zprofile"
ensure_shellenv "$HOME/.bash_profile"

if ! command -v go >/dev/null 2>&1; then
  echo "[install] Installing Go..."
  "$BREW_BIN" install go
fi

echo "[install] Building ot CLI..."
mkdir -p "$REPO_ROOT/.ot/bin"
GIT_SHA="$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo dev)"
go build -ldflags "-X github.com/collinbentley1/cli/ot/cli.Version=${GIT_SHA}" -o "$REPO_ROOT/.ot/bin/ot" ./ot

BREW_PREFIX=$("$BREW_BIN" --prefix)
mkdir -p "$BREW_PREFIX/bin"
ln -sf "$REPO_ROOT/.ot/bin/ot" "$BREW_PREFIX/bin/ot"

echo "[install] ot installed to $BREW_PREFIX/bin/ot"
echo "[install] Running ot bootstrap (installs dependencies)..."
"$BREW_PREFIX/bin/ot" bootstrap || "$BREW_PREFIX/bin/ot" bootstrap
echo "[install] Done! Run 'ot up' to start the stack."
