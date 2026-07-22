// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package resolve

import (
	"fmt"
	"sort"
	"testing"

	"cosm/internal/errs"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// mapLoader is an in-memory SpecLoader keyed by "name@version".
type mapLoader map[string]types.Specs

func (m mapLoader) Load(name, uuid, version string) (types.Specs, error) {
	s, ok := m[name+"@"+version]
	if !ok {
		return types.Specs{}, fmt.Errorf("%w: %s@%s", errs.ErrPackageNotFound, name, version)
	}
	if uuid != "" && s.UUID != uuid {
		return types.Specs{}, fmt.Errorf("uuid mismatch for %s@%s", name, version)
	}
	return s, nil
}

// spec is a fixture helper: name/version + direct deps as (name,uuid,version) triples.
func spec(name, uuid, version string, deps ...[3]string) types.Specs {
	d := map[string]types.Dependency{}
	for _, t := range deps {
		major, _ := semver.Major(t[2])
		d[semver.UnitKey(t[1], major)] = types.Dependency{Name: t[0], Version: t[2]}
	}
	return types.Specs{
		Name: name, UUID: uuid, Version: version, Commit: "c-" + version,
		Tree: "sha256:" + name + version, Deps: d,
	}
}

func rootWith(deps ...[3]string) *types.Manifest {
	d := map[string]types.Dependency{}
	for _, t := range deps {
		major, _ := semver.Major(t[2])
		d[semver.UnitKey(t[1], major)] = types.Dependency{Name: t[0], Version: t[2]}
	}
	return &types.Manifest{Name: "root", UUID: "u-root", Version: "v1.0.0", Deps: d}
}

// selectedVersions flattens a build list to name->version for easy assertions.
func selectedVersions(bl types.BuildList) map[string]string {
	out := map[string]string{}
	for _, e := range bl.Dependencies {
		out[e.Name] = e.Version
	}
	return out
}

func assertSelected(t *testing.T, bl types.BuildList, want map[string]string) {
	t.Helper()
	got := selectedVersions(bl)
	if len(got) != len(want) {
		t.Errorf("build list size = %d want %d: %v", len(got), len(want), got)
	}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("selected %s = %q want %q", name, got[name], v)
		}
	}
}

// TestMVS_TestDeps: the root's test deps (and their regular closure) appear only
// under ResolveWithTests; a plain Resolve omits them.
func TestMVS_TestDeps(t *testing.T) {
	L := mapLoader{
		"app@v1.0.0":       spec("app", "u-app", "v1.0.0"),
		"harness@v1.0.0":   spec("harness", "u-harness", "v1.0.0", [3]string{"assertlib", "u-al", "v1.0.0"}),
		"assertlib@v1.0.0": spec("assertlib", "u-al", "v1.0.0"),
	}
	root := &types.Manifest{Name: "root", UUID: "u-root", Version: "v1.0.0",
		Deps: map[string]types.Dependency{
			semver.UnitKey("u-app", 1): {Name: "app", Version: "v1.0.0"},
		},
		TestDeps: map[string]types.Dependency{
			semver.UnitKey("u-harness", 1): {Name: "harness", Version: "v1.0.0"},
		},
	}

	bl, _, err := Resolve(root, L, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSelected(t, bl, map[string]string{"app": "v1.0.0"})

	blt, _, err := ResolveWithTests(root, L, nil)
	if err != nil {
		t.Fatal(err)
	}
	// harness pulls its regular dependency assertlib transitively; both are present.
	assertSelected(t, blt, map[string]string{"app": "v1.0.0", "harness": "v1.0.0", "assertlib": "v1.0.0"})
}

// TestMVS_TestDepsRaiseFloorOnlyUnderTests: a test dep can raise a shared unit's
// selected version, but only in the test resolution — the shipping build is
// unaffected.
func TestMVS_TestDepsRaiseFloorOnlyUnderTests(t *testing.T) {
	L := mapLoader{
		"lib@v1.0.0":     spec("lib", "u-lib", "v1.0.0", [3]string{"shared", "u-sh", "v1.0.0"}),
		"harness@v1.0.0": spec("harness", "u-harness", "v1.0.0", [3]string{"shared", "u-sh", "v1.5.0"}),
		"shared@v1.0.0":  spec("shared", "u-sh", "v1.0.0"),
		"shared@v1.5.0":  spec("shared", "u-sh", "v1.5.0"),
	}
	root := &types.Manifest{Name: "root", UUID: "u-root", Version: "v1.0.0",
		Deps: map[string]types.Dependency{
			semver.UnitKey("u-lib", 1): {Name: "lib", Version: "v1.0.0"},
		},
		TestDeps: map[string]types.Dependency{
			semver.UnitKey("u-harness", 1): {Name: "harness", Version: "v1.0.0"},
		},
	}

	bl, _, _ := Resolve(root, L, nil)
	assertSelected(t, bl, map[string]string{"lib": "v1.0.0", "shared": "v1.0.0"})

	blt, _, _ := ResolveWithTests(root, L, nil)
	assertSelected(t, blt, map[string]string{"lib": "v1.0.0", "harness": "v1.0.0", "shared": "v1.5.0"})
}

// TestMVS_AG reproduces the canonical A–G graph from the prototype's test.
// Expected build list for A (deps B v1.2.0, C v1.2.0): B1.2.0 C1.2.0 D1.4.0 E1.2.0.
func TestMVS_AG(t *testing.T) {
	L := mapLoader{
		"E@v1.1.0": spec("E", "u-E", "v1.1.0"),
		"E@v1.2.0": spec("E", "u-E", "v1.2.0"),
		"E@v1.3.0": spec("E", "u-E", "v1.3.0"),
		"G@v1.1.0": spec("G", "u-G", "v1.1.0"),
		"F@v1.1.0": spec("F", "u-F", "v1.1.0", [3]string{"G", "u-G", "v1.1.0"}),
		"D@v1.1.0": spec("D", "u-D", "v1.1.0", [3]string{"E", "u-E", "v1.1.0"}),
		"D@v1.2.0": spec("D", "u-D", "v1.2.0", [3]string{"E", "u-E", "v1.1.0"}),
		"D@v1.3.0": spec("D", "u-D", "v1.3.0", [3]string{"E", "u-E", "v1.2.0"}),
		"D@v1.4.0": spec("D", "u-D", "v1.4.0", [3]string{"E", "u-E", "v1.2.0"}),
		"B@v1.1.0": spec("B", "u-B", "v1.1.0", [3]string{"D", "u-D", "v1.1.0"}),
		"B@v1.2.0": spec("B", "u-B", "v1.2.0", [3]string{"D", "u-D", "v1.3.0"}),
		"C@v1.1.0": spec("C", "u-C", "v1.1.0"),
		"C@v1.2.0": spec("C", "u-C", "v1.2.0", [3]string{"D", "u-D", "v1.4.0"}),
		"C@v1.3.0": spec("C", "u-C", "v1.3.0", [3]string{"F", "u-F", "v1.1.0"}),
	}
	root := rootWith([3]string{"B", "u-B", "v1.2.0"}, [3]string{"C", "u-C", "v1.2.0"})
	bl, warns, err := Resolve(root, L, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
	assertSelected(t, bl, map[string]string{
		"B": "v1.2.0", "C": "v1.2.0", "D": "v1.4.0", "E": "v1.2.0",
	})
}

// TestMVS_LowerVersionFloor proves a non-selected lower version's requirement
// can still raise a transitive floor (the seen-set is over (unit,version)).
func TestMVS_LowerVersionFloor(t *testing.T) {
	L := mapLoader{
		"P@v1.0.0": spec("P", "u-P", "v1.0.0", [3]string{"R", "u-R", "v1.5.0"}),
		"P@v1.2.0": spec("P", "u-P", "v1.2.0", [3]string{"R", "u-R", "v1.2.0"}),
		"Q@v1.0.0": spec("Q", "u-Q", "v1.0.0", [3]string{"P", "u-P", "v1.2.0"}),
		"R@v1.2.0": spec("R", "u-R", "v1.2.0"),
		"R@v1.5.0": spec("R", "u-R", "v1.5.0"),
	}
	// Root requires P v1.0.0 (which needs R v1.5.0) and Q (which bumps P to v1.2.0,
	// whose R floor is only v1.2.0). Selected P=1.2.0 but R must stay 1.5.0.
	root := rootWith([3]string{"P", "u-P", "v1.0.0"}, [3]string{"Q", "u-Q", "v1.0.0"})
	bl, _, err := Resolve(root, L, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	assertSelected(t, bl, map[string]string{"P": "v1.2.0", "Q": "v1.0.0", "R": "v1.5.0"})
}

func TestMVS_V0Warning(t *testing.T) {
	L := mapLoader{
		"foo@v0.2.0": spec("foo", "u-foo", "v0.2.0"),
		"foo@v0.6.0": spec("foo", "u-foo", "v0.6.0"),
		"bar@v0.1.0": spec("bar", "u-bar", "v0.1.0", [3]string{"foo", "u-foo", "v0.6.0"}),
	}
	root := rootWith([3]string{"foo", "u-foo", "v0.2.0"}, [3]string{"bar", "u-bar", "v0.1.0"})
	bl, warns, err := Resolve(root, L, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	assertSelected(t, bl, map[string]string{"foo": "v0.6.0", "bar": "v0.1.0"})
	found := false
	for _, w := range warns {
		if w.Code == "W_V0_MINOR_BUMP" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected W_V0_MINOR_BUMP, got %v", warns)
	}
}

func TestMVS_NoV0WarningWhenEqual(t *testing.T) {
	L := mapLoader{"foo@v0.6.0": spec("foo", "u-foo", "v0.6.0")}
	root := rootWith([3]string{"foo", "u-foo", "v0.6.0"})
	_, warns, err := Resolve(root, L, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("unexpected warnings: %v", warns)
	}
}

func TestMVS_MissingVersion(t *testing.T) {
	L := mapLoader{} // empty: the required version does not exist
	root := rootWith([3]string{"foo", "u-foo", "v1.0.0"})
	if _, _, err := Resolve(root, L, nil); err == nil {
		t.Fatal("expected error for missing version")
	}
}

// devOverlay is a fake DevOverlay for a set of overridden units.
type devOverlay map[string]struct {
	man  types.Manifest
	path string
}

func (d devOverlay) Override(uuid string, major int) (types.Manifest, string, bool) {
	e, ok := d[semver.UnitKey(uuid, major)]
	return e.man, e.path, ok
}

func TestDevOverrideWins(t *testing.T) {
	// Loader deliberately lacks foo — the override must bypass the registry.
	L := mapLoader{}
	dev := devOverlay{
		semver.UnitKey("u-foo", 0): {
			man:  types.Manifest{Name: "foo", UUID: "u-foo", Version: "v0.9.9-dev", Build: "lua"},
			path: "/depot/dev/foo@v0",
		},
	}
	root := rootWith([3]string{"foo", "u-foo", "v0.2.0"})
	bl, _, err := Resolve(root, L, dev)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	e, ok := bl.Dependencies[semver.UnitKey("u-foo", 0)]
	if !ok {
		t.Fatal("foo not in build list")
	}
	if !e.Develop || e.Version != "v0.9.9-dev" || e.SourcePath != "/depot/dev/foo@v0" || e.Build != "lua" {
		t.Errorf("unexpected dev entry: %+v", e)
	}
}

func TestDevOverrideTransitive(t *testing.T) {
	// Dev foo depends on bar; bar resolves normally from the registry.
	L := mapLoader{"bar@v1.0.0": spec("bar", "u-bar", "v1.0.0")}
	dev := devOverlay{
		semver.UnitKey("u-foo", 0): {
			man: types.Manifest{Name: "foo", UUID: "u-foo", Version: "v0.9.9-dev",
				Deps: map[string]types.Dependency{
					semver.UnitKey("u-bar", 1): {Name: "bar", Version: "v1.0.0"},
				}},
			path: "/depot/dev/foo@v0",
		},
	}
	root := rootWith([3]string{"foo", "u-foo", "v0.1.0"})
	bl, _, err := Resolve(root, L, dev)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	assertSelected(t, bl, map[string]string{"foo": "v0.9.9-dev", "bar": "v1.0.0"})
	if !bl.Dependencies[semver.UnitKey("u-foo", 0)].Develop {
		t.Error("foo should be develop")
	}
}

func TestEmptyRoot(t *testing.T) {
	bl, warns, err := Resolve(&types.Manifest{Name: "solo", Version: "v1.0.0"}, mapLoader{}, nil)
	if err != nil || len(warns) != 0 || len(bl.Dependencies) != 0 {
		t.Fatalf("empty root should resolve to empty build list, got %v %v %v", bl, warns, err)
	}
}

// TestDeterministicAcrossOrder confirms resolution is independent of dep iteration
// order by running many times (Go randomizes map iteration).
func TestDeterministicAcrossOrder(t *testing.T) {
	L := mapLoader{
		"a@v1.0.0": spec("a", "u-a", "v1.0.0", [3]string{"c", "u-c", "v1.1.0"}),
		"b@v1.0.0": spec("b", "u-b", "v1.0.0", [3]string{"c", "u-c", "v1.3.0"}),
		"c@v1.1.0": spec("c", "u-c", "v1.1.0"),
		"c@v1.3.0": spec("c", "u-c", "v1.3.0"),
	}
	root := rootWith([3]string{"a", "u-a", "v1.0.0"}, [3]string{"b", "u-b", "v1.0.0"})
	var prev []string
	for i := 0; i < 25; i++ {
		bl, _, err := Resolve(root, L, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for k, e := range bl.Dependencies {
			got = append(got, k+"="+e.Version)
		}
		sort.Strings(got)
		if prev != nil {
			if len(got) != len(prev) {
				t.Fatalf("nondeterministic size")
			}
			for j := range got {
				if got[j] != prev[j] {
					t.Fatalf("nondeterministic result: %v vs %v", got, prev)
				}
			}
		}
		prev = got
	}
	// c must be the max floor (v1.3.0).
	if got := selectedVersions(func() types.BuildList { bl, _, _ := Resolve(root, L, nil); return bl }())["c"]; got != "v1.3.0" {
		t.Errorf("c = %q want v1.3.0", got)
	}
}
