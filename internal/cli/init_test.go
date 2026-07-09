package cli

import (
	"os"
	"path/filepath"
	"testing"

	"cosm/internal/manifest"
)

// TestSetupWritesDevGuide verifies `cosm setup` drops the agent guide into the
// develop workspace, and that a second setup never clobbers a user's edits.
func TestSetupWritesDevGuide(t *testing.T) {
	home := setupEnv(t)
	prev := DevWorkspaceGuide
	DevWorkspaceGuide = "GUIDE BODY"
	t.Cleanup(func() { DevWorkspaceGuide = prev })

	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}
	guide := filepath.Join(home, ".cosm", "dev", "CLAUDE.md")
	got, err := os.ReadFile(guide)
	if err != nil {
		t.Fatalf("guide not written: %v", err)
	}
	if string(got) != "GUIDE BODY" {
		t.Fatalf("guide = %q", got)
	}

	// A user edit must survive a re-run of setup.
	if err := os.WriteFile(guide, []byte("EDITED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(guide)
	if string(got) != "EDITED" {
		t.Fatalf("setup clobbered a user edit: %q", got)
	}
}

// TestInitScaffoldsProvides verifies `cosm init --build lua` delegates to the
// extension: it sets `provides` (namespace derived from the version's major) and
// lays down the matching source stub, so the user need not hand-edit either.
func TestInitScaffoldsProvides(t *testing.T) {
	home := setupEnv(t)
	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}
	buildExtInto(t, filepath.Join(home, ".cosm"), "lua")

	dir := filepath.Join(home, "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A non-zero major proves the namespace tracks the version, not a hardcoded @v0.
	if _, err := runCLI(t, dir, "init", "widget", "v2.1.0", "--build", "lua"); err != nil {
		t.Fatal(err)
	}

	m, err := manifest.LoadManifestFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Provides) != 1 || m.Provides[0] != "widget@v2" {
		t.Fatalf("provides = %v, want [widget@v2]", m.Provides)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "widget@v2", "widget.lua")); err != nil {
		t.Fatalf("source stub not created: %v", err)
	}
}

// TestInitMissingExtensionDegrades verifies init still writes the manifest (no
// provides, no error) when the named build extension isn't installed.
func TestInitMissingExtensionDegrades(t *testing.T) {
	home := setupEnv(t)
	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "init", "widget", "--build", "nope"); err != nil {
		t.Fatalf("init must not fail when extension is absent: %v", err)
	}
	m, err := manifest.LoadManifestFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Build != "nope" {
		t.Fatalf("build = %q, want nope", m.Build)
	}
	if len(m.Provides) != 0 {
		t.Fatalf("provides = %v, want none", m.Provides)
	}
}
