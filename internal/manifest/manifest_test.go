// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/errs"
	"cosm/internal/types"
)

// TestLoadRegistryRefsIncompatible: an index in an older cosm's shape (array of
// strings) yields an actionable usage error, not a raw JSON type mismatch.
func TestLoadRegistryRefsIncompatible(t *testing.T) {
	p := filepath.Join(t.TempDir(), "registries.json")
	if err := os.WriteFile(p, []byte(`["cosmlua","cosmcpp"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRegistryRefs(p)
	if err == nil {
		t.Fatal("expected an error for an incompatible index")
	}
	if !errors.Is(err, errs.ErrUsage) {
		t.Errorf("want ErrUsage, got %v", err)
	}
	if strings.Contains(err.Error(), "unmarshal") || !strings.Contains(err.Error(), "older/incompatible cosm") {
		t.Errorf("error should be actionable, got: %v", err)
	}

	// A valid index still round-trips, and a missing file is empty (no error).
	if err := SaveRegistryRefs(p, []types.RegistryRef{{Name: "R", UUID: "u", GitURL: "g"}}); err != nil {
		t.Fatal(err)
	}
	if refs, err := LoadRegistryRefs(p); err != nil || len(refs) != 1 || refs[0].Name != "R" {
		t.Fatalf("valid index: %v %v", refs, err)
	}
	if refs, err := LoadRegistryRefs(filepath.Join(t.TempDir(), "none.json")); err != nil || refs != nil {
		t.Fatalf("missing index should be empty: %v %v", refs, err)
	}
}

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
