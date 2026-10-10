// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"os"
	"path/filepath"
	"testing"

	"cosm/internal/types"
)

// TestBuildListHash: the digest must move when anything below the project changes —
// including a develop checkout edited in place under an unchanged version — and stay
// put otherwise, independent of where checkouts live on disk.
func TestBuildListHash(t *testing.T) {
	devA := t.TempDir()
	if err := os.WriteFile(filepath.Join(devA, "m.lua"), []byte("return 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	mk := func(qVersion, devPath string) types.BuildList {
		return types.BuildList{Dependencies: map[string]types.BuildListEntry{
			"u-p@v1": {Name: "p", Version: "v1.2.0", Build: "lua", Tree: "sha256:p"},
			"u-q@v0": {Name: "q", Version: qVersion, Build: "lua", Tree: "sha256:q-" + qVersion},
			"u-d@v0": {Name: "d", Version: "v0.1.0", Build: "lua", Develop: true, SourcePath: devPath},
		}}
	}
	hash := func(bl types.BuildList) string {
		t.Helper()
		h, err := BuildListHash(bl)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	base := hash(mk("v0.3.0", devA))
	for i := 0; i < 10; i++ {
		if hash(mk("v0.3.0", devA)) != base {
			t.Fatal("not deterministic")
		}
	}
	if hash(mk("v0.3.1", devA)) == base {
		t.Error("a transitive version bump must change the hash")
	}

	// Same content at a different path (another machine, a worktree) -> same hash.
	devB := t.TempDir()
	if err := os.WriteFile(filepath.Join(devB, "m.lua"), []byte("return 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hash(mk("v0.3.0", devB)) != base {
		t.Error("hash must not depend on where a checkout lives")
	}
	// cosm's own .cosm/ state in the checkout is not content.
	if err := os.MkdirAll(filepath.Join(devA, ".cosm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devA, ".cosm", "develop.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hash(mk("v0.3.0", devA)) != base {
		t.Error(".cosm/ state must not change the hash")
	}
	// Editing a develop dependency in place, same version string -> new hash.
	if err := os.WriteFile(filepath.Join(devA, "m.lua"), []byte("return 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if hash(mk("v0.3.0", devA)) == base {
		t.Error("editing a develop checkout must change the hash")
	}
}
