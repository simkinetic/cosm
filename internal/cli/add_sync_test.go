package cli

import (
	"os"
	"path/filepath"
	"testing"

	"cosm/internal/manifest"
)

// TestAdd_LazySyncOnMiss: a version published to the registry after the local
// clone's last pull is invisible to a stale `add` — with `--offline` that's an
// (actionable) error, but a plain `add` lazily pulls the registry and succeeds.
func TestAdd_LazySyncOnMiss(t *testing.T) {
	home := setupEnv(t)
	depotA := filepath.Join(home, ".cosm")
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, out)
		}
		return out
	}

	ok(runCLI(t, home, "setup"))
	buildExtInto(t, depotA, "lua")
	regRemote := bare(t, home, "reg.git")
	ok(runCLI(t, home, "registry", "init", "R", regRemote))

	// foo v0.1.0 published + registered in depot A.
	foo := filepath.Join(home, "foo")
	if err := os.MkdirAll(filepath.Join(foo, "src", "foo@v0"), 0o755); err != nil {
		t.Fatal(err)
	}
	ok(runCLI(t, foo, "init", "foo", "v0.1.0", "--build", "lua"))
	if err := os.WriteFile(filepath.Join(foo, "src", "foo@v0", "foo.lua"), []byte("return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fooRemote := bare(t, home, "foo.git")
	gitRun(t, foo, "init")
	gitRun(t, foo, "add", ".")
	gitRun(t, foo, "commit", "-m", "init")
	gitRun(t, foo, "branch", "-M", "main")
	gitRun(t, foo, "remote", "add", "origin", fooRemote)
	gitRun(t, foo, "push", "-u", "origin", "main")
	ok(runCLI(t, foo, "release", "v0.1.0"))
	ok(runCLI(t, home, "registry", "add", "R", fooRemote))

	// Publish v0.2.0 and register it via a SECOND depot — depot A's clone stays stale.
	ok(runCLI(t, foo, "release", "--minor")) // v0.2.0 -> foo remote
	t.Setenv("COSM_DEPOT", filepath.Join(home, ".cosm2"))
	ok(runCLI(t, home, "setup"))
	ok(runCLI(t, home, "registry", "clone", regRemote))
	ok(runCLI(t, home, "registry", "add", "R", fooRemote)) // pushes v0.2.0 into the reg remote
	t.Setenv("COSM_DEPOT", depotA)                         // back to the stale depot

	app := filepath.Join(home, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	ok(runCLI(t, app, "init", "app", "--build", "lua"))

	// --offline: the stale clone lacks v0.2.0, so this errors (no pull).
	if out, err := runCLI(t, app, "add", "foo", "v0.2.0", "--offline"); err == nil {
		t.Fatalf("offline add of an unsynced version should fail: %q", out)
	}

	// Online: lazy sync pulls v0.2.0, then the add succeeds.
	ok(runCLI(t, app, "add", "foo", "v0.2.0"))
	m, err := manifest.LoadManifestFromDir(app)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range m.Deps {
		if d.Name == "foo" && d.Version == "v0.2.0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("foo v0.2.0 not added after lazy sync: %+v", m.Deps)
	}
}
