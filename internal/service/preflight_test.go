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
	"cosm/internal/semver"
	"cosm/internal/types"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := (gitx.Exec{}).Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}

// releaseRepo makes a committed package "lib" with the given deps; with origin, it
// is pushed to a fresh bare remote. Returns the checkout and the bare remote.
func releaseRepo(t *testing.T, withOrigin bool, deps map[string]types.Dependency) (string, string) {
	t.Helper()
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	t.Setenv("COSM_OFFLINE", "1") // seeded registries are plain files: never pull
	dir := t.TempDir()
	if err := manifest.SaveManifest(filepath.Join(dir, "cosm.json"), &types.Manifest{
		Name: "lib", UUID: "u-lib", Version: "v0.1.0", Build: "lua", License: "MIT", Deps: deps,
	}); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	bare := ""
	if withOrigin {
		bare = t.TempDir()
		gitT(t, bare, "init", "-q", "--bare")
		gitT(t, dir, "remote", "add", "origin", bare)
		gitT(t, dir, "push", "-q", "-u", "origin", "main")
	}
	return dir, bare
}

func macroDep(version string) map[string]types.Dependency {
	return map[string]types.Dependency{semver.UnitKey("u-macro", 0): {Name: "macro", Version: version}}
}

// TestPlanReleaseCollectsEveryProblem: one run reports every problem — here a dirty
// tree and a dependency whose declared minimum is no longer registered (the field
// case: a package still requiring a removed v0.1.0) — and the real release then
// refuses, listing both, with nothing changed.
func TestPlanReleaseCollectsEveryProblem(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "macro", "u-macro", mkSpec("macro", "u-macro", "v0.2.0"))
	s := New(d, gitx.Exec{})
	dir, bare := releaseRepo(t, true, macroDep("v0.1.0"))
	_ = os.WriteFile(filepath.Join(dir, "wip.lua"), []byte("x"), 0o644)

	p, err := s.PlanRelease(dir, ReleaseOpts{Patch: true})
	if err != nil {
		t.Fatal(err)
	}
	probs := p.Problems()
	if len(probs) != 2 {
		t.Fatalf("want 2 problems (dirty tree, missing dep), got %d: %v", len(probs), probs)
	}
	if !errors.Is(probs[0], errs.ErrDirtyWorktree) || !strings.Contains(probs[0].Error(), "wip.lua") {
		t.Errorf("first problem should name the dirty file: %v", probs[0])
	}
	dep := probs[1].Error()
	if !errors.Is(probs[1], errs.ErrPackageNotFound) || !strings.Contains(dep, "Published: v0.2.0") ||
		!strings.Contains(dep, "cosm upgrade macro") || strings.Count(dep, "registry") > 1 {
		t.Errorf("dep problem should name the published versions and the fix, once: %v", probs[1])
	}

	head := gitT(t, dir, "rev-parse", "HEAD")
	_, err = s.Release(dir, ReleaseOpts{Patch: true})
	var pe *PreflightError
	if !errors.As(err, &pe) || len(pe.Problems) != 2 {
		t.Fatalf("release should refuse with both problems: %v", err)
	}
	if !errors.Is(err, errs.ErrDirtyWorktree) || !errors.Is(err, errs.ErrPackageNotFound) {
		t.Error("the combined error must still match each kind")
	}
	if gitT(t, dir, "rev-parse", "HEAD") != head || gitT(t, dir, "tag") != "" || gitT(t, bare, "tag") != "" {
		t.Error("a refused release must change nothing")
	}
}

// TestReleaseRefusesWithoutOrigin: pushing is the last step, so with no origin the
// old release committed and tagged locally and then failed, leaving a stray tag
// that blocked the retry. The preflight now refuses first.
func TestReleaseRefusesWithoutOrigin(t *testing.T) {
	s := New(seedDepot(t), gitx.Exec{})
	dir, _ := releaseRepo(t, false, nil)
	head := gitT(t, dir, "rev-parse", "HEAD")
	_, err := s.Release(dir, ReleaseOpts{Version: "v0.2.0"})
	if !errors.Is(err, errs.ErrUsage) || !strings.Contains(err.Error(), "git remote add origin") {
		t.Fatalf("want a clear no-origin refusal, got %v", err)
	}
	if gitT(t, dir, "rev-parse", "HEAD") != head || gitT(t, dir, "tag") != "" {
		t.Error("no commit or tag may be left behind")
	}
	if m, _ := manifest.LoadManifestFromDir(dir); m.Version != "v0.1.0" {
		t.Errorf("cosm.json must be untouched, version = %s", m.Version)
	}
}

// TestPlanReleaseTagOnOrigin: a tag that exists only on origin (pushed from
// another clone) is caught before the release is attempted.
func TestPlanReleaseTagOnOrigin(t *testing.T) {
	s := New(seedDepot(t), gitx.Exec{})
	dir, _ := releaseRepo(t, true, nil)
	gitT(t, dir, "tag", "v0.1.1")
	gitT(t, dir, "push", "-q", "origin", "v0.1.1")
	gitT(t, dir, "tag", "-d", "v0.1.1")
	p, err := s.PlanRelease(dir, ReleaseOpts{Patch: true})
	if err != nil {
		t.Fatal(err)
	}
	probs := p.Problems()
	if len(probs) != 1 || !errors.Is(probs[0], errs.ErrVersionExists) || !strings.Contains(probs[0].Error(), "on origin") {
		t.Fatalf("want one tag-exists-on-origin problem, got %v", probs)
	}
}

// TestPlanReleaseClosureFromRegistries: the declared dependency is registered, but
// its own requirement is not — consumers would fail one level down.
func TestPlanReleaseClosureFromRegistries(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "macro", "u-macro", mkSpec("macro", "u-macro", "v0.2.0", [3]string{"base", "u-base", "v0.1.0"}))
	s := New(d, gitx.Exec{})
	dir, _ := releaseRepo(t, true, macroDep("v0.2.0"))
	p, err := s.PlanRelease(dir, ReleaseOpts{Patch: true})
	if err != nil {
		t.Fatal(err)
	}
	probs := p.Problems()
	if len(probs) != 1 || !errors.Is(probs[0], errs.ErrPackageNotFound) || !strings.Contains(probs[0].Error(), "registries only") {
		t.Fatalf("want one closure problem, got %v", probs)
	}
}

// TestReleaseHappyPath: everything passes, the plan describes the release and the
// registry entry, and the release then commits, tags, and pushes.
func TestReleaseHappyPath(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "macro", "u-macro", mkSpec("macro", "u-macro", "v0.2.0"))
	seedPkg(t, d, "R", "lib", "u-lib", mkSpec("lib", "u-lib", "v0.1.0"))
	s := New(d, gitx.Exec{})
	dir, bare := releaseRepo(t, true, macroDep("v0.2.0"))
	p, err := s.PlanRelease(dir, ReleaseOpts{Minor: true})
	if err != nil {
		t.Fatal(err)
	}
	if probs := p.Problems(); len(probs) != 0 {
		t.Fatalf("unexpected problems: %v", probs)
	}
	if p.To != "v0.2.0" || p.Released.Version != "v0.2.0" || p.Released.License != "MIT" || p.Branch != "main" || p.Remote != bare {
		t.Errorf("plan = %+v", p)
	}
	if strings.Join(p.Registries, ",") != "R" {
		t.Errorf("registries listing lib = %v, want [R]", p.Registries)
	}
	if ver, err := s.Release(dir, ReleaseOpts{Minor: true}); err != nil || ver != "v0.2.0" {
		t.Fatalf("release: %q %v", ver, err)
	}
	if gitT(t, bare, "tag") != "v0.2.0" {
		t.Error("tag not pushed")
	}
}

// TestPlanReleaseWarnsDevelopedDep: building against a develop checkout passes the
// preflight (the declared floor is published) but is flagged, since consumers get
// the registry version instead.
func TestPlanReleaseWarnsDevelopedDep(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "macro", "u-macro", mkSpec("macro", "u-macro", "v0.2.0"))
	s := New(d, gitx.Exec{})
	dir, _ := releaseRepo(t, true, macroDep("v0.2.0"))
	dev := d.DevUnit("macro", 0)
	if err := manifest.SaveManifest(filepath.Join(dev, "cosm.json"), &types.Manifest{Name: "macro", UUID: "u-macro", Version: "v0.3.0", Build: "lua"}); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), &types.Workspace{SchemaVersion: 1,
		Entries: []types.WorkspaceEntry{{Name: "macro", UUID: "u-macro", Major: 0, Path: "dev/macro@v0"}}}); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SaveEnrollment(develop.EnrollmentPath(dir), &types.Enrollment{Enrolled: []string{semver.UnitKey("u-macro", 0)}}); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "-A") // .cosm/ is ignored by its own .gitignore; keep the tree clean
	p, err := s.PlanRelease(dir, ReleaseOpts{Patch: true})
	if err != nil {
		t.Fatal(err)
	}
	if probs := p.Problems(); len(probs) != 0 {
		t.Fatalf("a developed dep with a published floor isn't a problem: %v", probs)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "macro is enrolled for development") || !strings.Contains(p.Warnings[0], "v0.2.0") {
		t.Errorf("warnings = %v", p.Warnings)
	}
}
