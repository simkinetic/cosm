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
