package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntegration_RegistrySync proves registry-side tag discovery: after a
// package is registered, a new tag pushed to its remote is picked up by
// `cosm registry sync` (the operation a registry's CI runs on a schedule).
func TestIntegration_RegistrySync(t *testing.T) {
	home := setupEnv(t)
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("failed: %v\n%s", err, out)
		}
		return out
	}
	ok(runCLI(t, home, "setup"))
	ok(runCLI(t, home, "registry", "init", "R", bare(t, home, "reg.git")))

	// A package released at v0.1.0 and registered.
	lib := filepath.Join(home, "lib")
	os.MkdirAll(filepath.Join(lib, "src", "lib@v0"), 0o755)
	ok(runCLI(t, lib, "init", "lib", "v0.1.0", "--build", "lua"))
	os.WriteFile(filepath.Join(lib, "src", "lib@v0", "lib.lua"), []byte("return {}\n"), 0o644)
	remote := bare(t, home, "lib.git")
	gitRun(t, lib, "init")
	gitRun(t, lib, "add", ".")
	gitRun(t, lib, "commit", "-m", "init")
	gitRun(t, lib, "branch", "-M", "main")
	gitRun(t, lib, "remote", "add", "origin", remote)
	gitRun(t, lib, "push", "-u", "origin", "main")
	ok(runCLI(t, lib, "release", "v0.1.0"))
	ok(runCLI(t, home, "registry", "add", "R", remote))

	// A brand-new upstream release the registry doesn't know about yet.
	ok(runCLI(t, lib, "release", "--minor")) // v0.2.0, pushed to the lib remote
	if out := ok(runCLI(t, home, "registry", "status", "R")); strings.Contains(out, "v0.2.0") {
		t.Fatalf("v0.2.0 should not be registered before sync: %q", out)
	}

	// The registry CI runs sync -> discovers and registers v0.2.0.
	if out := ok(runCLI(t, home, "registry", "sync", "R")); !strings.Contains(out, "lib@v0.2.0") {
		t.Fatalf("sync should register v0.2.0: %q", out)
	}
	out := ok(runCLI(t, home, "registry", "status", "R"))
	if !strings.Contains(out, "v0.1.0") || !strings.Contains(out, "v0.2.0") {
		t.Fatalf("after sync, expected both versions: %q", out)
	}

	// A second sync with nothing new is a no-op.
	if out := ok(runCLI(t, home, "registry", "sync", "R")); !strings.Contains(out, "up to date") {
		t.Errorf("idempotent sync: %q", out)
	}
}
