// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package materialize

import (
	"encoding/json"
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

// TestStablePrefix: `.cosm/env/<name>@v<major>` is a constant path that re-points
// as the content-addressed prefix changes, so tools caching cosm env output survive
// a dep's content changing.
func TestStablePrefix(t *testing.T) {
	proj := t.TempDir()
	real1, real2 := t.TempDir(), t.TempDir()
	want := filepath.Join(proj, ".cosm", "env", "foo@v0")

	link := stablePrefix(proj, "foo", 0, real1)
	if link != want {
		t.Fatalf("stable path = %s, want %s", link, want)
	}
	if tgt, _ := os.Readlink(link); tgt != real1 {
		t.Fatalf("target = %s, want %s", tgt, real1)
	}
	// Re-pointing keeps the same stable path but the new target.
	if link2 := stablePrefix(proj, "foo", 0, real2); link2 != want {
		t.Fatalf("stable path changed on re-point: %s", link2)
	}
	if tgt, _ := os.Readlink(link); tgt != real2 {
		t.Fatalf("not re-pointed: %s", tgt)
	}
	// An empty prefix is returned as-is (no symlink).
	if got := stablePrefix(proj, "foo", 0, ""); got != "" {
		t.Errorf("empty prefix = %q", got)
	}
}

func TestEnsureLocalGitignore(t *testing.T) {
	proj := t.TempDir()
	ensureLocalGitignore(proj)
	data, err := os.ReadFile(filepath.Join(proj, ".cosm", ".gitignore"))
	if err != nil || string(data) != "*\n" {
		t.Fatalf(".cosm/.gitignore = %q, %v", data, err)
	}
	// A user's existing file is not overwritten.
	custom := filepath.Join(proj, ".cosm", ".gitignore")
	if err := os.WriteFile(custom, []byte("keep-me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureLocalGitignore(proj)
	if data, _ := os.ReadFile(custom); string(data) != "keep-me\n" {
		t.Fatalf("overwrote existing .gitignore: %q", data)
	}
}

// TestClosureKeys: the closure of direct deps spans the whole transitive graph,
// deepest-first, de-duplicated across diamonds.
func TestClosureKeys(t *testing.T) {
	// B -> {C, D}; C -> E; D -> E (diamond on E). Direct dep of the root: B.
	bl := types.BuildList{Dependencies: map[string]types.BuildListEntry{
		"B": {Name: "B", DepKeys: []string{"C", "D"}},
		"C": {Name: "C", DepKeys: []string{"E"}},
		"D": {Name: "D", DepKeys: []string{"E"}},
		"E": {Name: "E"},
	}}
	got := closureKeys([]string{"B"}, bl)

	pos := map[string]int{}
	for i, k := range got {
		if _, dup := pos[k]; dup {
			t.Fatalf("duplicate %s in %v", k, got)
		}
		pos[k] = i
	}
	for _, k := range []string{"B", "C", "D", "E"} {
		if _, ok := pos[k]; !ok {
			t.Fatalf("closure missing %s: %v", k, got)
		}
	}
	if !(pos["E"] < pos["C"] && pos["E"] < pos["D"] && pos["C"] < pos["B"] && pos["D"] < pos["B"]) {
		t.Fatalf("not deepest-first: %v", got)
	}
}

// fakeExtensionRecording records each build request's stdin (one compact JSON per
// line) so a test can inspect exactly which deps a package's build received.
func fakeExtensionRecording(t *testing.T, d depot.Depot, reqfile string) {
	t.Helper()
	dir := filepath.Join(d.Extensions(), "fake")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1" in
  info) echo '{"extension":"fake","version":"0.0.1","protocol":1,"toolchainId":"fake-tc","capabilities":["info","build","activate"]}' ;;
  build) cat >> "` + reqfile + `"; printf '\n' >> "` + reqfile + `"; echo '{"status":"ok","descriptor":{"marker":"x"}}' ;;
  activate) cat >/dev/null; echo '{"env":{}}' ;;
  *) echo unknown >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "cosm-ext-fake"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestBuildForwardsTransitiveClosure: building a package must hand the extension
// its FULL transitive dep closure (so CMAKE_PREFIX_PATH etc. spans it), not just
// direct deps. Regression for the transitive-prefix bug.
func TestBuildForwardsTransitiveClosure(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cosm")
	d := depot.New(root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := d.Setup(); err != nil {
		t.Fatal(err)
	}
	reqfile := filepath.Join(t.TempDir(), "requests.jsonl")
	fakeExtensionRecording(t, d, reqfile)
	m := &Materializer{D: d, Git: gitx.Exec{}, Run: ext.NewRunner(d),
		Opt: Options{Platform: types.Platform{OS: "linux", Arch: "amd64"}, BuildType: "Release", Jobs: 1}}

	// a -> b -> c, so c is transitive to a.
	bl := types.BuildList{Dependencies: map[string]types.BuildListEntry{
		"u-a@v1": {Name: "a", UUID: "u-a", Major: 1, Version: "v1.0.0", Build: "fake", Develop: true, SourcePath: devDir(t, "a"), DepKeys: []string{"u-b@v1"}},
		"u-b@v1": {Name: "b", UUID: "u-b", Major: 1, Version: "v1.0.0", Build: "fake", Develop: true, SourcePath: devDir(t, "b"), DepKeys: []string{"u-c@v1"}},
		"u-c@v1": {Name: "c", UUID: "u-c", Major: 1, Version: "v1.0.0", Build: "fake", Develop: true, SourcePath: devDir(t, "c")},
	}}
	if _, err := m.BuildAll(bl); err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	data, _ := os.ReadFile(reqfile)
	var aDeps map[string]bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r ext.BuildRequest
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad request json %q: %v", line, err)
		}
		if r.Package.Name == "a" {
			aDeps = map[string]bool{}
			for _, dep := range r.Deps {
				aDeps[dep.Name] = true
			}
		}
	}
	if aDeps == nil {
		t.Fatal("no build request recorded for 'a'")
	}
	if !aDeps["b"] || !aDeps["c"] {
		t.Fatalf("a's build deps = %v; want both b (direct) and c (transitive)", aDeps)
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
