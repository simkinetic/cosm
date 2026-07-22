// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package treehash

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicAndSensitive(t *testing.T) {
	d1 := t.TempDir()
	write(t, d1, "src/a.lua", "print(1)")
	write(t, d1, "README.md", "hi")
	h1, err := Tree(d1)
	if err != nil {
		t.Fatal(err)
	}

	// Same content in a fresh dir -> identical hash.
	d2 := t.TempDir()
	write(t, d2, "README.md", "hi")
	write(t, d2, "src/a.lua", "print(1)")
	h2, err := Tree(d2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash not deterministic across dirs: %s vs %s", h1, h2)
	}

	// Change content -> different hash.
	write(t, d2, "src/a.lua", "print(2)")
	h3, _ := Tree(d2)
	if h3 == h1 {
		t.Fatal("content change did not change hash")
	}
}

func TestGitExcluded(t *testing.T) {
	d := t.TempDir()
	write(t, d, "file.txt", "data")
	before, _ := Tree(d)
	// Add a .git dir with junk — must not affect the hash.
	write(t, d, ".git/HEAD", "ref: refs/heads/main")
	write(t, d, ".git/config", "[core]")
	after, _ := Tree(d)
	if before != after {
		t.Fatalf(".git changed the tree hash: %s vs %s", before, after)
	}
}

func TestSymlinkHashed(t *testing.T) {
	d := t.TempDir()
	write(t, d, "real.txt", "data")
	if err := os.Symlink("real.txt", filepath.Join(d, "link")); err != nil {
		t.Skip("symlinks unsupported")
	}
	h1, err := Tree(d)
	if err != nil {
		t.Fatal(err)
	}
	// Repointing the symlink changes the hash.
	os.Remove(filepath.Join(d, "link"))
	os.Symlink("other.txt", filepath.Join(d, "link"))
	h2, _ := Tree(d)
	if h1 == h2 {
		t.Error("symlink target change should change hash")
	}
}

func TestAddedFileChangesHash(t *testing.T) {
	d := t.TempDir()
	write(t, d, "a.txt", "x")
	h1, _ := Tree(d)
	write(t, d, "b.txt", "y")
	h2, _ := Tree(d)
	if h1 == h2 {
		t.Fatal("adding a file did not change the hash")
	}
}
