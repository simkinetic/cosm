package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envAt points the CLI + git at a given HOME/depot.
func envAt(t *testing.T, home string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "gitconfig"),
		[]byte("[user]\n\tname=t\n\temail=t@t\n[init]\n\tdefaultBranch=main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("COSM_DEPOT", filepath.Join(home, ".cosm"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
}

// TestInProcess_BinaryPublishConsume publishes a package's binary from one depot
// and consumes it — with no source access — from a separate depot (§8.5).
func TestInProcess_BinaryPublishConsume(t *testing.T) {
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("failed: %v\n%s", err, out)
		}
		return out
	}
	pub := t.TempDir()
	con := t.TempDir()
	store := filepath.Join(pub, "store")

	// --- Publisher depot ---
	envAt(t, pub)
	ok(runCLI(t, pub, "setup"))
	buildLuaExt(t, filepath.Join(pub, ".cosm"))
	binRemote := bare(t, pub, "binreg.git")
	ok(runCLI(t, pub, "registry", "init", "B", binRemote, "--kind", "mixed"))

	depDir := filepath.Join(pub, "dep")
	os.MkdirAll(filepath.Join(depDir, "src", "dep@v0"), 0o755)
	ok(runCLI(t, depDir, "init", "dep", "v0.1.0", "--build", "lua"))
	os.WriteFile(filepath.Join(depDir, "src", "dep@v0", "dep.lua"), []byte("return 1\n"), 0o644)
	ok(runCLI(t, depDir, "publish", "--registry", "B", "--store", store))

	if entries, _ := os.ReadDir(store); len(entries) == 0 {
		t.Fatal("artifact store empty after publish")
	}

	// --- Consumer depot (no source access) ---
	envAt(t, con)
	ok(runCLI(t, con, "setup"))
	buildLuaExt(t, filepath.Join(con, ".cosm"))
	ok(runCLI(t, con, "registry", "clone", binRemote))

	appDir := filepath.Join(con, "app")
	os.MkdirAll(filepath.Join(appDir, "src", "app@v0"), 0o755)
	ok(runCLI(t, appDir, "init", "app", "--build", "lua"))
	ok(runCLI(t, appDir, "add", "dep", "v0.1.0"))
	ok(runCLI(t, appDir, "build"))

	// The dependency must have been materialized from the binary artifact.
	if !hasBinaryBuild(t, filepath.Join(con, ".cosm", "builds")) {
		t.Error("expected a binary-sourced build in the consumer depot")
	}
	if out := ok(runCLI(t, appDir, "run", "--", "sh", "-c", "echo LP=$LUA_PATH")); !strings.Contains(out, "?.lua") {
		t.Errorf("run LUA_PATH missing dependency root: %q", out)
	}
}

func hasBinaryBuild(t *testing.T, buildsDir string) bool {
	t.Helper()
	found := false
	filepath.WalkDir(buildsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "meta.json" {
			return nil
		}
		data, _ := os.ReadFile(p)
		if strings.Contains(string(data), `"source": "binary"`) {
			found = true
		}
		return nil
	})
	return found
}
