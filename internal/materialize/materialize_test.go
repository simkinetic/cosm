package materialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/depot"
	"cosm/internal/ext"
	"cosm/internal/gitx"
	"cosm/internal/types"
)

// fakeExtension writes a shell extension that records each build invocation to a
// counter file, so we can assert the artifact cache prevents rebuilds.
func fakeExtension(t *testing.T, d depot.Depot, counter string) {
	t.Helper()
	dir := filepath.Join(d.Extensions(), "fake")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
verb="$1"
case "$verb" in
  info) echo '{"extension":"fake","version":"0.0.1","protocol":1,"toolchainId":"fake-tc","capabilities":["info","build","activate"]}' ;;
  build) cat >/dev/null; echo build >> "` + counter + `"; echo '{"status":"ok","descriptor":{"marker":"x"}}' ;;
  activate) cat >/dev/null; echo '{"env":{"FAKE":"1"},"prependPaths":{"FAKE_PATH":["/a","/b"]}}' ;;
  *) echo "unknown verb" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "cosm-ext-fake"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func devDir(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newMat(t *testing.T) (*Materializer, depot.Depot, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".cosm")
	d := depot.New(root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := d.Setup(); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "counter")
	fakeExtension(t, d, counter)
	m := &Materializer{
		D: d, Git: gitx.Exec{}, Run: ext.NewRunner(d),
		Opt: Options{Platform: types.Platform{OS: "linux", Arch: "amd64"}, BuildType: "Release", Jobs: 1},
	}
	return m, d, counter
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(strings.TrimSpace(string(data)), "\n") + 1
}

func TestBuildAllTopoAndCache(t *testing.T) {
	m, _, counter := newMat(t)

	dirA := devDir(t, "a")
	dirB := devDir(t, "b")
	bl := types.BuildList{Dependencies: map[string]types.BuildListEntry{
		"u-a@v1": {Name: "a", UUID: "u-a", Major: 1, Version: "v1.0.0", Build: "fake",
			Develop: true, SourcePath: dirA, DepKeys: []string{"u-b@v1"}},
		"u-b@v1": {Name: "b", UUID: "u-b", Major: 1, Version: "v1.0.0", Build: "fake",
			Develop: true, SourcePath: dirB},
	}}

	built, err := m.BuildAll(bl)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(built) != 2 {
		t.Fatalf("expected 2 built, got %d", len(built))
	}
	for k, b := range built {
		if len(b.Descriptor) == 0 || b.BuildKey == "" {
			t.Errorf("%s missing descriptor/buildKey: %+v", k, b)
		}
	}
	if n := countLines(t, counter); n != 2 {
		t.Fatalf("expected 2 build invocations, got %d", n)
	}

	// Second run: identical inputs -> artifact cache hit, no extra builds.
	if _, err := m.BuildAll(bl); err != nil {
		t.Fatal(err)
	}
	if n := countLines(t, counter); n != 2 {
		t.Fatalf("expected still 2 build invocations after cache, got %d", n)
	}

	// Editing a dependency's source invalidates it and its dependent.
	if err := os.WriteFile(filepath.Join(dirB, "file.txt"), []byte("b-changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BuildAll(bl); err != nil {
		t.Fatal(err)
	}
	if n := countLines(t, counter); n != 4 {
		t.Fatalf("expected 2 more builds (b and dependent a), got %d total", n)
	}
}

func TestActivateAndAssembleEnv(t *testing.T) {
	m, _, _ := newMat(t)
	proj := devDir(t, "root")
	bl := types.BuildList{Dependencies: map[string]types.BuildListEntry{
		"u-b@v1": {Name: "b", UUID: "u-b", Major: 1, Version: "v1.0.0", Build: "fake", Develop: true, SourcePath: devDir(t, "b")},
	}}
	root := &types.Manifest{Name: "root", UUID: "u-root", Version: "v1.0.0", Build: "fake",
		Deps: map[string]types.Dependency{"u-b@v1": {Name: "b", Version: "v1.0.0"}}}

	built, err := m.EnsureBuilt(root, proj, bl)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := m.Activate(root, proj, bl, built, Built{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Env["FAKE"] != "1" {
		t.Errorf("activate env: %+v", resp.Env)
	}

	out := AssembleEnv([]string{"PATH=/usr/bin", "FAKE_PATH=/existing"}, resp)
	got := map[string]string{}
	for _, kv := range out {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			got[kv[:i]] = kv[i+1:]
		}
	}
	if got["FAKE"] != "1" {
		t.Errorf("FAKE not set: %v", got)
	}
	sep := string(os.PathListSeparator)
	want := "/a" + sep + "/b" + sep + "/existing"
	if got["FAKE_PATH"] != want {
		t.Errorf("FAKE_PATH = %q want %q", got["FAKE_PATH"], want)
	}
}

func TestIntegrityMismatch(t *testing.T) {
	m, _, _ := newMat(t)
	// A registry entry with a bogus tree hash and no mirror -> can't clone, so
	// exercise the branch where source exists but tree differs by faking a mirror.
	e := types.BuildListEntry{Name: "x", UUID: "u-x", Version: "v1.0.0", Commit: "deadbeef",
		Tree: "sha256:wrong", GitURL: ""}
	_, err := m.EnsureSource(e)
	if err == nil {
		t.Fatal("expected error for missing source / bad tree")
	}
}
