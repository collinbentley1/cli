package localdev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/collinbentley1/cli/ot/run"
)

func TestEnabledDefaultsToLinkedGitWorktrees(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".git"), []byte("gitdir: /tmp/example\n"), 0o644); err != nil {
		t.Fatalf("write git file: %v", err)
	}
	t.Setenv(isolationModeEnv, "")

	if !Enabled(repoRoot) {
		t.Fatal("expected linked git worktree to enable local isolation")
	}
}

func TestEnabledSkipsPrimaryGitCheckoutByDefault(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatalf("create git dir: %v", err)
	}
	t.Setenv(isolationModeEnv, "")

	if Enabled(repoRoot) {
		t.Fatal("expected primary git checkout to keep legacy local defaults")
	}
}

func TestApplyAllocatesWorktreePortsAndOverridesDBURLs(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".git"), []byte("gitdir: /tmp/example\n"), 0o644); err != nil {
		t.Fatalf("write git file: %v", err)
	}
	t.Setenv(registryDirEnv, t.TempDir())
	t.Setenv("LOCAL_PRIMARY_APP_PGSQL_DB_URL", "postgresql://postgres:postgres@127.0.0.1:5433/app")
	t.Setenv("LOCAL_PRIMARY_CRM_PGSQL_DB_URL", "postgresql+psycopg://crm:crm@127.0.0.1:5434/crm")

	profile, err := Apply(repoRoot)
	if err != nil {
		t.Fatalf("Apply() returned error: %v", err)
	}
	if profile == nil {
		t.Fatal("expected profile")
	}

	if got := os.Getenv(activeEnv); got != "1" {
		t.Fatalf("%s = %q, want 1", activeEnv, got)
	}
	if got := os.Getenv("APP_DB_CONTAINER"); !strings.HasPrefix(got, "app-postgres-") {
		t.Fatalf("APP_DB_CONTAINER = %q", got)
	}
	if got := os.Getenv("LOCAL_PRIMARY_APP_PGSQL_DB_URL"); strings.Contains(got, ":5433/") {
		t.Fatalf("LOCAL_PRIMARY_APP_PGSQL_DB_URL was not isolated: %q", got)
	}
	if got := os.Getenv("LOCAL_PRIMARY_CRM_PGSQL_DB_URL"); strings.Contains(got, ":5434/") {
		t.Fatalf("LOCAL_PRIMARY_CRM_PGSQL_DB_URL was not isolated: %q", got)
	}
	if got := os.Getenv("LOCAL_PRIMARY_APP_PGSQL_DB_URL"); strings.Contains(got, ":postgres@") {
		t.Fatalf("LOCAL_PRIMARY_APP_PGSQL_DB_URL should use a generated password: %q", got)
	}
	if got := os.Getenv("LOCAL_PRIMARY_CRM_PGSQL_DB_URL"); strings.Contains(got, ":crm@") {
		t.Fatalf("LOCAL_PRIMARY_CRM_PGSQL_DB_URL should use a generated password: %q", got)
	}
	if got := os.Getenv("APP_PORT"); got == "8005" || got == "" {
		t.Fatalf("APP_PORT = %q, want isolated non-default port", got)
	}
	profilePath := filepath.Join(repoRoot, ".ot", "local-dev-profile.json")
	info, err := os.Stat(profilePath)
	if err != nil {
		t.Fatalf("stat local profile: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("local profile mode = %v, want 0600", got)
	}
}

func writeLinkedWorktreeFixture(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoRoot, ".git"), []byte("gitdir: /tmp/example\n"), 0o644); err != nil {
		t.Fatalf("write git file: %v", err)
	}
	return repoRoot
}

// Two worktrees sharing one registry must get disjoint port blocks — this is
// the core isolation promise. Uses Ensure() rather than Apply() so process
// env is not mutated.
func TestEnsureAllocatesDisjointPortBlocksAcrossWorktrees(t *testing.T) {
	t.Setenv(registryDirEnv, t.TempDir())
	t.Setenv(forceReallocateEnv, "")

	repoA := writeLinkedWorktreeFixture(t)
	repoB := writeLinkedWorktreeFixture(t)

	profileA, err := Ensure(repoA)
	if err != nil {
		t.Fatalf("Ensure(repoA) returned error: %v", err)
	}
	profileB, err := Ensure(repoB)
	if err != nil {
		t.Fatalf("Ensure(repoB) returned error: %v", err)
	}
	if profileA == nil || profileB == nil {
		t.Fatal("expected profiles for both worktrees")
	}

	if profileA.Offset == profileB.Offset {
		t.Fatalf("both worktrees got offset %d; registry marking failed", profileA.Offset)
	}
	if profileA.InstanceID == profileB.InstanceID {
		t.Fatalf("both worktrees got instance id %q", profileA.InstanceID)
	}
	portsA := map[int]bool{}
	for _, port := range profileA.Ports.list() {
		portsA[port] = true
	}
	for _, port := range profileB.Ports.list() {
		if portsA[port] {
			t.Fatalf("port %d allocated to both worktrees\nA: %+v\nB: %+v", port, profileA.Ports, profileB.Ports)
		}
	}
}

// Re-allocation after a lost profile must be stable: the shared registry
// remembers the worktree's instance id and offset, so deleting
// .ot/local-dev-profile.json and re-running yields the same block (containers
// and ports keep working).
func TestEnsureReallocatesSameOffsetAfterProfileDeleted(t *testing.T) {
	t.Setenv(registryDirEnv, t.TempDir())
	t.Setenv(forceReallocateEnv, "")

	repoRoot := writeLinkedWorktreeFixture(t)
	first, err := Ensure(repoRoot)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	if first == nil {
		t.Fatal("expected profile")
	}

	if err := os.Remove(filepath.Join(repoRoot, ".ot", "local-dev-profile.json")); err != nil {
		t.Fatalf("remove profile: %v", err)
	}

	second, err := Ensure(repoRoot)
	if err != nil {
		t.Fatalf("Ensure() after profile delete returned error: %v", err)
	}
	if second == nil {
		t.Fatal("expected profile after delete")
	}
	if second.Offset != first.Offset {
		t.Fatalf("Offset changed after profile delete: %d -> %d", first.Offset, second.Offset)
	}
	if second.InstanceID != first.InstanceID {
		t.Fatalf("InstanceID changed after profile delete: %q -> %q", first.InstanceID, second.InstanceID)
	}
}

// OT_WORKTREE_REASSIGN_PORTS promises a fresh port block, not fresh
// credentials: the per-worktree postgres containers keep their existing
// passwords, so reallocation must reuse the prior profile's secrets.
func TestEnsureReassignPortsKeepsExistingDBSecrets(t *testing.T) {
	t.Setenv(registryDirEnv, t.TempDir())
	t.Setenv(forceReallocateEnv, "")

	repoRoot := writeLinkedWorktreeFixture(t)
	first, err := Ensure(repoRoot)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	if first == nil {
		t.Fatal("expected profile")
	}

	t.Setenv(forceReallocateEnv, "1")
	second, err := Ensure(repoRoot)
	if err != nil {
		t.Fatalf("Ensure() with reassign returned error: %v", err)
	}
	if second == nil {
		t.Fatal("expected profile under reassign")
	}
	if second.Secrets.AppDBPassword != first.Secrets.AppDBPassword {
		t.Fatalf("app DB password regenerated under %s; existing container would refuse the new credentials", forceReallocateEnv)
	}
	if second.Secrets.CRMDBPassword != first.Secrets.CRMDBPassword {
		t.Fatalf("crm DB password regenerated under %s", forceReallocateEnv)
	}
	if second.Containers != first.Containers {
		t.Fatalf("container names changed: %+v -> %+v", first.Containers, second.Containers)
	}
}

func TestShellExportLinesFromEnvIncludesLocalOverrides(t *testing.T) {
	t.Setenv(activeEnv, "1")
	t.Setenv("APP_PORT", "24005")
	t.Setenv("DB_URL", "postgresql://postgres:postgres@127.0.0.1:24009/app")
	t.Setenv("NEXT_PUBLIC_API_BASE_URL", "http://127.0.0.1:24005")

	got := strings.Join(ShellExportLinesFromEnv(), "\n")
	for _, want := range []string{
		"export APP_PORT='24005'",
		"export DB_URL='postgresql://postgres:postgres@127.0.0.1:24009/app'",
		"export NEXT_PUBLIC_API_BASE_URL='http://127.0.0.1:24005'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ShellExportLinesFromEnv() missing %q in:\n%s", want, got)
		}
	}
}

// MCP tunnel URLs and other “dynamicBackendEnvKeys“ are recomputed by each
// “ot up backend“ cycle. If the caller's shell still has a stale value from
// a previous run, the procfile prefix (and the run.Spec wrapper) would
// otherwise re-export that stale value and overwrite the freshly-computed one
// that overmind injects, sending the web/worker to the wrong tunnel.
func TestShellExportLinesFromEnvSkipsDynamicBackendKeys(t *testing.T) {
	t.Setenv(activeEnv, "1")
	t.Setenv("APP_PORT", "24005")
	t.Setenv("MCP_PUBLIC_URL", "https://stale-from-previous-run.example/mcp")
	t.Setenv("OT_BACKEND_MCP_TUNNEL_HOST", "stale-from-previous-run.example")
	t.Setenv("OT_BACKEND_MCP_TUNNEL_PUBLIC_URL", "https://stale-from-previous-run.example/mcp")
	t.Setenv("DEMO_DESTINATION_MCP_PUBLIC_URL", "https://stale-from-previous-run.example/demo-destination/mcp")

	got := strings.Join(ShellExportLinesFromEnv(), "\n")
	// Non-dynamic keys still flow through.
	if !strings.Contains(got, "export APP_PORT='24005'") {
		t.Fatalf("ShellExportLinesFromEnv() dropped APP_PORT:\n%s", got)
	}
	// Dynamic keys are excluded so overmind's freshly-computed value wins.
	for _, leaked := range []string{
		"MCP_PUBLIC_URL=",
		"OT_BACKEND_MCP_TUNNEL_HOST=",
		"OT_BACKEND_MCP_TUNNEL_PUBLIC_URL=",
		"DEMO_DESTINATION_MCP_PUBLIC_URL=",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("ShellExportLinesFromEnv() leaked stale dynamic key %q in:\n%s", leaked, got)
		}
	}
}

func TestWrapSpecWithShellExportsKeepsExplicitEnvOverrides(t *testing.T) {
	t.Setenv(activeEnv, "1")
	t.Setenv("HARNESS_BACKEND_URL", "http://127.0.0.1:52785")
	t.Setenv("NEXT_PUBLIC_API_BASE_URL", "http://127.0.0.1:52785")

	spec := WrapSpecWithShellExports(run.Spec{
		Program: "uv",
		Args:    []string{"run", "mcp-test-harness"},
		Env: map[string]string{
			"HARNESS_BACKEND_URL": "https://demo-machine.example.ts.net/_ot/local",
		},
	})

	command := strings.Join(spec.Args, " ")
	if strings.Contains(command, "export HARNESS_BACKEND_URL='http://127.0.0.1:52785'") {
		t.Fatalf("WrapSpecWithShellExports() should not overwrite explicit Env values:\n%s", command)
	}
	if !strings.Contains(command, "export NEXT_PUBLIC_API_BASE_URL='http://127.0.0.1:52785'") {
		t.Fatalf("WrapSpecWithShellExports() should keep non-overridden worktree exports:\n%s", command)
	}
}
