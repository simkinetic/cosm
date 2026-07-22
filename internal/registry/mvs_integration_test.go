// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package registry

import (
	"os"
	"path/filepath"
	"testing"

	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/resolve"
	"cosm/internal/semver"
	"cosm/internal/types"
)

type depRef struct{ name, uuid, version string }
type verSpec struct {
	v    string
	deps []depRef
}

// makeGraphPkg builds a package repo with one tagged commit per version (each
// cosm.json carrying that version's deps), pushed to a fresh bare remote.
func makeGraphPkg(t *testing.T, name, uuid string, vers []verSpec) string {
	t.Helper()
	g := gitx.Exec{}
	work := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(work, "src", name+"@v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(work, "src", name+"@v1", name+".lua"), []byte("return {}"), 0o644)
	mustRun(t, g, work, "init")
	first := true
	for _, ver := range vers {
		deps := map[string]types.Dependency{}
		for _, d := range ver.deps {
			mj, _ := semver.Major(d.version)
			deps[semver.UnitKey(d.uuid, mj)] = types.Dependency{Name: d.name, Version: d.version}
		}
		mj, _ := semver.Major(ver.v)
		m := &types.Manifest{Name: name, UUID: uuid, Version: ver.v, Build: "lua",
			Provides: []string{semver.UnitKey(name, mj)}, Deps: deps}
		if err := manifest.SaveManifest(filepath.Join(work, "cosm.json"), m); err != nil {
			t.Fatal(err)
		}
		mustRun(t, g, work, "add", ".")
		mustRun(t, g, work, "commit", "-m", ver.v)
		if first {
			mustRun(t, g, work, "branch", "-M", "main")
			first = false
		}
		mustRun(t, g, work, "tag", ver.v)
	}
	bare := initBare(t, name+".git")
	mustRun(t, g, work, "remote", "add", "origin", "file://"+bare)
	mustRun(t, g, work, "push", "origin", "main")
	mustRun(t, g, work, "push", "origin", "--tags")
	return "file://" + bare
}

// TestIntegration_MVS_AG exercises the canonical A–G graph through the real
// registry + git stack: multiple releases per package, transitive deps, and the
// lower-version-floor case (D@v1.3.0 is reachable via B@v1.2.0, but D@v1.4.0 is
// selected — both require E@v1.2.0). Expected: B1.2.0 C1.2.0 D1.4.0 E1.2.0.
func TestIntegration_MVS_AG(t *testing.T) {
	isolateGit(t)
	d := newDepot(t)
	svc := NewService(d, gitx.Exec{})
	if err := svc.InitRegistry("R", "file://"+initBare(t, "reg.git"), types.KindSource); err != nil {
		t.Fatal(err)
	}
	add := func(url string) {
		if _, _, err := svc.AddPackage("R", url); err != nil {
			t.Fatalf("registry add: %v", err)
		}
	}
	// Deliberately register dependents before some of their dependencies to prove
	// on-demand resolution is order-independent.
	add(makeGraphPkg(t, "E", "u-E", []verSpec{{"v1.1.0", nil}, {"v1.2.0", nil}, {"v1.3.0", nil}}))
	add(makeGraphPkg(t, "G", "u-G", []verSpec{{"v1.1.0", nil}}))
	add(makeGraphPkg(t, "F", "u-F", []verSpec{{"v1.1.0", []depRef{{"G", "u-G", "v1.1.0"}}}}))
	add(makeGraphPkg(t, "D", "u-D", []verSpec{
		{"v1.1.0", []depRef{{"E", "u-E", "v1.1.0"}}},
		{"v1.2.0", []depRef{{"E", "u-E", "v1.1.0"}}},
		{"v1.3.0", []depRef{{"E", "u-E", "v1.2.0"}}},
		{"v1.4.0", []depRef{{"E", "u-E", "v1.2.0"}}},
	}))
	add(makeGraphPkg(t, "B", "u-B", []verSpec{
		{"v1.1.0", []depRef{{"D", "u-D", "v1.1.0"}}},
		{"v1.2.0", []depRef{{"D", "u-D", "v1.3.0"}}},
	}))
	add(makeGraphPkg(t, "C", "u-C", []verSpec{
		{"v1.1.0", nil},
		{"v1.2.0", []depRef{{"D", "u-D", "v1.4.0"}}},
		{"v1.3.0", []depRef{{"F", "u-F", "v1.1.0"}}},
	}))

	root := &types.Manifest{Name: "A", UUID: "u-A", Version: "v1.0.0",
		Deps: map[string]types.Dependency{
			semver.UnitKey("u-B", 1): {Name: "B", Version: "v1.2.0"},
			semver.UnitKey("u-C", 1): {Name: "C", Version: "v1.2.0"},
		}}
	bl, warns, err := resolve.Resolve(root, NewLoader(d), nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	got := map[string]string{}
	for _, e := range bl.Dependencies {
		got[e.Name] = e.Version
	}
	want := map[string]string{"B": "v1.2.0", "C": "v1.2.0", "D": "v1.4.0", "E": "v1.2.0"}
	if len(got) != len(want) {
		t.Fatalf("build list = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q want %q", k, got[k], v)
		}
	}
	// F and G must NOT be present (only reachable via C@v1.3.0, which isn't selected).
	if _, ok := got["F"]; ok {
		t.Error("F should not be in the build list")
	}
}
