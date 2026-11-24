// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package develop

import (
	"os"
	"path/filepath"
	"testing"

	"cosm/internal/depot"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

func TestOverlayEnrolledPresent(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cosm-depot")
	d := depot.New(root)
	if err := os.MkdirAll(d.Dev(), 0o755); err != nil {
		t.Fatal(err)
	}
	// Workspace entry + checkout on disk.
	ws := &types.Workspace{Entries: []types.WorkspaceEntry{
		{Name: "foo", UUID: "u-foo", Major: 0, Ref: "main", RefKind: "branch", Path: "dev/foo@v0"},
	}}
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), ws); err != nil {
		t.Fatal(err)
	}
	devDir := d.DevUnit("foo", 0)
	if err := manifest.SaveManifest(filepath.Join(devDir, "cosm.json"), &types.Manifest{
		Name: "foo", UUID: "u-foo", Version: "v0.9.9-dev", Build: "lua",
	}); err != nil {
		t.Fatal(err)
	}
	// Project enrollment.
	proj := t.TempDir()
	if err := manifest.SaveEnrollment(EnrollmentPath(proj), &types.Enrollment{
		Enrolled: []string{semver.UnitKey("u-foo", 0)},
	}); err != nil {
		t.Fatal(err)
	}

	ov, missing, err := Build(d, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("unexpected missing: %v", missing)
	}
	man, path, ok := ov.Override("u-foo", 0)
	if !ok || man.Version != "v0.9.9-dev" || path != devDir {
		t.Fatalf("override = %+v %q %v", man, path, ok)
	}
	// Non-enrolled unit is not overridden.
	if _, _, ok := ov.Override("u-bar", 1); ok {
		t.Fatal("bar should not be overridden")
	}
}

func TestOverlayEnrolledMissingCheckout(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cosm-depot")
	d := depot.New(root)
	if err := os.MkdirAll(d.Dev(), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &types.Workspace{Entries: []types.WorkspaceEntry{
		{Name: "foo", UUID: "u-foo", Major: 0, Path: "dev/foo@v0"},
	}}
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), ws); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := manifest.SaveEnrollment(EnrollmentPath(proj), &types.Enrollment{
		Enrolled: []string{semver.UnitKey("u-foo", 0)},
	}); err != nil {
		t.Fatal(err)
	}
	ov, missing, err := Build(d, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0].Key != semver.UnitKey("u-foo", 0) || !missing[0].InWS {
		t.Fatalf("expected one in-workspace missing checkout, got %v", missing)
	}
	if _, _, ok := ov.Override("u-foo", 0); ok {
		t.Fatal("should not override when checkout missing")
	}
}

// TestAddSourceOverridesWorkspace: a per-invocation --source override wins over
// the single shared workspace checkout, so a git worktree of an enrolled unit can
// be built/tested without disturbing the workspace (or a parallel agent).
func TestAddSourceOverridesWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".cosm-depot")
	d := depot.New(root)
	if err := os.MkdirAll(d.Dev(), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &types.Workspace{Entries: []types.WorkspaceEntry{
		{Name: "foo", UUID: "u-foo", Major: 0, Ref: "main", RefKind: "branch", Path: "dev/foo@v0"},
	}}
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), ws); err != nil {
		t.Fatal(err)
	}
	devDir := d.DevUnit("foo", 0)
	if err := manifest.SaveManifest(filepath.Join(devDir, "cosm.json"), &types.Manifest{
		Name: "foo", UUID: "u-foo", Version: "v0.1.0", Build: "lua",
	}); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := manifest.SaveEnrollment(EnrollmentPath(proj), &types.Enrollment{
		Enrolled: []string{semver.UnitKey("u-foo", 0)},
	}); err != nil {
		t.Fatal(err)
	}
	ov, _, err := Build(d, proj)
	if err != nil {
		t.Fatal(err)
	}
	if _, path, _ := ov.Override("u-foo", 0); path != devDir {
		t.Fatalf("baseline path = %q, want the workspace checkout %q", path, devDir)
	}

	// A worktree of the same unit, elsewhere on disk.
	wt := realDir(t, t.TempDir()) // --source resolves symlinks (macOS: /var -> /private/var)
	if err := manifest.SaveManifest(filepath.Join(wt, "cosm.json"), &types.Manifest{
		Name: "foo", UUID: "u-foo", Version: "v0.2.0", Build: "lua",
	}); err != nil {
		t.Fatal(err)
	}
	name, err := ov.AddSource(wt)
	if err != nil {
		t.Fatal(err)
	}
	if name != "foo" {
		t.Errorf("name = %q, want foo", name)
	}
	man, path, ok := ov.Override("u-foo", 0)
	if !ok || path != wt || man.Version != "v0.2.0" {
		t.Fatalf("after AddSource: %+v %q %v; want the worktree %q", man, path, ok, wt)
	}
	// An unrelated unit is still untouched.
	if _, _, ok := ov.Override("u-bar", 0); ok {
		t.Fatal("bar should not be overridden")
	}
}

// TestAddSourceUnenrolled: --source also works for a unit that was never
// develop-enrolled (it becomes a develop override for this invocation).
func TestAddSourceUnenrolled(t *testing.T) {
	d := depot.New(filepath.Join(t.TempDir(), ".cosm-depot"))
	if err := os.MkdirAll(d.Dev(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), &types.Workspace{}); err != nil {
		t.Fatal(err)
	}
	ov, _, err := Build(d, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := realDir(t, t.TempDir()) // --source resolves symlinks (macOS: /var -> /private/var)
	if err := manifest.SaveManifest(filepath.Join(src, "cosm.json"), &types.Manifest{
		Name: "baz", UUID: "u-baz", Version: "v1.4.0", Build: "lua",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ov.AddSource(src); err != nil {
		t.Fatal(err)
	}
	if _, path, ok := ov.Override("u-baz", 1); !ok || path != src {
		t.Fatalf("override = %q %v, want %q", path, ok, src)
	}
}

// TestAddSourceBadDir: a --source path with no manifest is a clear error, not a
// silent no-op that would quietly build the wrong checkout.
func TestAddSourceBadDir(t *testing.T) {
	d := depot.New(filepath.Join(t.TempDir(), ".cosm-depot"))
	if err := os.MkdirAll(d.Dev(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SaveWorkspace(d.WorkspaceFile(), &types.Workspace{}); err != nil {
		t.Fatal(err)
	}
	ov, _, err := Build(d, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ov.AddSource(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory with no cosm.json")
	}
}

// realDir resolves symlinks, the way --source does.
func realDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestAddSourceResolvesSymlink: --source records the real directory, as the
// enrolled develop path does, so extensions and hashing never see the link.
func TestAddSourceResolvesSymlink(t *testing.T) {
	d := depot.New(filepath.Join(t.TempDir(), ".cosm-depot"))
	_ = os.MkdirAll(d.Dev(), 0o755)
	_ = manifest.SaveWorkspace(d.WorkspaceFile(), &types.Workspace{})
	ov, _, err := Build(d, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := realDir(t, t.TempDir())
	if err := manifest.SaveManifest(filepath.Join(src, "cosm.json"), &types.Manifest{Name: "p", UUID: "u-p", Version: "v0.1.0", Build: "lua"}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "p@v0")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ov.AddSource(link); err != nil {
		t.Fatal(err)
	}
	if _, path, _ := ov.Override("u-p", 0); path != src {
		t.Errorf("source path = %q, want the real directory %q", path, src)
	}
}
