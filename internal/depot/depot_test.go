package depot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupAndVerify(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cosm")
	d := New(root)
	if d.IsInitialized() {
		t.Fatal("should not be initialized before setup")
	}
	// Isolate XDG so the test doesn't touch the real home.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := d.Setup(); err != nil {
		t.Fatal(err)
	}
	if !d.IsInitialized() {
		t.Fatal("should be initialized after setup")
	}
	for _, sub := range requiredDirs {
		if _, err := os.Stat(filepath.Join(root, sub)); err != nil {
			t.Errorf("missing dir %s", sub)
		}
	}
	if _, err := os.Stat(d.RegistriesFile()); err != nil {
		t.Error("registries.json not created")
	}
	if _, err := os.Stat(d.ConfigFile()); err != nil {
		t.Error("config.json not created")
	}
	if err := d.Setup(); err != nil {
		t.Fatalf("setup should be idempotent: %v", err)
	}
}

func TestResolveRootPrecedence(t *testing.T) {
	// flag wins.
	got, err := ResolveRoot("/tmp/explicit")
	if err != nil || got != "/tmp/explicit" {
		t.Fatalf("flag precedence: %q %v", got, err)
	}
	// env next.
	t.Setenv("COSM_DEPOT", "/tmp/fromenv")
	got, _ = ResolveRoot("")
	if got != "/tmp/fromenv" {
		t.Fatalf("env precedence: %q", got)
	}
}

func TestPathHelpers(t *testing.T) {
	d := New("/depot")
	if d.Build("uuid", "commit", "sha256:abc") != "/depot/builds/uuid/commit/sha256_abc" {
		t.Errorf("build path: %s", d.Build("uuid", "commit", "sha256:abc"))
	}
	if d.DevUnit("terrastd", 0) != "/depot/dev/terrastd@v0" {
		t.Errorf("dev unit path: %s", d.DevUnit("terrastd", 0))
	}
	if d.Mirror("u") != "/depot/mirrors/u.git" {
		t.Errorf("mirror path: %s", d.Mirror("u"))
	}
}

func TestLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cosm")
	d := New(root)
	rel, err := d.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if err := rel(); err != nil {
		t.Fatal(err)
	}
	// Re-acquire after release must succeed.
	rel2, err := d.Lock()
	if err != nil {
		t.Fatal(err)
	}
	_ = rel2()
}
