// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package treehash

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLiveIgnoresCosmState: Live (the build-key input for a working checkout)
// ignores a top-level .cosm/, while Tree (the integrity anchor stored in registry
// specs) is unchanged and still covers it — already-registered packages that
// committed a .cosm/develop.json must keep verifying.
func TestLiveIgnoresCosmState(t *testing.T) {
	root := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/a.lua", "return 1")
	tree0, _ := Tree(root)
	live0, _ := Live(root)
	if tree0 != live0 {
		t.Fatalf("without .cosm/, Tree and Live must agree: %s vs %s", tree0, live0)
	}

	// cosm's own state appears: enrollment, gitignore, an env link to a prefix.
	write(".cosm/develop.json", `{"enrolled":[]}`)
	write(".cosm/.gitignore", "*\n")
	if err := os.MkdirAll(filepath.Join(root, ".cosm", "env"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/depot/builds/x/artifacts", filepath.Join(root, ".cosm", "env", "app@v0")); err != nil {
		t.Fatal(err)
	}
	if live, _ := Live(root); live != live0 {
		t.Error("Live must ignore the top-level .cosm/")
	}
	if tree, _ := Tree(root); tree == tree0 {
		t.Error("Tree must keep covering .cosm/ (integrity hashes depend on it)")
	}
	// Re-pointing the env link (what every activate does) must not move Live.
	_ = os.Remove(filepath.Join(root, ".cosm", "env", "app@v0"))
	_ = os.Symlink("/depot/builds/y/artifacts", filepath.Join(root, ".cosm", "env", "app@v0"))
	if live, _ := Live(root); live != live0 {
		t.Error("re-pointing .cosm/env changed Live: rebuild loop")
	}

	// Only the TOP-LEVEL .cosm is cosm state; a nested one is ordinary source.
	write("vendor/.cosm/data.txt", "x")
	if live, _ := Live(root); live == live0 {
		t.Error("a nested .cosm/ is source and must be hashed")
	}
}
