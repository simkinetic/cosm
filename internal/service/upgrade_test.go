package service

import (
	"errors"
	"path/filepath"
	"testing"

	"cosm/internal/depot"
	"cosm/internal/errs"
	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/registry"
	"cosm/internal/semver"
	"cosm/internal/types"
)

func seedDepot(t *testing.T) depot.Depot {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".cosm")
	d := depot.New(root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := d.Setup(); err != nil {
		t.Fatal(err)
	}
	return d
}

func seedPkg(t *testing.T, d depot.Depot, regName, name, uuid string, specs ...types.Specs) {
	t.Helper()
	regDir := d.Registry(regName)
	reg, _ := manifest.LoadRegistry(registry.MetaFile(regDir))
	if reg == nil || reg.Name == "" {
		reg = &types.Registry{Name: regName, UUID: "ur", GitURL: "git@h:R.git", Kind: types.KindSource,
			Packages: map[string]types.PackageInfo{}}
	}
	reg.Packages[name] = types.PackageInfo{UUID: uuid, GitURL: "git@h:" + name + ".git"}
	if err := manifest.SaveRegistry(registry.MetaFile(regDir), reg); err != nil {
		t.Fatal(err)
	}
	var vs []string
	for _, s := range specs {
		s := s
		if err := manifest.SaveSpecs(registry.SpecsFile(regDir, name, s.Version), &s); err != nil {
			t.Fatal(err)
		}
		vs = append(vs, s.Version)
	}
	if err := manifest.SaveVersions(registry.VersionsFile(regDir, name), vs); err != nil {
		t.Fatal(err)
	}
	refs, _ := manifest.LoadRegistryRefs(d.RegistriesFile())
	for _, r := range refs {
		if r.Name == regName {
			return
		}
	}
	refs = append(refs, types.RegistryRef{Name: regName, UUID: "ur", GitURL: "git@h:R.git"})
	manifest.SaveRegistryRefs(d.RegistriesFile(), refs)
}

func mkSpec(name, uuid, version string, deps ...[3]string) types.Specs {
	dm := map[string]types.Dependency{}
	for _, dd := range deps {
		mj, _ := semver.Major(dd[2])
		dm[semver.UnitKey(dd[1], mj)] = types.Dependency{Name: dd[0], Version: dd[2]}
	}
	return types.Specs{Name: name, UUID: uuid, Version: version, Commit: "c" + version,
		Tree: "sha256:" + name + version, GitURL: "git@h:" + name + ".git", Build: "lua", Deps: dm}
}

func writeProject(t *testing.T, dir string, deps map[string]types.Dependency) {
	t.Helper()
	if err := manifest.SaveManifest(filepath.Join(dir, "cosm.json"), &types.Manifest{
		Name: "app", UUID: "u-app", Version: "v0.1.0", Build: "lua", Deps: deps,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeWithinMajor(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "p", "u-p",
		mkSpec("p", "u-p", "v1.0.0"),
		mkSpec("p", "u-p", "v1.1.0"),
		mkSpec("p", "u-p", "v1.2.0"),
		mkSpec("p", "u-p", "v2.0.0"),
	)
	proj := t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.0.0"},
	})
	s := New(d, gitx.Exec{})

	// Default upgrade stays within major 1 -> v1.2.0 (not v2.0.0).
	old, nw, err := s.Upgrade(proj, "p", "", false)
	if err != nil || old != "v1.0.0" || nw != "v1.2.0" {
		t.Fatalf("upgrade: %s->%s err=%v", old, nw, err)
	}
	// Constraint v1.1 -> latest v1.1.x (only v1.1.0 here).
	writeProject(t, proj, map[string]types.Dependency{semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.0.0"}})
	_, nw, err = s.Upgrade(proj, "p", "v1.1", false)
	if err != nil || nw != "v1.1.0" {
		t.Fatalf("constrained upgrade: %s err=%v", nw, err)
	}
}

func TestDowngradeConflict(t *testing.T) {
	d := seedDepot(t)
	// a@v1.0.0 requires p@v1.2.0; root also depends on p directly.
	seedPkg(t, d, "R", "p", "u-p",
		mkSpec("p", "u-p", "v1.0.0"),
		mkSpec("p", "u-p", "v1.2.0"),
	)
	seedPkg(t, d, "R", "a", "u-a",
		mkSpec("a", "u-a", "v1.0.0", [3]string{"p", "u-p", "v1.2.0"}),
	)
	proj := t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-a", 1): {Name: "a", Version: "v1.0.0"},
		semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.2.0"},
	})
	s := New(d, gitx.Exec{})

	// Downgrading p to v1.0.0 is unsatisfiable: a's only version needs p v1.2.0.
	_, err := s.Downgrade(proj, "p", "v1.0.0")
	if !errors.Is(err, errs.ErrResolutionConflict) {
		t.Fatalf("expected resolution conflict, got %v", err)
	}
	// Nothing should have been written.
	m, _ := manifest.LoadManifestFromDir(proj)
	if m.Deps[semver.UnitKey("u-p", 1)].Version != "v1.2.0" {
		t.Fatalf("downgrade should not have modified deps, got %s", m.Deps[semver.UnitKey("u-p", 1)].Version)
	}
}

func TestDowngradeCascade(t *testing.T) {
	d := seedDepot(t)
	// a@v1.1.0 needs p@v1.2.0; a@v1.0.0 needs p@v1.0.0.
	seedPkg(t, d, "R", "p", "u-p",
		mkSpec("p", "u-p", "v1.0.0"),
		mkSpec("p", "u-p", "v1.2.0"),
	)
	seedPkg(t, d, "R", "a", "u-a",
		mkSpec("a", "u-a", "v1.0.0", [3]string{"p", "u-p", "v1.0.0"}),
		mkSpec("a", "u-a", "v1.1.0", [3]string{"p", "u-p", "v1.2.0"}),
	)
	proj := t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-a", 1): {Name: "a", Version: "v1.1.0"},
		semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.2.0"},
	})
	s := New(d, gitx.Exec{})

	// Downgrading p cascades a down to v1.0.0 (its highest p-v1.0.0-compatible version).
	warns, err := s.Downgrade(proj, "p", "v1.0.0")
	if err != nil {
		t.Fatalf("downgrade should succeed via cascade, got %v", err)
	}
	if len(warns) == 0 {
		t.Error("expected a warning about the cascaded downgrade of 'a'")
	}
	m, _ := manifest.LoadManifestFromDir(proj)
	if m.Deps[semver.UnitKey("u-p", 1)].Version != "v1.0.0" {
		t.Errorf("p floor = %s want v1.0.0", m.Deps[semver.UnitKey("u-p", 1)].Version)
	}
	if m.Deps[semver.UnitKey("u-a", 1)].Version != "v1.0.0" {
		t.Errorf("a floor = %s want v1.0.0 (cascaded)", m.Deps[semver.UnitKey("u-a", 1)].Version)
	}
	// And it actually resolves p to v1.0.0 now.
	_, bl, _, _ := s.Resolve(proj)
	if bl.Dependencies[semver.UnitKey("u-p", 1)].Version != "v1.0.0" {
		t.Errorf("p resolved to %s want v1.0.0", bl.Dependencies[semver.UnitKey("u-p", 1)].Version)
	}
}

func TestDowngradeSuccess(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "p", "u-p",
		mkSpec("p", "u-p", "v1.0.0"),
		mkSpec("p", "u-p", "v1.2.0"),
	)
	proj := t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.2.0"},
	})
	s := New(d, gitx.Exec{})
	if _, err := s.Downgrade(proj, "p", "v1.0.0"); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	m, _ := manifest.LoadManifestFromDir(proj)
	if m.Deps[semver.UnitKey("u-p", 1)].Version != "v1.0.0" {
		t.Fatalf("downgrade not applied: %s", m.Deps[semver.UnitKey("u-p", 1)].Version)
	}
}

func TestAddMultiRegistry(t *testing.T) {
	d := seedDepot(t)
	// The same package (same UUID) is available in two registries — the
	// clearance-tier / mirror scenario. Add must surface both and let the caller pick.
	seedPkg(t, d, "R1", "p", "u-p", mkSpec("p", "u-p", "v1.0.0"))
	seedPkg(t, d, "R2", "p", "u-p", mkSpec("p", "u-p", "v1.0.0"))
	proj := t.TempDir()
	writeProject(t, proj, nil)
	s := New(d, gitx.Exec{})

	locs, err := s.Loader.Find("p", "v1.0.0")
	if err != nil || len(locs) != 2 {
		t.Fatalf("Find should return 2 locations, got %d (%v)", len(locs), err)
	}

	chosen := ""
	chooser := func(name, version string, ls []registry.Location) (registry.Location, error) {
		for _, l := range ls {
			if l.Registry == "R2" {
				chosen = l.Registry
				return l, nil
			}
		}
		return ls[0], nil
	}
	ver, reg, err := s.Add(proj, "p", "v1.0.0", AddOpts{Major: -1}, chooser)
	if err != nil || ver != "v1.0.0" || reg != "R2" || chosen != "R2" {
		t.Fatalf("multi-registry add: ver=%s reg=%s chosen=%s err=%v", ver, reg, chosen, err)
	}

	// --registry fast-path: a single candidate remains after filtering, so no
	// chooser is consulted (nil chooser must not be called).
	proj2 := t.TempDir()
	writeProject(t, proj2, nil)
	ver, reg, err = s.Add(proj2, "p", "v1.0.0", AddOpts{Registry: "R2", Major: -1}, nil)
	if err != nil || reg != "R2" {
		t.Fatalf("registry fast-path: reg=%s err=%v", reg, err)
	}

	// A registry that has no such package errors instead of prompting.
	proj3 := t.TempDir()
	writeProject(t, proj3, nil)
	if _, _, err := s.Add(proj3, "p", "v1.0.0", AddOpts{Registry: "nope", Major: -1}, nil); err == nil {
		t.Fatal("expected not-found for unknown registry filter")
	}
}

func TestAddMajorFastPath(t *testing.T) {
	d := seedDepot(t)
	// Two major lines of the same package coexist in one registry.
	seedPkg(t, d, "R", "p", "u-p",
		mkSpec("p", "u-p", "v0.7.0"),
		mkSpec("p", "u-p", "v1.2.0"),
	)
	s := New(d, gitx.Exec{})

	// No version + two majors is ambiguous; a nil chooser must not be called.
	proj := t.TempDir()
	writeProject(t, proj, nil)
	if _, _, err := s.Add(proj, "p", "", AddOpts{Major: -1}, nil); err == nil {
		t.Fatal("expected ambiguity error with two majors and no chooser")
	}

	// --major selects one line non-interactively.
	proj0 := t.TempDir()
	writeProject(t, proj0, nil)
	ver, _, err := s.Add(proj0, "p", "", AddOpts{Major: 0}, nil)
	if err != nil || ver != "v0.7.0" {
		t.Fatalf("major=0 fast-path: ver=%s err=%v", ver, err)
	}

	proj1 := t.TempDir()
	writeProject(t, proj1, nil)
	ver, _, err = s.Add(proj1, "p", "", AddOpts{Major: 1}, nil)
	if err != nil || ver != "v1.2.0" {
		t.Fatalf("major=1 fast-path: ver=%s err=%v", ver, err)
	}

	// A major line that doesn't exist errors.
	proj9 := t.TempDir()
	writeProject(t, proj9, nil)
	if _, _, err := s.Add(proj9, "p", "", AddOpts{Major: 9}, nil); err == nil {
		t.Fatal("expected not-found for absent major")
	}
}

func TestAddAndRm(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "p", "u-p", mkSpec("p", "u-p", "v1.0.0"))
	proj := t.TempDir()
	writeProject(t, proj, nil)
	s := New(d, gitx.Exec{})

	ver, reg, err := s.Add(proj, "p", "v1.0.0", AddOpts{Major: -1}, nil)
	if err != nil || ver != "v1.0.0" || reg != "R" {
		t.Fatalf("add: %s %s %v", ver, reg, err)
	}
	m, _ := manifest.LoadManifestFromDir(proj)
	if _, ok := m.Deps[semver.UnitKey("u-p", 1)]; !ok {
		t.Fatal("dep not added")
	}
	if err := s.Rm(proj, "p", nil); err != nil {
		t.Fatalf("rm: %v", err)
	}
	m, _ = manifest.LoadManifestFromDir(proj)
	if len(m.Deps) != 0 {
		t.Fatalf("dep not removed: %v", m.Deps)
	}
}
