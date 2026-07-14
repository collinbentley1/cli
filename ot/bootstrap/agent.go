package bootstrap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/collinbentley1/cli/ot/run"
)

type agentTool struct {
	Name     string
	Binaries []string
	Hint     string
}

var baseAgentTools = []agentTool{
	{Name: "git", Binaries: []string{"git"}, Hint: "install git in the cloud environment setup script"},
	{Name: "infisical", Binaries: []string{"infisical"}, Hint: "install the Infisical CLI from Infisical's official apt repository in the cloud environment setup script"},
}

// IsCloudAgent returns true for the pared-down Linux/cloud path used by Codex
// Cloud and Claude Code Cloud. Humans should not normally set these env vars.
func IsCloudAgent() bool {
	return truthyEnv("OT_CLOUD_AGENT") || CloudAgentHost() != ""
}

func CloudAgentHost() string {
	if host := strings.TrimSpace(os.Getenv("OT_CLOUD_AGENT_HOST")); host != "" {
		return strings.ToLower(host)
	}
	if strings.TrimSpace(os.Getenv("CODEX_ENV_GO_VERSION")) != "" ||
		strings.TrimSpace(os.Getenv("CODEX_ENV_NODE_VERSION")) != "" {
		return "codex"
	}
	if strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")) != "" {
		return "claude"
	}
	return ""
}

func truthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func runAgentBootstrap(ctx context.Context, opts Options) error {
	target := normalizeRuntimeTarget(opts.Target)
	if target == "" {
		target = "all"
	}
	tools := agentBootstrapTools(target)
	if err := installMissingAgentTools(ctx, tools); err != nil {
		return err
	}
	if err := ensureAgentTools(tools); err != nil {
		return err
	}

	scope := agentDependencyScope(target)
	if scope.backend || scope.crm || scope.frontend {
		ctx = withBootstrapMode(ctx)
		if err := installProjectDependencies(ctx, opts.RepoRoot, opts.Quiet, scope); err != nil {
			return err
		}
	}
	return ensureBootstrapStateDirs(opts.RepoRoot)
}

func runAgentRuntimePreflight(ctx context.Context, opts Options) error {
	target := normalizeRuntimeTarget(opts.Target)
	if target == "" {
		target = "all"
	}
	tools := agentRuntimeTools(target)
	if err := installMissingAgentTools(ctx, tools); err != nil {
		return err
	}
	if err := ensureAgentTools(tools); err != nil {
		return err
	}
	if agentRuntimeNeedsDocker(target) {
		if err := ensureAgentDockerDaemon(ctx); err != nil {
			return err
		}
	}

	scope := agentDependencyScope(target)
	if scope.backend || scope.crm || scope.frontend {
		ctx = withBootstrapMode(ctx)
		if err := installProjectDependencies(ctx, opts.RepoRoot, opts.Quiet, scope); err != nil {
			return err
		}
	}
	return ensureBootstrapStateDirs(opts.RepoRoot)
}

func agentRuntimeNeedsDocker(target string) bool {
	switch target {
	case "all", "backend", "auth", "crm", "wordpress":
		return true
	default:
		return false
	}
}

func ensureAgentDockerDaemon(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not available: install Docker in the cloud environment setup script: %w", err)
	}
	configureAgentDockerMode()
	if err := agentDockerInfo(ctx); err == nil {
		return nil
	}

	fmt.Println("[bootstrap] Starting Docker daemon for cloud agent...")
	if err := startAgentDockerDaemon(ctx); err != nil {
		return err
	}
	if err := waitForAgentDocker(ctx, 45*time.Second); err != nil {
		return fmt.Errorf("docker daemon not ready in cloud agent: %w%s", err, agentDockerLogTail())
	}
	return nil
}

func configureAgentDockerMode() {
	if CloudAgentHost() != "codex" {
		return
	}
	if strings.TrimSpace(os.Getenv("OT_DOCKER_NETWORK_MODE")) == "" {
		_ = os.Setenv("OT_DOCKER_NETWORK_MODE", "host")
	}
	if strings.TrimSpace(os.Getenv("OT_DOCKER_DISABLE_BRIDGE")) == "" {
		_ = os.Setenv("OT_DOCKER_DISABLE_BRIDGE", "1")
	}
}

func agentDockerInfo(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := run.RunCapture(cctx, run.Spec{Program: "docker", Args: []string{"info"}})
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func agentDockerLogTail() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	data, err := os.ReadFile(home + "/.ot/logs/dockerd.log")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > 60 {
		lines = lines[len(lines)-60:]
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return ""
	}
	return "\n~/.ot/logs/dockerd.log tail:\n" + strings.Join(lines, "\n")
}

func waitForAgentDocker(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := agentDockerInfo(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("timed out after %s", timeout)
}

func startAgentDockerDaemon(ctx context.Context) error {
	script := `set -euo pipefail
if docker info >/dev/null 2>&1; then
  exit 0
fi

run_as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
    sudo -n "$@"
  else
    echo "root or passwordless sudo is required to start dockerd" >&2
    return 1
  fi
}

if command -v service >/dev/null 2>&1; then
  run_as_root service docker start >/tmp/ot-docker-service.log 2>&1 || true
fi
if docker info >/dev/null 2>&1; then
  exit 0
fi
if ! command -v dockerd >/dev/null 2>&1; then
  echo "dockerd is not installed" >&2
  exit 1
fi

log_dir="$HOME/.ot/logs"
data_root="${OT_DOCKER_DATA_ROOT:-$HOME/.local/share/docker}"
mkdir -p "$log_dir" "$data_root"

bridge_args=()
if [ "${OT_DOCKER_DISABLE_BRIDGE:-}" = "1" ] || [ "${OT_DOCKER_NETWORK_MODE:-}" = "host" ]; then
  bridge_args=(--storage-driver=vfs --iptables=false --ip-masq=false --ip-forward=false --ip6tables=false --bridge=none)
  data_root="${OT_DOCKER_DATA_ROOT:-$HOME/.local/share/docker-vfs}"
  mkdir -p "$data_root"
fi

start_dockerd() {
  log_file="$1"
  shift
  if [ "$(id -u)" -eq 0 ]; then
    nohup dockerd --host=unix:///var/run/docker.sock --data-root="$data_root" "$@" >"$log_file" 2>&1 &
  else
    sudo -n env OT_DOCKER_DATA_ROOT="$data_root" OT_DOCKER_LOG_FILE="$log_file" \
      sh -c 'nohup dockerd --host=unix:///var/run/docker.sock --data-root="$OT_DOCKER_DATA_ROOT" "$@" >"$OT_DOCKER_LOG_FILE" 2>&1 &' sh "$@"
  fi
}

if ! pgrep -x dockerd >/dev/null 2>&1; then
  start_dockerd "$log_dir/dockerd.log" "${bridge_args[@]}"
fi
for _ in $(seq 1 8); do
  docker info >/dev/null 2>&1 && exit 0
  sleep 1
done

# Some nested Linux containers cannot use overlay networking/storage defaults.
# Retry with conservative settings before reporting the daemon as unavailable.
if ! docker info >/dev/null 2>&1; then
  if pgrep -x dockerd >/dev/null 2>&1; then
    run_as_root pkill -TERM dockerd >/dev/null 2>&1 || true
    sleep 2
  fi
  data_root="${OT_DOCKER_DATA_ROOT:-$HOME/.local/share/docker-vfs}"
  mkdir -p "$data_root"
  start_dockerd "$log_dir/dockerd.log" --storage-driver=vfs --iptables=false --ip-masq=false --ip-forward=false --ip6tables=false --bridge=none
fi
exit 0
`
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "bash",
		Args:    []string{"-lc", script},
	})
	if err != nil {
		return fmt.Errorf("start dockerd: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

func ensureAgentTools(tools []agentTool) error {
	var missing []string
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		if len(tool.Binaries) == 0 {
			continue
		}
		ok := true
		for _, bin := range tool.Binaries {
			if _, err := exec.LookPath(bin); err != nil {
				ok = false
				break
			}
		}
		if !ok {
			if tool.Hint != "" {
				missing = append(missing, fmt.Sprintf("%s (%s)", tool.Name, tool.Hint))
			} else {
				missing = append(missing, tool.Name)
			}
		}
	}
	if len(missing) > 0 {
		host := CloudAgentHost()
		if host == "" {
			host = runtime.GOOS
		}
		return fmt.Errorf("ot cloud-agent preflight failed on %s; missing required tools: %s", host, strings.Join(missing, ", "))
	}
	return nil
}

func installMissingAgentTools(ctx context.Context, tools []agentTool) error {
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		if agentToolAvailable(tool) {
			continue
		}
		if err := installAgentTool(ctx, tool.Name); err != nil {
			return err
		}
	}
	prependLocalBinToPath()
	return nil
}

func agentToolAvailable(tool agentTool) bool {
	if len(tool.Binaries) == 0 {
		return true
	}
	for _, bin := range tool.Binaries {
		if _, err := exec.LookPath(bin); err != nil {
			return false
		}
	}
	return true
}

func installAgentTool(ctx context.Context, name string) error {
	switch name {
	case "infisical":
		return runAgentInstallScript(ctx, "install infisical", agentInstallInfisicalScript())
	case "uv":
		return runAgentInstallScript(ctx, "install uv", agentInstallUVScript())
	case "docker":
		return runAgentInstallScript(ctx, "install docker", agentInstallDockerScript())
	case "tmux":
		return runAgentInstallScript(ctx, "install tmux", agentInstallAptScript("tmux"))
	case "overmind":
		return runAgentInstallScript(ctx, "install overmind", agentInstallOvermindScript())
	case "pre-commit":
		if _, err := exec.LookPath("uv"); err != nil {
			if err := installAgentTool(ctx, "uv"); err != nil {
				return err
			}
		}
		return runAgentInstallScript(ctx, "install pre-commit", `uv tool install pre-commit`)
	default:
		return nil
	}
}

func runAgentInstallScript(ctx context.Context, label, script string) error {
	out, err := run.RunCapture(ctx, run.Spec{
		Program: "bash",
		Args:    []string{"-lc", "set -euo pipefail\n" + script},
	})
	if err != nil {
		return fmt.Errorf("%s: %w: %s", label, err, strings.TrimSpace(out))
	}
	prependLocalBinToPath()
	return nil
}

func prependLocalBinToPath() {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	localBin := home + "/.local/bin"
	path := os.Getenv("PATH")
	for _, entry := range strings.Split(path, ":") {
		if entry == localBin {
			return
		}
	}
	if path == "" {
		_ = os.Setenv("PATH", localBin)
		return
	}
	_ = os.Setenv("PATH", localBin+":"+path)
}

func agentInstallAptScript(packages ...string) string {
	return fmt.Sprintf(`
export DEBIAN_FRONTEND=noninteractive
run_as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    sudo -n -E "$@"
  else
    echo "root or passwordless sudo is required to install %s" >&2
    return 1
  fi
}
run_as_root apt-get update
run_as_root apt-get install -y --no-install-recommends %s
`, strings.Join(packages, " "), strings.Join(packages, " "))
}

func agentInstallInfisicalScript() string {
	return `
mkdir -p "$HOME/.local/bin"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) infisical_arch=amd64 ;;
  aarch64|arm64) infisical_arch=arm64 ;;
  *) echo "unsupported Infisical arch: $arch" >&2; exit 1 ;;
esac
version="0.43.79"
tmp_dir="$(mktemp -d)"
curl --fail --location --silent --show-error --max-time 90 \
  "https://github.com/Infisical/cli/releases/download/v${version}/cli_${version}_linux_${infisical_arch}.tar.gz" \
  -o "$tmp_dir/infisical.tgz"
tar -C "$tmp_dir" -xzf "$tmp_dir/infisical.tgz" infisical
install -m 0755 "$tmp_dir/infisical" "$HOME/.local/bin/infisical"
rm -rf "$tmp_dir"
`
}

func agentInstallUVScript() string {
	return `
mkdir -p "$HOME/.local/bin"
curl --fail --location --silent --show-error --max-time 120 https://astral.sh/uv/install.sh | sh
`
}

func agentInstallDockerScript() string {
	return `
mkdir -p "$HOME/.local/bin"
if command -v docker >/dev/null 2>&1 && command -v dockerd >/dev/null 2>&1; then
  exit 0
fi
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) docker_arch=x86_64 ;;
  aarch64|arm64) docker_arch=aarch64 ;;
  *) echo "unsupported Docker arch: $arch" >&2; exit 1 ;;
esac
version="29.3.1"
tmp_dir="$(mktemp -d)"
curl --fail --location --silent --show-error --max-time 180 \
  "https://download.docker.com/linux/static/stable/${docker_arch}/docker-${version}.tgz" \
  -o "$tmp_dir/docker.tgz"
tar -C "$tmp_dir" -xzf "$tmp_dir/docker.tgz"
for bin in containerd containerd-shim-runc-v2 ctr docker docker-init docker-proxy dockerd runc; do
  if [ -f "$tmp_dir/docker/$bin" ]; then
    install -m 0755 "$tmp_dir/docker/$bin" "$HOME/.local/bin/$bin"
  fi
done
rm -rf "$tmp_dir"
`
}

func agentInstallOvermindScript() string {
	return `
mkdir -p "$HOME/.local/bin"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) overmind_arch=amd64 ;;
  aarch64|arm64) overmind_arch=arm64 ;;
  *) echo "unsupported Overmind arch: $arch" >&2; exit 1 ;;
esac
version="2.5.1"
tmp_file="$(mktemp)"
curl --fail --location --silent --show-error --max-time 90 \
  "https://github.com/DarthSim/overmind/releases/download/v${version}/overmind-v${version}-linux-${overmind_arch}.gz" \
  -o "$tmp_file.gz"
gunzip -c "$tmp_file.gz" > "$tmp_file"
install -m 0755 "$tmp_file" "$HOME/.local/bin/overmind"
rm -f "$tmp_file" "$tmp_file.gz"
`
}

func agentBootstrapTools(target string) []agentTool {
	tools := append([]agentTool{}, baseAgentTools...)
	switch target {
	case "ot":
		return append(tools,
			agentTool{Name: "go", Binaries: []string{"go"}, Hint: "install Go in the cloud environment"},
		)
	case "backend", "auth":
		return append(tools, backendCheckAgentTools()...)
	case "crm":
		return append(tools, crmCheckAgentTools()...)
	case "frontend":
		return append(tools, frontendCheckAgentTools()...)
	case "datadog":
		return append(tools,
			agentTool{Name: "datadog-static-analyzer-git-hook", Binaries: []string{"datadog-static-analyzer-git-hook"}, Hint: "Datadog's local analyzer must be installed for this target"},
		)
	case "skills":
		return append(tools, agentTool{Name: "bash", Binaries: []string{"bash"}})
	default:
		tools = append(tools,
			agentTool{Name: "go", Binaries: []string{"go"}, Hint: "install Go in the cloud environment"},
			agentTool{Name: "pre-commit", Binaries: []string{"pre-commit"}, Hint: "install pre-commit in the cloud environment setup script"},
			agentTool{Name: "golangci-lint", Binaries: []string{"golangci-lint"}, Hint: "install golangci-lint in the cloud environment setup script"},
		)
		tools = append(tools, backendCheckAgentTools()...)
		tools = append(tools, crmCheckAgentTools()...)
		tools = append(tools, frontendCheckAgentTools()...)
		return tools
	}
}

func agentRuntimeTools(target string) []agentTool {
	tools := append([]agentTool{}, baseAgentTools...)
	switch target {
	case "backend", "auth":
		return append(tools, backendRuntimeAgentTools()...)
	case "crm":
		return append(tools, crmRuntimeAgentTools()...)
	case "frontend":
		return append(tools, frontendRuntimeAgentTools()...)
	case "db":
		tools = append(tools, agentTool{Name: "porter", Binaries: []string{"porter"}, Hint: "install Porter in the cloud environment if DB tunnels are needed"})
		return append(tools, overmindAgentTools()...)
	case "wordpress":
		return append(tools, agentTool{Name: "docker", Binaries: []string{"docker"}, Hint: "provide Docker in the cloud environment"}, agentTool{Name: "node", Binaries: []string{"node"}}, agentTool{Name: "npm", Binaries: []string{"npm"}})
	default:
		tools = append(tools, backendRuntimeAgentTools()...)
		tools = append(tools, frontendRuntimeAgentTools()...)
		return tools
	}
}

func backendRuntimeAgentTools() []agentTool {
	tools := []agentTool{
		{Name: "uv", Binaries: []string{"uv"}, Hint: "install uv in the cloud environment setup script"},
		{Name: "docker", Binaries: []string{"docker"}, Hint: "provide Docker in the cloud environment for local Postgres"},
	}
	return append(tools, overmindAgentTools()...)
}

func backendCheckAgentTools() []agentTool {
	return []agentTool{
		{Name: "uv", Binaries: []string{"uv"}, Hint: "install uv in the cloud environment setup script"},
		{Name: "pre-commit", Binaries: []string{"pre-commit"}, Hint: "install pre-commit in the cloud environment setup script"},
	}
}

func crmRuntimeAgentTools() []agentTool {
	tools := backendRuntimeAgentTools()
	tools = append(tools, overmindAgentTools()...)
	return tools
}

func crmCheckAgentTools() []agentTool {
	return []agentTool{
		{Name: "uv", Binaries: []string{"uv"}, Hint: "install uv in the cloud environment setup script"},
		{Name: "pre-commit", Binaries: []string{"pre-commit"}, Hint: "install pre-commit in the cloud environment setup script"},
	}
}

func frontendRuntimeAgentTools() []agentTool {
	tools := []agentTool{
		{Name: "node", Binaries: []string{"node"}, Hint: "install Node.js in the cloud environment"},
		{Name: "npm", Binaries: []string{"npm"}, Hint: "install npm in the cloud environment"},
	}
	return append(tools, overmindAgentTools()...)
}

func frontendCheckAgentTools() []agentTool {
	return []agentTool{
		{Name: "node", Binaries: []string{"node"}, Hint: "install Node.js in the cloud environment"},
		{Name: "npm", Binaries: []string{"npm"}, Hint: "install npm in the cloud environment"},
		{Name: "pre-commit", Binaries: []string{"pre-commit"}, Hint: "install pre-commit in the cloud environment setup script"},
	}
}

func overmindAgentTools() []agentTool {
	return []agentTool{
		{Name: "tmux", Binaries: []string{"tmux"}, Hint: "install tmux with apt in the cloud environment setup script"},
		{Name: "overmind", Binaries: []string{"overmind"}, Hint: "install overmind in the cloud environment setup script"},
	}
}

func agentDependencyScope(target string) dependencyScope {
	switch target {
	case "backend", "auth":
		return dependencyScope{backend: true}
	case "crm":
		return dependencyScope{crm: true}
	case "frontend":
		return dependencyScope{frontend: true}
	case "ot", "datadog", "db", "skills", "wordpress":
		return dependencyScope{}
	default:
		return dependencyScope{backend: true, crm: true, frontend: true}
	}
}
