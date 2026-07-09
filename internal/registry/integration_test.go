package registry

import (
	"os"
	"path/filepath"
	"testing"

	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/resolve"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// These tests drive real git through file:// remotes — hermetic, no network.

func isolateGit(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	cfg := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname=t\n\temail=t@t\n[init]\n\tdefaultBranch=main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
}

func initBare(t *testing.T, name string) string {
	t.Helper()
	g := gitx.Exec{}
	p := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(p, "init", "--bare"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(p, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	return p
}

// makePkg creates a working package repo with cosm.json + a tag, pushed to a
// fresh bare remote. Returns the file:// URL of the remote.
func makePkg(t *testing.T, name, uuid, version string, deps map[string]types.Dependency) string {
	t.Helper()
	g := gitx.Exec{}
	work := filepath.Join(t.TempDir(), name)
	major, _ := semver.Major(version)
	if err := os.MkdirAll(filepath.Join(work, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &types.Manifest{Name: name, UUID: uuid, Version: version, Build: "lua",
		Provides: []string{semver.UnitKey(name, major)}, Deps: deps}
	if err := manifest.SaveManifest(filepath.Join(work, "cosm.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "src", name+".lua"), []byte("return {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, g, work, "init")
	mustRun(t, g, work, "add", ".")
	mustRun(t, g, work, "commit", "-m", "init")
	mustRun(t, g, work, "branch", "-M", "main")
	mustRun(t, g, work, "tag", version)
	bare := initBare(t, name+".git")
	mustRun(t, g, work, "remote", "add", "origin", "file://"+bare)
	mustRun(t, g, work, "push", "origin", "main")
	mustRun(t, g, work, "push", "origin", "--tags")
	return "file://" + bare
}

// makePkgMasterHead is makePkg but with the bare remote's HEAD left at 'master'
// while the package is pushed on 'main' — the real-world state produced by a plain
// `git init --bare` (default HEAD) plus `git branch -M main`. A fresh clone of such
// a remote checks out no working tree, which AddPackage must recover from.
func makePkgMasterHead(t *testing.T, name, uuid, version string) string {
	t.Helper()
	g := gitx.Exec{}
	work := filepath.Join(t.TempDir(), name)
	major, _ := semver.Major(version)
	if err := os.MkdirAll(filepath.Join(work, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &types.Manifest{Name: name, UUID: uuid, Version: version, Build: "lua",
		Provides: []string{semver.UnitKey(name, major)}}
	if err := manifest.SaveManifest(filepath.Join(work, "cosm.json"), m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "src", name+".lua"), []byte("return {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, g, work, "init")
	mustRun(t, g, work, "add", ".")
	mustRun(t, g, work, "commit", "-m", "init")
	mustRun(t, g, work, "branch", "-M", "main")
	mustRun(t, g, work, "tag", version)

	bare := filepath.Join(t.TempDir(), name+".git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, g, bare, "init", "--bare")
	mustRun(t, g, bare, "symbolic-ref", "HEAD", "refs/heads/master") // HEAD != pushed branch
	mustRun(t, g, work, "remote", "add", "origin", "file://"+bare)
	mustRun(t, g, work, "push", "origin", "main")
	mustRun(t, g, work, "push", "origin", "--tags")
	return "file://" + bare
}

func mustRun(t *testing.T, g gitx.Exec, dir string, args ...string) {
	t.Helper()
	if _, err := g.Run(dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestIntegration_InitAddResolve(t *testing.T) {
	isolateGit(t)
	d := newDepot(t)
	svc := NewService(d, gitx.Exec{})

	// Init a registry against an empty bare remote.
	regRemote := initBare(t, "reg.git")
	if err := svc.InitRegistry("R", "file://"+regRemote, types.KindSource); err != nil {
		t.Fatalf("InitRegistry: %v", err)
	}

	// A dependency package "dep" and a package "lib" that depends on it.
	depURL := makePkg(t, "dep", "u-dep", "v1.0.0", nil)
	if _, added, err := svc.AddPackage("R", depURL); err != nil || len(added) != 1 {
		t.Fatalf("AddPackage dep: added=%v err=%v", added, err)
	}
	libURL := makePkg(t, "lib", "u-lib", "v0.1.0", map[string]types.Dependency{
		semver.UnitKey("u-dep", 1): {Name: "dep", Version: "v1.0.0"},
	})
	if _, _, err := svc.AddPackage("R", libURL); err != nil {
		t.Fatalf("AddPackage lib: %v", err)
	}

	// specs.json carries commit + tree hash.
	loader := NewLoader(d)
	sp, err := loader.Load("dep", "u-dep", "v1.0.0")
	if err != nil {
		t.Fatalf("Load dep: %v", err)
	}
	if len(sp.Commit) != 40 || sp.Tree == "" || sp.GitURL != depURL {
		t.Errorf("dep specs missing commit/tree/giturl: %+v", sp)
	}

	// registry status reflects both packages.
	_, pkgs, err := svc.Status("R")
	if err != nil || len(pkgs) != 2 {
		t.Fatalf("Status: %d pkgs, err=%v", len(pkgs), err)
	}

	// Resolve an app that depends on lib -> pulls in dep transitively.
	root := &types.Manifest{Name: "app", UUID: "u-app", Version: "v0.1.0",
		Deps: map[string]types.Dependency{
			semver.UnitKey("u-lib", 0): {Name: "lib", Version: "v0.1.0"},
		}}
	bl, _, err := resolve.Resolve(root, loader, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got := map[string]string{}
	for _, e := range bl.Dependencies {
		got[e.Name] = e.Version
	}
	if got["lib"] != "v0.1.0" || got["dep"] != "v1.0.0" || len(got) != 2 {
		t.Fatalf("build list = %v", got)
	}
}

// TestIntegration_AddPackageBranchHeadMismatch reproduces the tutorial failure
// where `git init --bare` leaves HEAD at 'master' but the package is pushed on
// 'main': the clone has no working tree, and AddPackage must still find cosm.json.
func TestIntegration_AddPackageBranchHeadMismatch(t *testing.T) {
	isolateGit(t)
	d := newDepot(t)
	svc := NewService(d, gitx.Exec{})

	regRemote := initBare(t, "reg.git")
	if err := svc.InitRegistry("R", "file://"+regRemote, types.KindSource); err != nil {
		t.Fatalf("InitRegistry: %v", err)
	}

	url := makePkgMasterHead(t, "widget", "u-w", "v0.1.0")
	name, added, err := svc.AddPackage("R", url)
	if err != nil {
		t.Fatalf("AddPackage must recover from HEAD/branch mismatch: %v", err)
	}
	if name != "widget" || len(added) != 1 || added[0] != "v0.1.0" {
		t.Fatalf("name=%q added=%v", name, added)
	}
	if _, err := NewLoader(d).Load("widget", "u-w", "v0.1.0"); err != nil {
		t.Fatalf("Load registered version: %v", err)
	}
}

func TestIntegration_AddVersion(t *testing.T) {
	isolateGit(t)
	d := newDepot(t)
	svc := NewService(d, gitx.Exec{})
	regRemote := initBare(t, "reg.git")
	if err := svc.InitRegistry("R", "file://"+regRemote, types.KindSource); err != nil {
		t.Fatal(err)
	}
	url := makePkg(t, "p", "u-p", "v1.0.0", nil)
	if _, _, err := svc.AddPackage("R", url); err != nil {
		t.Fatal(err)
	}
	// Push a new tag to the package remote, then register just that version.
	g := gitx.Exec{}
	clone := filepath.Join(t.TempDir(), "p-clone")
	mustRun(t, g, "", "clone", url, clone)
	// bump version in cosm.json, commit, tag v1.1.0, push
	if err := manifest.SaveManifest(filepath.Join(clone, "cosm.json"), &types.Manifest{
		Name: "p", UUID: "u-p", Version: "v1.1.0", Build: "lua", Provides: []string{"p@v1"},
	}); err != nil {
		t.Fatal(err)
	}
	mustRun(t, g, clone, "add", ".")
	mustRun(t, g, clone, "commit", "-m", "v1.1.0")
	mustRun(t, g, clone, "tag", "v1.1.0")
	mustRun(t, g, clone, "push", "origin", "main")
	mustRun(t, g, clone, "push", "origin", "--tags")

	// Re-adding the package picks up the new tag (idempotent).
	_, added, err := svc.AddPackage("R", url)
	if err != nil || len(added) != 1 || added[0] != "v1.1.0" {
		t.Fatalf("re-add should register v1.1.0: added=%v err=%v", added, err)
	}
	// Adding again with nothing new is a no-op.
	if _, added, err := svc.AddPackage("R", url); err != nil || len(added) != 0 {
		t.Fatalf("idempotent re-add: added=%v err=%v", added, err)
	}
	loc, err := NewLoader(d).Find("p", "")
	if err != nil || len(loc) != 1 || loc[0].Specs.Version != "v1.1.0" {
		t.Fatalf("latest after re-add: %+v %v", loc, err)
	}
}

func TestIntegration_RemoveCloneUpdate(t *testing.T) {
	isolateGit(t)
	d := newDepot(t)
	svc := NewService(d, gitx.Exec{})
	regRemote := initBare(t, "reg.git")
	if err := svc.InitRegistry("R", "file://"+regRemote, types.KindSource); err != nil {
		t.Fatal(err)
	}
	url := makePkg(t, "p", "u-p", "v1.0.0", nil)
	if _, _, err := svc.AddPackage("R", url); err != nil {
		t.Fatal(err)
	}

	// Update (pull, no-op).
	if err := svc.UpdateRegistry("R"); err != nil {
		t.Fatalf("UpdateRegistry: %v", err)
	}
	// Remove the version, then the package.
	if err := svc.RemoveVersion("R", "p", "v1.0.0"); err != nil {
		t.Fatalf("RemoveVersion: %v", err)
	}
	if err := svc.RemovePackage("R", "p"); err != nil {
		t.Fatalf("RemovePackage: %v", err)
	}
	_, pkgs, err := svc.Status("R")
	if err != nil || len(pkgs) != 0 {
		t.Fatalf("Status after removal: %d pkgs %v", len(pkgs), err)
	}

	// Delete locally then clone back from the remote.
	if err := svc.DeleteRegistryLocal("R"); err != nil {
		t.Fatalf("DeleteRegistryLocal: %v", err)
	}
	name, err := svc.CloneRegistry("file://" + regRemote)
	if err != nil || name != "R" {
		t.Fatalf("CloneRegistry: %q %v", name, err)
	}
	refs, _ := svc.ListRegistries()
	if len(refs) != 1 || refs[0].Name != "R" {
		t.Fatalf("registries after clone: %+v", refs)
	}
}
