package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDevelop_LocalSiblingBootstrap drives the whole unpublished-sibling flow
// through the CLI: create a local library, adopt it with `develop --path`, declare
// it with the `add` workspace-fallback, then build/run against the live source and
// see an edit reflected without any release.
func TestDevelop_LocalSiblingBootstrap(t *testing.T) {
	if _, err := exec.LookPath("lua"); err != nil {
		t.Skip("lua not installed")
	}
	home := setupEnv(t)
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, out)
		}
		return out
	}
	writeFile := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ok(runCLI(t, home, "setup"))
	buildExtInto(t, filepath.Join(home, ".cosm"), "lua")

	// An unpublished sibling library.
	sib := filepath.Join(home, "sib")
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}
	ok(runCLI(t, sib, "init", "sib", "v0.1.0", "--build", "lua"))
	writeFile(filepath.Join(sib, "src", "sib@v0", "sib.lua"),
		"local sib = {}\nfunction sib.hi() return \"from sib\" end\nreturn sib\n")

	// The app that consumes it.
	app := filepath.Join(home, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	ok(runCLI(t, app, "init", "app", "--build", "lua"))
	writeFile(filepath.Join(app, "src", "main.lua"),
		"local sib = require(\"sib@v0.sib\")\nprint(sib.hi())\n")

	// Bootstrap without ever publishing sib: adopt by path, then declare the dep.
	ok(runCLI(t, app, "develop", "sib", "--path", sib))
	ok(runCLI(t, app, "add", "sib"))

	if out := ok(runCLI(t, app, "status")); !strings.Contains(out, "sib") || !strings.Contains(out, "(develop)") {
		t.Fatalf("status missing develop sib: %q", out)
	}

	if out := ok(runCLI(t, app, "run", "--", "lua", "src/main.lua")); !strings.Contains(out, "from sib") {
		t.Fatalf("run output = %q, want 'from sib'", out)
	}

	// A live edit to the sibling is picked up with no release.
	writeFile(filepath.Join(sib, "src", "sib@v0", "sib.lua"),
		"local sib = {}\nfunction sib.hi() return \"edited sib\" end\nreturn sib\n")
	if out := ok(runCLI(t, app, "run", "--", "lua", "src/main.lua")); !strings.Contains(out, "edited sib") {
		t.Fatalf("live edit not reflected: %q", out)
	}
}
