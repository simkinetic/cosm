package service

import (
	"os"
	"path/filepath"
	"testing"

	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

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
