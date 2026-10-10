// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/develop"
	"cosm/internal/errs"
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
	_, _, warns, _ := s.Resolve(proj)
	if !hasWarn(warns, "W_DEVELOP_MISSING") {
		t.Fatalf("expected W_DEVELOP_MISSING: %v", warns)
	}
	// The message must say which depot and path were checked: the usual cause is a
	// different depot than the one holding the checkout, and "missing" alone hid it.
	for _, w := range warns {
		if w.Code != "W_DEVELOP_MISSING" {
			continue
		}
		if !strings.Contains(w.Message, d.Root) || !strings.Contains(w.Message, d.DevUnit("p", 1)) {
			t.Errorf("message should name depot %s and path %s: %q", d.Root, d.DevUnit("p", 1), w.Message)
		}
	}

	// In the workspace, but its checkout directory is gone -> same detail.
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), &types.Workspace{
		SchemaVersion: 1,
		Entries:       []types.WorkspaceEntry{{Name: "p", UUID: "u-p", Major: 1, Path: "dev/p@v1"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _, warns, _ = s.Resolve(proj)
	found := false
	for _, w := range warns {
		if w.Code == "W_DEVELOP_MISSING" {
			found = true
			if !strings.Contains(w.Message, "no checkout at "+d.DevUnit("p", 1)) || !strings.Contains(w.Message, d.Root) {
				t.Errorf("in-workspace message should name the checkout path and depot: %q", w.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected W_DEVELOP_MISSING for a vanished checkout: %v", warns)
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

// TestResolveWithSources: --source redirects a dependency to a specific checkout
// (e.g. a git worktree) for this invocation, overriding the one checkout the
// shared workspace can hold — without touching the workspace or the enrollment.
func TestResolveWithSources(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "p", "u-p", mkSpec("p", "u-p", "v1.0.0"))
	s := New(d, gitx.Exec{})
	proj := t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-p", 1): {Name: "p", Version: "v1.0.0"},
	})

	// p is develop-enrolled at the shared workspace checkout.
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
	if err := manifest.SaveEnrollment(develop.EnrollmentPath(proj), &types.Enrollment{
		Enrolled: []string{semver.UnitKey("u-p", 1)},
	}); err != nil {
		t.Fatal(err)
	}

	// Baseline: the workspace checkout is what gets built.
	_, bl, _, err := s.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if got := bl.Dependencies[semver.UnitKey("u-p", 1)].SourcePath; got != pDev {
		t.Fatalf("baseline SourcePath = %q, want %q", got, pDev)
	}

	// A worktree of p elsewhere on disk.
	wt := t.TempDir()
	if err := manifest.SaveManifest(filepath.Join(wt, "cosm.json"),
		&types.Manifest{Name: "p", UUID: "u-p", Version: "v1.0.0", Build: "lua"}); err != nil {
		t.Fatal(err)
	}
	_, bl, _, err = s.ResolveWithTests(proj, WithSources([]string{wt}))
	if err != nil {
		t.Fatal(err)
	}
	e := bl.Dependencies[semver.UnitKey("u-p", 1)]
	if e.SourcePath != wt || !e.Develop {
		t.Fatalf("with --source: SourcePath = %q develop = %v, want %q true", e.SourcePath, e.Develop, wt)
	}

	// The override is per-invocation: it must not persist to the next resolve.
	_, bl, _, err = s.Resolve(proj)
	if err != nil {
		t.Fatal(err)
	}
	if got := bl.Dependencies[semver.UnitKey("u-p", 1)].SourcePath; got != pDev {
		t.Fatalf("after override, SourcePath = %q, want the workspace checkout %q", got, pDev)
	}
}

// TestResolveWithSourcesBadDir: a bogus --source is a usage error, never a silent
// fallback to a different checkout.
func TestResolveWithSourcesBadDir(t *testing.T) {
	d := seedDepot(t)
	s := New(d, gitx.Exec{})
	proj := t.TempDir()
	writeProject(t, proj, nil)
	if _, _, _, err := s.Resolve(proj, WithSources([]string{t.TempDir()})); err == nil {
		t.Fatal("expected an error for a --source dir with no cosm.json")
	}
}

// TestMissingUnpublishedDevelopDep: an unpublished, develop-only dependency has no
// registry release to fall back to, so a missing checkout — or running against the
// wrong depot — used to fail with a bare "not found in any registry", pointing away
// from the real cause. The error must say where cosm looked, and still wrap the
// original error (same exit code).
func TestMissingUnpublishedDevelopDep(t *testing.T) {
	d := seedDepot(t) // a depot that does NOT hold the sibling's checkout
	s := New(d, gitx.Exec{})
	proj := t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-sib", 0): {Name: "sib", Version: "v0.1.0"},
	})
	if err := manifest.SaveEnrollment(develop.EnrollmentPath(proj), &types.Enrollment{
		Enrolled: []string{semver.UnitKey("u-sib", 0)},
	}); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := s.Resolve(proj)
	if err == nil {
		t.Fatal("expected a resolution error")
	}
	if !errors.Is(err, errs.ErrPackageNotFound) {
		t.Errorf("original error must stay wrapped (exit code): %v", err)
	}
	for _, want := range []string{"sib is enrolled for development", d.Root, d.DevUnit("sib", 0), "COSM_DEPOT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%v", want, err)
		}
	}
}

// coStack builds an unpublished co-developed chain app -> direct -> transitive,
// with direct and transitive adopted into the workspace by path (never released).
func coStack(t *testing.T, s *Service) (proj, directDir, transDir string) {
	t.Helper()
	mk := func(name, uuid string, deps map[string]types.Dependency) string {
		dir := t.TempDir()
		if err := manifest.SaveManifest(filepath.Join(dir, "cosm.json"), &types.Manifest{
			Name: name, UUID: uuid, Version: "v0.1.0", Build: "lua", Deps: deps,
		}); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	transDir = mk("transitive", "u-trans", nil)
	directDir = mk("direct", "u-direct", map[string]types.Dependency{
		semver.UnitKey("u-trans", 0): {Name: "transitive", Version: "v0.1.0"},
	})
	proj = t.TempDir()
	writeProject(t, proj, map[string]types.Dependency{
		semver.UnitKey("u-direct", 0): {Name: "direct", Version: "v0.1.0"},
	})
	// Adopt both into the workspace from a scratch project, so `proj` itself is
	// enrolled for neither: that is the state `cosm develop --all` must fix.
	scratch := t.TempDir()
	writeProject(t, scratch, nil)
	for name, dir := range map[string]string{"direct": directDir, "transitive": transDir} {
		if _, err := s.Develop(scratch, name, -1, "", "", dir); err != nil {
			t.Fatal(err)
		}
	}
	return proj, directDir, transDir
}

// TestDevelopAllUnpublishedStack: `develop --all` resolved first, but a transitive
// unpublished sibling only resolves once enrolled — so it failed before learning it
// should enroll it. It now walks the graph through the workspace checkouts.
func TestDevelopAllUnpublishedStack(t *testing.T) {
	d := seedDepot(t)
	s := New(d, gitx.Exec{})
	proj, _, _ := coStack(t, s)

	names, err := s.DevelopAll(proj)
	if err != nil {
		t.Fatalf("develop --all on an unpublished stack: %v", err)
	}
	if strings.Join(names, ",") != "direct,transitive" {
		t.Fatalf("enrolled %v, want [direct transitive]", names)
	}
	_, bl, _, err := s.Resolve(proj)
	if err != nil {
		t.Fatalf("stack should resolve after --all: %v", err)
	}
	if !bl.Dependencies[semver.UnitKey("u-trans", 0)].Develop {
		t.Error("transitive should resolve from its develop checkout")
	}
	enr, _ := manifest.LoadEnrollment(develop.EnrollmentPath(proj))
	if enr.Names[semver.UnitKey("u-trans", 0)] != "transitive" {
		t.Errorf("enrollment should record names: %v", enr.Names)
	}
}

// TestResolveHintsUnenrolledSibling: when resolution fails on a package that sits in
// the workspace but this project isn't enrolled for, say so instead of a bare
// "not found in any registry".
func TestResolveHintsUnenrolledSibling(t *testing.T) {
	d := seedDepot(t)
	s := New(d, gitx.Exec{})
	proj, _, _ := coStack(t, s)
	if _, err := s.Develop(proj, "direct", -1, "", "", ""); err != nil { // direct only
		t.Fatal(err)
	}
	_, _, _, err := s.Resolve(proj)
	if err == nil {
		t.Fatal("expected failure: transitive isn't enrolled")
	}
	if !errors.Is(err, errs.ErrPackageNotFound) || errs.ExitCode(err) != 3 {
		t.Errorf("must still be a not-found error with exit code 3: %v", err)
	}
	for _, want := range []string{"transitive is in the develop workspace", "isn't enrolled", "cosm develop --all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should say %q:\n%v", want, err)
		}
	}
}

// TestMissingTransitiveNamed: a transitive enrolled unit is not in the project's
// cosm.json, so with its checkout gone (or the wrong depot) it showed as a raw
// <uuid>@v0. The name now comes from the enrollment or a registry clone.
func TestMissingTransitiveNamed(t *testing.T) {
	d := seedDepot(t)
	s := New(d, gitx.Exec{})
	proj, _, _ := coStack(t, s)
	if _, err := s.DevelopAll(proj); err != nil {
		t.Fatal(err)
	}
	// The wrong depot: same project, a depot holding none of the checkouts.
	other := New(seedDepot(t), gitx.Exec{})
	_, _, _, err := other.Resolve(proj)
	if err == nil {
		t.Fatal("expected failure in a depot without the checkouts")
	}
	if strings.Contains(err.Error(), "u-trans@v0 is enrolled") || !strings.Contains(err.Error(), "transitive is enrolled for development") {
		t.Errorf("transitive should be named, not shown as a key:\n%v", err)
	}

	// A legacy enrollment without names still finds the name in a registry clone.
	enr, _ := manifest.LoadEnrollment(develop.EnrollmentPath(proj))
	enr.Names = nil
	if err := manifest.SaveEnrollment(develop.EnrollmentPath(proj), enr); err != nil {
		t.Fatal(err)
	}
	seedPkg(t, other.D, "R", "transitive", "u-trans", mkSpec("transitive", "u-trans", "v0.0.1"))
	if _, _, _, err := other.Resolve(proj); err == nil || !strings.Contains(err.Error(), "transitive is enrolled for development") {
		t.Errorf("legacy enrollment: name should come from the registry clone:\n%v", err)
	}
}
