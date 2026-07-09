package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"cosm/internal/gitx"
)

// moduleRoot is captured at load time (before any t.Chdir) so `go build` can
// find go.mod regardless of the current working directory.
var moduleRoot = func() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(f), "..", ".."))
}()

// realHome is captured before tests override HOME, so `go build` for extensions
// still finds the real module cache.
var realHome = os.Getenv("HOME")

// runCLI executes the command tree in-process (so coverage is attributed),
// capturing stdout. cwd is set via t.Chdir.
func runCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	depotFlag = ""
	root := buildRoot()
	root.SetArgs(args)
	err := root.Execute()
	w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return string(data), err
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := (gitx.Exec{}).Run(dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func bare(t *testing.T, home, name string) string {
	t.Helper()
	p := filepath.Join(home, "remotes", name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, p, "init", "--bare")
	gitRun(t, p, "symbolic-ref", "HEAD", "refs/heads/main")
	return "file://" + p
}

func buildLuaExt(t *testing.T, depotRoot string) { buildExtInto(t, depotRoot, "lua") }

// buildExtInto compiles cosm-ext-<id> into the depot's extensions directory.
func buildExtInto(t *testing.T, depotRoot, id string) {
	t.Helper()
	dest := filepath.Join(depotRoot, "extensions", id, "cosm-ext-"+id)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", dest, "./cmd/cosm-ext-"+id)
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(), "GOPROXY=off", "HOME="+realHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s ext: %v\n%s", id, err, out)
	}
}

func setupEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "gitconfig"),
		[]byte("[user]\n\tname=t\n\temail=t@t\n[init]\n\tdefaultBranch=main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("COSM_DEPOT", filepath.Join(home, ".cosm"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	return home
}

// TestInProcess_FullLifecycle drives the whole tool in-process: setup, registry,
// init/release/register, add, build, run, upgrade/downgrade, develop/free.
func TestInProcess_FullLifecycle(t *testing.T) {
	home := setupEnv(t)
	must := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("command failed: %v\n%s", err, out)
		}
		return out
	}

	must(runCLI(t, home, "setup"))
	buildLuaExt(t, filepath.Join(home, ".cosm"))

	regRemote := bare(t, home, "reg.git")
	must(runCLI(t, home, "registry", "init", "R", regRemote))

	// Dependency package with two releases.
	depDir := filepath.Join(home, "dep")
	os.MkdirAll(filepath.Join(depDir, "src", "dep@v0"), 0o755)
	must(runCLI(t, depDir, "init", "dep", "v0.1.0", "--build", "lua"))
	os.WriteFile(filepath.Join(depDir, "src", "dep@v0", "dep.lua"), []byte("return 1\n"), 0o644)
	depRemote := bare(t, home, "dep.git")
	gitRun(t, depDir, "init")
	gitRun(t, depDir, "add", ".")
	gitRun(t, depDir, "commit", "-m", "init")
	gitRun(t, depDir, "branch", "-M", "main")
	gitRun(t, depDir, "remote", "add", "origin", depRemote)
	gitRun(t, depDir, "push", "-u", "origin", "main")
	must(runCLI(t, depDir, "release", "--patch")) // v0.1.1
	must(runCLI(t, depDir, "release", "--minor")) // v0.2.0

	must(runCLI(t, home, "registry", "add", "R", depRemote))
	if out := must(runCLI(t, home, "registry", "status", "R")); !strings.Contains(out, "dep:") {
		t.Errorf("registry status: %q", out)
	}

	// App.
	appDir := filepath.Join(home, "app")
	os.MkdirAll(filepath.Join(appDir, "src", "app@v0"), 0o755)
	must(runCLI(t, appDir, "init", "app", "--build", "lua"))
	must(runCLI(t, appDir, "add", "dep", "v0.1.1"))

	if out := must(runCLI(t, appDir, "status")); !strings.Contains(out, "dep v0.1.1") {
		t.Errorf("status: %q", out)
	}
	// upgrade within major (v0): -> v0.2.0
	if out := must(runCLI(t, appDir, "upgrade", "dep")); !strings.Contains(out, "v0.2.0") {
		t.Errorf("upgrade: %q", out)
	}
	// downgrade back to v0.1.1
	must(runCLI(t, appDir, "downgrade", "dep", "v0.1.1"))

	// build + run.
	must(runCLI(t, appDir, "build"))
	if out := must(runCLI(t, appDir, "env")); !strings.Contains(out, "LUA_PATH") {
		t.Errorf("env: %q", out)
	}

	// develop / free.
	must(runCLI(t, appDir, "develop", "dep"))
	if out := must(runCLI(t, appDir, "status")); !strings.Contains(out, "(develop)") {
		t.Errorf("status not develop: %q", out)
	}
	must(runCLI(t, appDir, "free", "dep"))

	// rm.
	must(runCLI(t, appDir, "rm", "dep"))
	if out := must(runCLI(t, appDir, "status")); !strings.Contains(out, "Resolved 0") {
		t.Errorf("status after rm: %q", out)
	}
}

func TestInProcess_Errors(t *testing.T) {
	home := setupEnv(t)
	// status in an uninitialized depot cwd with no project.
	runCLI(t, home, "setup")
	if _, err := runCLI(t, home, "status"); err == nil {
		t.Error("expected error: no cosm.json")
	}
	if _, err := runCLI(t, home, "registry", "status", "nope"); err == nil {
		t.Error("expected error: unknown registry")
	}
	if _, err := runCLI(t, home, "add", "ghost", "v1.0.0"); err == nil {
		t.Error("expected error: no project")
	}
}
