// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"os"
	"path/filepath"
	"testing"

	"cosm/internal/develop"
	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/resolve"
	"cosm/internal/semver"
	"cosm/internal/types"
)

func hasWarn(warns []resolve.Warning, code string) bool {
	for _, w := range warns {
		if w.Code == code {
			return true
		}
	}
	return false
}

// TestDevelopAll enrolls this project in every workspace package it depends on.
func TestDevelopAll(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "p", "u-p", mkSpec("p", "u-p", "v1.0.0"))
	seedPkg(t, d, "R", "q", "u-q", mkSpec("q", "u-q", "v1.0.0"))
	s := New(d, gitx.Exec{})
	proj := t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.0.0"},
		semver.UnitKey("u-q", 1): {Name: "q", Version: "v1.0.0"},
	})

	// p is in the workspace (checkout present); q is not.
	pDev := d.DevUnit("p", 1)
	if err := os.MkdirAll(pDev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SaveManifest(filepath.Join(pDev, "cosm.json"),
		&types.Manifest{Name: "p", UUID: "u-p", Version: "v1.0.0", Build: "lua"}); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), &types.Workspace{
		SchemaVersion: 1,
		Entries:       []types.WorkspaceEntry{{Name: "p", UUID: "u-p", Major: 1, Path: "dev/p@v1"}},
	}); err != nil {
		t.Fatal(err)
	}

	names, err := s.DevelopAll(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "p" {
		t.Fatalf("DevelopAll enrolled %v, want [p]", names)
	}
	_, bl, _, _ := s.Resolve(proj)
	if !bl.Dependencies[semver.UnitKey("u-p", 1)].Develop {
		t.Error("p should resolve as develop after --all")
	}
	if bl.Dependencies[semver.UnitKey("u-q", 1)].Develop {
		t.Error("q must not be develop (not in the workspace)")
	}
}

// TestDevelopAdvisories: build/test/run/env/status all resolve through here, so the
// two silent develop states are surfaced uniformly — a dependency sitting in the
// workspace this project isn't developing, and an enrolled unit whose checkout is
// gone (both silently using the registry version).
func TestDevelopAdvisories(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "p", "u-p", mkSpec("p", "u-p", "v1.0.0"))
	s := New(d, gitx.Exec{})
	proj := t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.0.0"},
	})

	// Baseline: no workspace, no enrollment -> no develop advisories.
	if _, _, warns, err := s.Resolve(proj); err != nil {
		t.Fatal(err)
	} else if hasWarn(warns, "W_DEVELOP_AVAILABLE") || hasWarn(warns, "W_DEVELOP_MISSING") {
		t.Fatalf("unexpected develop advisory: %v", warns)
	}

	// p is in the workspace but this project isn't enrolled -> AVAILABLE.
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), &types.Workspace{
		SchemaVersion: 1,
		Entries:       []types.WorkspaceEntry{{Name: "p", UUID: "u-p", Major: 1, Path: "dev/p@v1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, warns, _ := s.Resolve(proj); !hasWarn(warns, "W_DEVELOP_AVAILABLE") {
		t.Fatalf("expected W_DEVELOP_AVAILABLE: %v", warns)
	}

	// Enrolled for p but no workspace entry (checkout missing) -> MISSING.
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), &types.Workspace{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SaveEnrollment(develop.EnrollmentPath(proj), &types.Enrollment{
		SchemaVersion: 1, Enrolled: []string{semver.UnitKey("u-p", 1)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, warns, _ := s.Resolve(proj); !hasWarn(warns, "W_DEVELOP_MISSING") {
		t.Fatalf("expected W_DEVELOP_MISSING: %v", warns)
	}
}

// TestDevelopFromPathBootstrap covers co-developing a brand-new, unpublished
// sibling: adopt it by path, declare it via the add workspace-fallback, and
// confirm resolution overrides it from the live local checkout.
func TestDevelopFromPathBootstrap(t *testing.T) {
	d := seedDepot(t)
	s := New(d, gitx.Exec{})

	// An unpublished package on disk (not in any registry).
	sib := t.TempDir()
	if err := manifest.SaveManifest(filepath.Join(sib, "cosm.json"), &types.Manifest{
		Name: "sib", UUID: "u-sib", Version: "v0.2.0", Build: "lua",
	}); err != nil {
		t.Fatal(err)
	}
	wantReal, _ := filepath.EvalSymlinks(sib)

	proj := t.TempDir()
	writeProject(t, proj, nil)

	// Adopt via --path: symlink under dev/, workspace entry (local), enrollment.
	devDir, err := s.Develop(proj, "sib", -1, "", "", sib)
	if err != nil {
		t.Fatalf("develop --path: %v", err)
	}
	if fi, lerr := os.Lstat(devDir); lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at %s: %v", devDir, lerr)
	}
	if got, _ := filepath.EvalSymlinks(devDir); got != wantReal {
		t.Fatalf("symlink -> %s, want %s", got, wantReal)
	}
	ws, _ := manifest.LoadWorkspace(d.WorkspaceFile())
	if len(ws.Entries) != 1 || !ws.Entries[0].Local || ws.Entries[0].UUID != "u-sib" {
		t.Fatalf("workspace entry: %+v", ws.Entries)
	}

	// Before declaring it, a plain add can't find it in a registry.
	if _, _, err := s.Add(proj, "nope", "", AddOpts{Major: -1}, nil); err == nil {
		t.Error("expected not-found for a package neither published nor developed")
	}

	// `cosm add sib` (no registry) falls back to the workspace.
	ver, reg, err := s.Add(proj, "sib", "", AddOpts{Major: -1}, nil)
	if err != nil {
		t.Fatalf("add fallback: %v", err)
	}
	if ver != "v0.2.0" || reg != "(develop)" {
		t.Fatalf("add fallback = %q %q, want v0.2.0 (develop)", ver, reg)
	}
	m, _ := manifest.LoadManifestFromDir(proj)
	if dep, ok := m.Deps[semver.UnitKey("u-sib", 0)]; !ok || dep.Version != "v0.2.0" {
		t.Fatalf("dep edge not written: %v", m.Deps)
	}

	// Resolution overrides sib from the live local checkout.
	_, bl, _, err := s.Resolve(proj)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	e, ok := bl.Dependencies[semver.UnitKey("u-sib", 0)]
	if !ok {
		t.Fatalf("sib absent from build list: %+v", bl.Dependencies)
	}
	if !e.Develop {
		t.Error("sib should be a develop entry")
	}
	if got, _ := filepath.EvalSymlinks(e.SourcePath); got != wantReal {
		t.Errorf("SourcePath = %s, want %s", e.SourcePath, wantReal)
	}

	// free --purge removes only the symlink, never the real checkout.
	if err := s.Free(proj, "sib", -1, true); err != nil {
		t.Fatalf("free --purge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sib, "cosm.json")); err != nil {
		t.Fatalf("purge deleted the real checkout: %v", err)
	}
}
