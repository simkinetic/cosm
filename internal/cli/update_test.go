package cli

import (
	"strings"
	"testing"
)

// TestIntegration_UpdateSyncsRemotes proves `cosm update` pulls changes another
// user pushed to a shared registry remote (§12.6): depot B clones a registry,
// depot A adds a new package, and B sees it only after `cosm update`.
func TestIntegration_UpdateSyncsRemotes(t *testing.T) {
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("failed: %v\n%s", err, out)
		}
		return out
	}
	a := t.TempDir() // writer depot
	b := t.TempDir() // reader depot

	// Depot A: create a registry (shared remote) and register 'alpha'.
	envAt(t, a)
	ok(runCLI(t, a, "setup"))
	regRemote := bare(t, a, "reg.git")
	ok(runCLI(t, a, "registry", "init", "R", regRemote))
	ok(runCLI(t, a, "registry", "add", "R", makeLuaDep(t, a, "alpha")))

	// Depot B: clone the registry — sees alpha, not beta.
	envAt(t, b)
	ok(runCLI(t, b, "setup"))
	ok(runCLI(t, b, "registry", "clone", regRemote))
	if out := ok(runCLI(t, b, "registry", "status", "R")); !strings.Contains(out, "alpha") {
		t.Fatalf("cloned registry should have alpha: %q", out)
	}

	// Depot A: register a second package 'beta' (pushes to the shared remote).
	envAt(t, a)
	ok(runCLI(t, a, "registry", "add", "R", makeLuaDep(t, a, "beta")))

	// Depot B: before update it doesn't know about beta.
	envAt(t, b)
	if out := ok(runCLI(t, b, "registry", "status", "R")); strings.Contains(out, "beta") {
		t.Fatalf("beta should not be visible before update: %q", out)
	}

	// `cosm update` (no args) syncs all registries -> beta appears.
	if out := ok(runCLI(t, b, "update")); !strings.Contains(out, "Updated R") {
		t.Fatalf("update output: %q", out)
	}
	out := ok(runCLI(t, b, "registry", "status", "R"))
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("after update, expected both alpha and beta: %q", out)
	}
}

// TestIntegration_UpdateNoRegistries handles the empty case gracefully.
func TestIntegration_UpdateNoRegistries(t *testing.T) {
	home := setupEnv(t)
	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, home, "update")
	if err != nil {
		t.Fatalf("update with no registries should succeed: %v", err)
	}
	if !strings.Contains(out, "No registries") {
		t.Errorf("expected 'No registries' message: %q", out)
	}
}
