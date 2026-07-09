package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/registry"
)

// publishLuaLib creates a Lua library (with the given body and already-registered
// deps), releases it, and registers it. Returns nothing; the package is in `reg`.
func publishLuaLib(t *testing.T, home, reg, name, body string, deps [][2]string) {
	t.Helper()
	dir := filepath.Join(home, name)
	if err := os.MkdirAll(filepath.Join(dir, "src", name+"@v0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "init", name, "v0.1.0", "--build", "lua"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", name+"@v0", name+".lua"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, d := range deps {
		if _, err := runCLI(t, dir, "add", d[0], d[1]); err != nil {
			t.Fatalf("add %v: %v", d, err)
		}
	}
	remote := bare(t, home, name+".git")
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "init")
	gitRun(t, dir, "branch", "-M", "main")
	gitRun(t, dir, "remote", "add", "origin", remote)
	gitRun(t, dir, "push", "-u", "origin", "main")
	if _, err := runCLI(t, dir, "release", "v0.1.0"); err != nil {
		t.Fatalf("release %s: %v", name, err)
	}
	if _, err := runCLI(t, home, "registry", "add", reg, remote); err != nil {
		t.Fatalf("registry add %s: %v", name, err)
	}
}

// TestIntegration_Diamond builds and runs a diamond: app -> {b, c}, and both
// b -> d and c -> d. The shared transitive dep d must be resolved once and wired
// into both parents; the program links through the whole graph at runtime.
func TestIntegration_Diamond(t *testing.T) {
	home := setupEnv(t)
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("failed: %v\n%s", err, out)
		}
		return out
	}
	ok(runCLI(t, home, "setup"))
	buildExtInto(t, filepath.Join(home, ".cosm"), "lua")
	ok(runCLI(t, home, "registry", "init", "R", bare(t, home, "reg.git")))

	// d is the shared leaf; b and c both use it; app uses b and c.
	publishLuaLib(t, home, "R", "d",
		"local M = {}\nfunction M.val() return \"D\" end\nreturn M\n", nil)
	publishLuaLib(t, home, "R", "b",
		"local d = require(\"d@v0.d\")\nlocal M = {}\nfunction M.f() return d.val() end\nreturn M\n",
		[][2]string{{"d", "v0.1.0"}})
	publishLuaLib(t, home, "R", "c",
		"local d = require(\"d@v0.d\")\nlocal M = {}\nfunction M.f() return d.val() end\nreturn M\n",
		[][2]string{{"d", "v0.1.0"}})

	app := filepath.Join(home, "app")
	os.MkdirAll(app, 0o755)
	ok(runCLI(t, app, "init", "app", "--build", "lua"))
	ok(runCLI(t, app, "add", "b", "v0.1.0"))
	ok(runCLI(t, app, "add", "c", "v0.1.0"))
	os.MkdirAll(filepath.Join(app, "src"), 0o755)
	os.WriteFile(filepath.Join(app, "src", "main.lua"),
		[]byte("local b = require(\"b@v0.b\")\nlocal c = require(\"c@v0.c\")\nprint(b.f() .. c.f())\n"), 0o644)

	// Build list must contain exactly b, c, d (d shared, once).
	out := ok(runCLI(t, app, "status"))
	if !strings.Contains(out, "Resolved 3 dependencies") {
		t.Errorf("expected 3 resolved deps (b, c, d): %q", out)
	}
	ok(runCLI(t, app, "build"))

	if _, err := exec.LookPath("lua"); err != nil {
		t.Skip("lua not installed; build/resolve verified")
	}
	res := ok(runCLI(t, app, "run", "--", "lua", "src/main.lua"))
	if !strings.Contains(res, "DD") {
		t.Fatalf("diamond program output = %q, want 'DD'", res)
	}
}

// TestIntegration_IntegrityMismatch proves a corrupted recorded tree hash is
// caught during materialization (§6.5, §8.1) — a guarantee unit tests can only
// approximate.
func TestIntegration_IntegrityMismatch(t *testing.T) {
	home := setupEnv(t)
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("failed: %v\n%s", err, out)
		}
		return out
	}
	ok(runCLI(t, home, "setup"))
	buildExtInto(t, filepath.Join(home, ".cosm"), "lua")
	ok(runCLI(t, home, "registry", "init", "R", bare(t, home, "reg.git")))
	publishLuaLib(t, home, "R", "lib",
		"local M = {}\nfunction M.hi() return \"hi\" end\nreturn M\n", nil)

	// Corrupt the recorded tree hash in the registry.
	specsPath := registry.SpecsFile(filepath.Join(home, ".cosm", "registries", "R"), "lib", "v0.1.0")
	sp, err := manifest.LoadSpecs(specsPath)
	if err != nil {
		t.Fatal(err)
	}
	sp.Tree = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if err := manifest.SaveSpecs(specsPath, sp); err != nil {
		t.Fatal(err)
	}

	app := filepath.Join(home, "app")
	os.MkdirAll(app, 0o755)
	ok(runCLI(t, app, "init", "app", "--build", "lua"))
	ok(runCLI(t, app, "add", "lib", "v0.1.0"))

	// Building must fail with an integrity error when the source is materialized.
	_, err = runCLI(t, app, "build")
	if err == nil {
		t.Fatal("expected an integrity error building a tampered dependency")
	}
	if !errors.Is(err, errs.ErrIntegrityMismatch) {
		t.Fatalf("expected ErrIntegrityMismatch, got %v", err)
	}
}
