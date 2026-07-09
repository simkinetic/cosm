package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTutorial_Lua encodes docs/tutorial-lua.md end to end: publish a library,
// consume it from an app, build, and run the program.
func TestTutorial_Lua(t *testing.T) {
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

	// 1. depot + extension
	ok(runCLI(t, home, "setup"))
	buildExtInto(t, filepath.Join(home, ".cosm"), "lua")

	// 2. registry
	ok(runCLI(t, home, "registry", "init", "cosmlua", bare(t, home, "cosmlua.git")))

	// 3. library
	lib := filepath.Join(home, "strutil")
	os.MkdirAll(lib, 0o755)
	ok(runCLI(t, lib, "init", "strutil", "v0.1.0", "--build", "lua"))
	writeFile(filepath.Join(lib, "src", "strutil@v0", "strutil.lua"),
		"local strutil = {}\nfunction strutil.greet(name)\n  return \"Hello, \" .. name .. \"!\"\nend\nreturn strutil\n")

	// 4. release
	strutilRemote := bare(t, home, "strutil.git")
	gitRun(t, lib, "init")
	gitRun(t, lib, "add", ".")
	gitRun(t, lib, "commit", "-m", "initial strutil")
	gitRun(t, lib, "branch", "-M", "main")
	gitRun(t, lib, "remote", "add", "origin", strutilRemote)
	gitRun(t, lib, "push", "-u", "origin", "main")
	ok(runCLI(t, lib, "release", "v0.1.0"))

	// 5. register
	ok(runCLI(t, home, "registry", "add", "cosmlua", strutilRemote))
	if out := ok(runCLI(t, home, "registry", "status", "cosmlua")); !strings.Contains(out, "strutil") || !strings.Contains(out, "v0.1.0") {
		t.Fatalf("registry status: %q", out)
	}

	// 6. app
	app := filepath.Join(home, "greeter")
	os.MkdirAll(app, 0o755)
	ok(runCLI(t, app, "init", "greeter", "--build", "lua"))
	ok(runCLI(t, app, "add", "strutil", "v0.1.0"))
	writeFile(filepath.Join(app, "src", "main.lua"),
		"local strutil = require(\"strutil@v0.strutil\")\nprint(strutil.greet(\"World\"))\n")

	// 7. build + run
	if out := ok(runCLI(t, app, "status")); !strings.Contains(out, "strutil v0.1.0") {
		t.Errorf("status: %q", out)
	}
	ok(runCLI(t, app, "build"))

	if _, err := exec.LookPath("lua"); err != nil {
		t.Log("lua not installed; verifying the environment instead of executing")
		if out := ok(runCLI(t, app, "run", "--", "sh", "-c", "echo LP=$LUA_PATH")); !strings.Contains(out, "?.lua") {
			t.Errorf("LUA_PATH not wired: %q", out)
		}
		return
	}
	out := ok(runCLI(t, app, "run", "--", "lua", "src/main.lua"))
	if !strings.Contains(out, "Hello, World!") {
		t.Fatalf("program output = %q, want 'Hello, World!'", out)
	}

	// 8. develop: edit the library live and see the change.
	ok(runCLI(t, app, "develop", "strutil"))
	if out := ok(runCLI(t, app, "status")); !strings.Contains(out, "(develop)") {
		t.Errorf("expected develop marker: %q", out)
	}
	devSrc := filepath.Join(home, ".cosm", "dev", "strutil@v0", "src", "strutil@v0", "strutil.lua")
	writeFile(devSrc, "local strutil = {}\nfunction strutil.greet(name)\n  return \"HELLO \" .. name\nend\nreturn strutil\n")
	out = ok(runCLI(t, app, "run", "--", "lua", "src/main.lua"))
	if !strings.Contains(out, "HELLO World") {
		t.Fatalf("develop edit not reflected: %q", out)
	}
	ok(runCLI(t, app, "free", "strutil"))
	out = ok(runCLI(t, app, "run", "--", "lua", "src/main.lua"))
	if !strings.Contains(out, "Hello, World!") {
		t.Fatalf("after free, expected released behavior: %q", out)
	}
}
