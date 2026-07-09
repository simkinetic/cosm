package manifest

import (
	"errors"
	"path/filepath"
	"testing"

	"cosm/internal/errs"
	"cosm/internal/types"
)

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cosm.json")
	in := &types.Manifest{
		Name: "linalg", UUID: "u-1", Version: "v0.1.0", Build: "cmake",
		Provides: []string{"linalg@v0"},
		Deps: map[string]types.Dependency{
			"u-2@v0": {Name: "terrastd", Version: "v0.6.4"},
		},
	}
	if err := SaveManifest(p, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if out.SchemaVersion != types.SchemaVersion {
		t.Errorf("schemaVersion not stamped: %d", out.SchemaVersion)
	}
	if out.Name != "linalg" || out.Build != "cmake" || out.Deps["u-2@v0"].Version != "v0.6.4" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestLoadManifestMissing(t *testing.T) {
	_, err := LoadManifest(filepath.Join(t.TempDir(), "cosm.json"))
	if !errors.Is(err, errs.ErrNoProject) {
		t.Fatalf("expected ErrNoProject, got %v", err)
	}
}

func TestRegistryAndSpecsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	rp := filepath.Join(dir, "registry.json")
	reg := &types.Registry{Name: "R", UUID: "ur", GitURL: "git@h:R.git", Kind: types.KindSource,
		Packages: map[string]types.PackageInfo{"p": {UUID: "up", GitURL: "git@h:p.git"}}}
	if err := SaveRegistry(rp, reg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRegistry(rp)
	if err != nil || got.Kind != types.KindSource || got.Packages["p"].UUID != "up" {
		t.Fatalf("registry round-trip: %+v %v", got, err)
	}

	sp := filepath.Join(dir, "specs.json")
	specs := &types.Specs{Name: "p", UUID: "up", Version: "v1.0.0", Commit: "abc", Tree: "sha256:x",
		Deps: map[string]types.Dependency{}}
	if err := SaveSpecs(sp, specs); err != nil {
		t.Fatal(err)
	}
	gs, err := LoadSpecs(sp)
	if err != nil || gs.Commit != "abc" || gs.SchemaVersion != types.SchemaVersion {
		t.Fatalf("specs round-trip: %+v %v", gs, err)
	}
}

func TestEnrollmentAndWorkspaceDefaults(t *testing.T) {
	dir := t.TempDir()
	e, err := LoadEnrollment(filepath.Join(dir, "develop.json"))
	if err != nil || len(e.Enrolled) != 0 {
		t.Fatalf("missing enrollment should be empty, got %+v %v", e, err)
	}
	w, err := LoadWorkspace(filepath.Join(dir, "workspace.json"))
	if err != nil || len(w.Entries) != 0 {
		t.Fatalf("missing workspace should be empty, got %+v %v", w, err)
	}
	e.Enrolled = append(e.Enrolled, "u@v0")
	if err := SaveEnrollment(filepath.Join(dir, "develop.json"), e); err != nil {
		t.Fatal(err)
	}
	e2, _ := LoadEnrollment(filepath.Join(dir, "develop.json"))
	if len(e2.Enrolled) != 1 || e2.Enrolled[0] != "u@v0" {
		t.Fatalf("enrollment round-trip: %+v", e2)
	}
}
