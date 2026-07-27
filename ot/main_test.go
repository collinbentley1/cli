package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMainBinarySmoke is a real end-to-end smoke test of the package main
// shim: it compiles the binary and runs `ot --help`, exercising
// main() -> cli.Execute() -> buildCommands().Execute() for real. `--help`
// is short-circuited by cobra before PersistentPreRunE runs, so it needs
// no repo, no Docker, and no network. This replaces a prior placebo test
// (which asserted 1+1==2 and only served to make package main report "ok").
func TestMainBinarySmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build-and-run smoke test in -short mode")
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}

	binPath := filepath.Join(t.TempDir(), "ot")
	build := exec.Command(goBin, "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ot binary: %v\n%s", err, out)
	}

	out, err := exec.Command(binPath, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("ot --help failed: %v\n%s", err, out)
	}

	got := string(out)
	// The help text must advertise the tool and at least one real subcommand
	// wired up by buildCommands(); a broken shim or command tree fails here.
	for _, want := range []string{"per-worktree", "Available Commands", "up", "down"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ot --help output missing %q:\n%s", want, got)
		}
	}
}
