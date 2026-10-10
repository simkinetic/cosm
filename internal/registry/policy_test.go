// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/types"
)

// pkgWithTags makes a package remote carrying several release tags (each with a
// matching cosm.json version).
func pkgWithTags(t *testing.T, name, uuid string, versions ...string) string {
	t.Helper()
	url := makePkg(t, name, uuid, versions[0], nil)
	g := gitx.Exec{}
	clone := filepath.Join(t.TempDir(), name+"-clone")
	mustRun(t, g, "", "clone", url, clone)
	for _, v := range versions[1:] {
		if err := manifest.SaveManifest(filepath.Join(clone, "cosm.json"), &types.Manifest{
			Name: name, UUID: uuid, Version: v, Build: "lua",
		}); err != nil {
			t.Fatal(err)
		}
		mustRun(t, g, clone, "commit", "-qam", v)
		mustRun(t, g, clone, "tag", v)
	}
	mustRun(t, g, clone, "push", "-q", "origin", "main", "--tags")
	return url
}

func newRegistry(t *testing.T) (*Service, string) {
	t.Helper()
	isolateGit(t)
	d := newDepot(t)
	svc := NewService(d, gitx.Exec{})
	if err := svc.InitRegistry("R", "file://"+initBare(t, "reg.git"), types.KindSource); err != nil {
		t.Fatal(err)
	}
	return svc, d.Registry("R")
}

func versionsOf(t *testing.T, regDir, name string) string {
	t.Helper()
	vs, _ := manifest.LoadVersions(VersionsFile(regDir, name))
	return strings.Join(vs, ",")
}

// TestRegisterFrom: --from sets a floor that this add and every later add or sync
// respect — a fresh registry doesn't fill up with a repository's stale old tags.
func TestRegisterFrom(t *testing.T) {
	svc, regDir := newRegistry(t)
	url := pkgWithTags(t, "p", "u-p", "v0.1.0", "v0.1.1", "v0.2.0")
	res, err := svc.AddPackageWith("R", url, AddOptions{From: "v0.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Added, ",") != "v0.2.0" || len(res.Skipped) != 2 {
		t.Fatalf("added %v skipped %v", res.Added, res.Skipped)
	}
	if _, _, err := svc.SyncRegistry("R"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AddPackage("R", url); err != nil {
		t.Fatal(err)
	}
	if got := versionsOf(t, regDir, "p"); got != "v0.2.0" {
		t.Fatalf("after sync and re-add: %s, want only v0.2.0", got)
	}
	// Naming a tag below the floor is an error that says how to lower it.
	if _, err := svc.AddPackageWith("R", url, AddOptions{Tags: []string{"v0.1.1"}}); err == nil || !strings.Contains(err.Error(), "--from v0.1.1") {
		t.Errorf("tag below floor: %v", err)
	}
}

// TestRegisterTags: --tag registers just the named tags, this once.
func TestRegisterTags(t *testing.T) {
	svc, regDir := newRegistry(t)
	url := pkgWithTags(t, "p", "u-p", "v1.0.0", "v1.1.0", "v1.2.0")
	if _, err := svc.AddPackageWith("R", url, AddOptions{Tags: []string{"v1.1.0"}}); err != nil {
		t.Fatal(err)
	}
	if got := versionsOf(t, regDir, "p"); got != "v1.1.0" {
		t.Fatalf("registered %s, want v1.1.0", got)
	}
	if _, err := svc.AddPackageWith("R", url, AddOptions{Tags: []string{"v9.9.9"}}); err == nil {
		t.Error("a tag the repository doesn't have must be an error")
	}
}

// TestRemovedVersionStaysRemoved: rm used to be undone by the next sync or add,
// because the tag is still in the package's repository.
func TestRemovedVersionStaysRemoved(t *testing.T) {
	svc, regDir := newRegistry(t)
	url := pkgWithTags(t, "p", "u-p", "v1.0.0", "v1.1.0")
	if _, _, err := svc.AddPackage("R", url); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveVersion("R", "p", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.SyncRegistry("R"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AddPackage("R", url); err != nil {
		t.Fatal(err)
	}
	if got := versionsOf(t, regDir, "p"); got != "v1.1.0" {
		t.Fatalf("removed version came back: %s", got)
	}
	// Explicitly asking for it registers it again and lifts the exclusion.
	if _, err := svc.AddPackageWith("R", url, AddOptions{Tags: []string{"v1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	if pol, _ := manifest.LoadPolicy(PolicyFile(regDir, "p")); len(pol.Excluded) != 0 {
		t.Errorf("exclusion not lifted: %v", pol.Excluded)
	}
}

// TestYank: a yanked version stays registered and loadable (reproducible builds)
// but is never picked as latest or offered for selection.
func TestYank(t *testing.T) {
	svc, regDir := newRegistry(t)
	url := pkgWithTags(t, "p", "u-p", "v1.0.0", "v1.1.0")
	if _, _, err := svc.AddPackage("R", url); err != nil {
		t.Fatal(err)
	}
	if err := svc.Yank("R", "p", "v1.1.0", "broken ABI"); err != nil {
		t.Fatal(err)
	}
	l := NewLoader(svc.d)
	if reason, y := l.Yanked("p", "u-p", "v1.1.0"); !y || reason != "broken ABI" {
		t.Errorf("Yanked = %q %v", reason, y)
	}
	if _, err := l.Load("p", "u-p", "v1.1.0"); err != nil {
		t.Errorf("a yanked version must still load: %v", err)
	}
	if locs, _ := l.Find("p", ""); len(locs) != 1 || locs[0].Specs.Version != "v1.0.0" {
		t.Errorf("latest should skip the yanked version: %+v", locs)
	}
	if _, _, vs, _ := l.SelectableVersions("p"); strings.Join(vs, ",") != "v1.0.0" {
		t.Errorf("selectable = %v", vs)
	}
	if err := svc.Yank("R", "p", "v3.0.0", ""); err == nil {
		t.Error("yanking an unregistered version must fail")
	}
	if err := svc.Unyank("R", "p", "v1.1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(PolicyFile(regDir, "p")); !os.IsNotExist(err) {
		t.Error("an empty policy should leave no policy.json behind")
	}
}
