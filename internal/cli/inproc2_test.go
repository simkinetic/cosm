package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeLuaDep creates a lua package with releases v0.1.1 and v0.2.0 pushed to a
// fresh bare remote, returning the remote URL.
func makeLuaDep(t *testing.T, home, name string) string {
	t.Helper()
	dir := filepath.Join(home, name)
	if err := os.MkdirAll(filepath.Join(dir, "src", name+"@v0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "init", name, "v0.1.0", "--build", "lua"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "src", name+"@v0", name+".lua"), []byte("return 1\n"), 0o644)
	remote := bare(t, home, name+".git")
	gitRun(t, dir, "init")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "init")
	gitRun(t, dir, "branch", "-M", "main")
	gitRun(t, dir, "remote", "add", "origin", remote)
	gitRun(t, dir, "push", "-u", "origin", "main")
	if _, err := runCLI(t, dir, "release", "--patch"); err != nil { // v0.1.1
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "release", "--minor"); err != nil { // v0.2.0
		t.Fatal(err)
	}
	return remote
}

func TestInProcess_RegistryAndBuildCommands(t *testing.T) {
	home := setupEnv(t)
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("failed: %v\n%s", err, out)
		}
		return out
	}
	ok(runCLI(t, home, "setup"))
	buildLuaExt(t, filepath.Join(home, ".cosm"))

	regRemote := bare(t, home, "reg.git")
	ok(runCLI(t, home, "registry", "init", "R", regRemote))
	depRemote := makeLuaDep(t, home, "dep")
	ok(runCLI(t, home, "registry", "add", "R", depRemote))

	// registry update (pull, no-op) and status.
	ok(runCLI(t, home, "update", "R"))
	ok(runCLI(t, home, "update"))

	// registry rm a version, then confirm status reflects it.
	ok(runCLI(t, home, "registry", "rm", "R", "dep", "v0.2.0", "--force"))

	// delete R locally then clone it back from its remote.
	ok(runCLI(t, home, "registry", "delete", "R", "--force"))
	ok(runCLI(t, home, "registry", "clone", regRemote))
	ok(runCLI(t, home, "registry", "status", "R"))

	// App using the dep; exercise env, test, upgrade --latest, develop --purge.
	appDir := filepath.Join(home, "app")
	os.MkdirAll(filepath.Join(appDir, "src", "app@v0"), 0o755)
	ok(runCLI(t, appDir, "init", "app", "--build", "lua"))
	ok(runCLI(t, appDir, "add", "dep", "v0.1.1"))
	ok(runCLI(t, appDir, "build"))
	ok(runCLI(t, appDir, "test"))
	if out := ok(runCLI(t, appDir, "env")); !strings.Contains(out, "LUA_PATH") {
		t.Errorf("env: %q", out)
	}
	// --latest stays within major 0; v0.2.0 was removed, so it remains v0.1.1.
	ok(runCLI(t, appDir, "upgrade", "dep", "--latest"))
	ok(runCLI(t, appDir, "upgrade", "--all"))

	ok(runCLI(t, appDir, "develop", "dep"))
	ok(runCLI(t, appDir, "free", "dep", "--purge"))
	if _, err := os.Stat(filepath.Join(home, ".cosm", "dev", "dep@v0")); !os.IsNotExist(err) {
		t.Errorf("purge should remove the shared checkout")
	}
}

func TestInProcess_ErrorPaths(t *testing.T) {
	home := setupEnv(t)
	runCLI(t, home, "setup")

	appDir := filepath.Join(home, "app")
	os.MkdirAll(appDir, 0o755)
	if _, err := runCLI(t, appDir, "init", "app", "--build", "lua"); err != nil {
		t.Fatal(err)
	}
	// duplicate init
	if _, err := runCLI(t, appDir, "init", "app"); err == nil {
		t.Error("expected duplicate init error")
	}
	// add unknown package
	if _, err := runCLI(t, appDir, "add", "ghost", "v1.0.0"); err == nil {
		t.Error("expected unknown package error")
	}
	// bad version to init
	badDir := filepath.Join(home, "bad")
	os.MkdirAll(badDir, 0o755)
	if _, err := runCLI(t, badDir, "init", "bad", "1.0"); err == nil {
		t.Error("expected invalid version error")
	}
	// release with no git repo
	if _, err := runCLI(t, appDir, "release", "--patch"); err == nil {
		t.Error("expected release error (dirty/no repo)")
	}
	// upgrade non-dependency
	if _, err := runCLI(t, appDir, "upgrade", "nope"); err == nil {
		t.Error("expected upgrade unknown error")
	}
	// downgrade non-dependency
	if _, err := runCLI(t, appDir, "downgrade", "nope", "v1.0.0"); err == nil {
		t.Error("expected downgrade unknown error")
	}
	// develop/free unknown
	if _, err := runCLI(t, appDir, "develop", "nope"); err == nil {
		t.Error("expected develop unknown error")
	}
	if _, err := runCLI(t, appDir, "free", "nope"); err == nil {
		t.Error("expected free unknown error")
	}
	// rm unknown
	if _, err := runCLI(t, appDir, "rm", "nope"); err == nil {
		t.Error("expected rm unknown error")
	}
}
