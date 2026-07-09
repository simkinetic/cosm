package registry

import (
	"path/filepath"
	"testing"

	"cosm/internal/depot"
	"cosm/internal/manifest"
	"cosm/internal/resolve"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// seedRegistry writes a registry (metadata + per-version specs + versions.json)
// into the depot and registers it in registries.json.
func seedRegistry(t *testing.T, d depot.Depot, regName string, pkgs map[string]string, specs ...types.Specs) {
	t.Helper()
	regDir := d.Registry(regName)
	packages := map[string]types.PackageInfo{}
	for name, uuid := range pkgs {
		packages[name] = types.PackageInfo{UUID: uuid, GitURL: "git@h:" + name + ".git"}
	}
	if err := manifest.SaveRegistry(MetaFile(regDir), &types.Registry{
		Name: regName, UUID: "ur-" + regName, GitURL: "git@h:" + regName + ".git",
		Kind: types.KindSource, Packages: packages,
	}); err != nil {
		t.Fatal(err)
	}
	byName := map[string][]string{}
	for _, s := range specs {
		s := s
		if err := manifest.SaveSpecs(SpecsFile(regDir, s.Name, s.Version), &s); err != nil {
			t.Fatal(err)
		}
		byName[s.Name] = append(byName[s.Name], s.Version)
	}
	for name, vs := range byName {
		if err := manifest.SaveVersions(VersionsFile(regDir, name), vs); err != nil {
			t.Fatal(err)
		}
	}
	// Append to registries.json.
	refs, _ := manifest.LoadRegistryRefs(d.RegistriesFile())
	refs = append(refs, types.RegistryRef{Name: regName, UUID: "ur-" + regName, GitURL: "git@h:" + regName + ".git"})
	if err := manifest.SaveRegistryRefs(d.RegistriesFile(), refs); err != nil {
		t.Fatal(err)
	}
}

func spec(name, uuid, version string, deps ...[3]string) types.Specs {
	d := map[string]types.Dependency{}
	for _, t := range deps {
		major, _ := semver.Major(t[2])
		d[semver.UnitKey(t[1], major)] = types.Dependency{Name: t[0], Version: t[2]}
	}
	return types.Specs{Name: name, UUID: uuid, Version: version, Commit: "c-" + version,
		Tree: "sha256:" + name + version, GitURL: "git@h:" + name + ".git", Deps: d}
}

func newDepot(t *testing.T) depot.Depot {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".cosm")
	d := depot.New(root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := d.Setup(); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLoaderLoadAndFind(t *testing.T) {
	d := newDepot(t)
	seedRegistry(t, d, "R", map[string]string{"p": "u-p", "q": "u-q"},
		spec("q", "u-q", "v1.0.0"),
		spec("q", "u-q", "v1.2.0"),
		spec("p", "u-p", "v1.0.0", [3]string{"q", "u-q", "v1.0.0"}),
	)
	l := NewLoader(d)

	sp, err := l.Load("p", "u-p", "v1.0.0")
	if err != nil || sp.Commit != "c-v1.0.0" {
		t.Fatalf("Load p: %+v %v", sp, err)
	}
	// UUID mismatch must fail.
	if _, err := l.Load("p", "wrong-uuid", "v1.0.0"); err == nil {
		t.Fatal("expected uuid mismatch failure")
	}
	// Find latest q.
	locs, err := l.Find("q", "")
	if err != nil || len(locs) != 1 || locs[0].Specs.Version != "v1.2.0" {
		t.Fatalf("Find q latest: %+v %v", locs, err)
	}
}

func TestFindPerMajor(t *testing.T) {
	d := newDepot(t)
	seedRegistry(t, d, "R", map[string]string{"p": "u-p"},
		spec("p", "u-p", "v0.6.0"),
		spec("p", "u-p", "v0.7.0"),
		spec("p", "u-p", "v1.0.0"),
		spec("p", "u-p", "v1.2.0"),
	)
	l := NewLoader(d)

	// No version -> latest of each major line, newest major first.
	locs, err := l.Find("p", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) != 2 {
		t.Fatalf("expected 2 candidates (one per major), got %d: %+v", len(locs), locs)
	}
	if locs[0].Specs.Version != "v1.2.0" || locs[1].Specs.Version != "v0.7.0" {
		t.Fatalf("per-major latest wrong/misordered: %s, %s", locs[0].Specs.Version, locs[1].Specs.Version)
	}

	// An explicit version still returns exactly that one.
	one, err := l.Find("p", "v0.6.0")
	if err != nil || len(one) != 1 || one[0].Specs.Version != "v0.6.0" {
		t.Fatalf("explicit version: %+v %v", one, err)
	}
}

// TestResolveFromDisk composes the real Loader with the pure resolver.
func TestResolveFromDisk(t *testing.T) {
	d := newDepot(t)
	seedRegistry(t, d, "R", map[string]string{"p": "u-p", "q": "u-q", "r": "u-r"},
		spec("r", "u-r", "v1.0.0"),
		spec("r", "u-r", "v1.5.0"),
		spec("q", "u-q", "v1.0.0", [3]string{"r", "u-r", "v1.5.0"}),
		spec("p", "u-p", "v1.0.0", [3]string{"r", "u-r", "v1.0.0"}, [3]string{"q", "u-q", "v1.0.0"}),
	)
	root := &types.Manifest{Name: "app", UUID: "u-app", Version: "v0.1.0",
		Deps: map[string]types.Dependency{
			semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.0.0"},
		}}
	bl, warns, err := resolve.Resolve(root, NewLoader(d), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings: %v", warns)
	}
	got := map[string]string{}
	for _, e := range bl.Dependencies {
		got[e.Name] = e.Version
	}
	want := map[string]string{"p": "v1.0.0", "q": "v1.0.0", "r": "v1.5.0"}
	if len(got) != len(want) {
		t.Fatalf("build list = %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q want %q", k, got[k], v)
		}
	}
	// Enrichment: commit/tree/giturl carried from specs.
	for _, e := range bl.Dependencies {
		if e.Commit == "" || e.Tree == "" || e.GitURL == "" {
			t.Errorf("entry %s missing enrichment: %+v", e.Name, e)
		}
	}
}
