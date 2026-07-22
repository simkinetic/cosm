// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// setupWithRemote creates a repo with a bare origin on branch main.
func setupWithRemote(t *testing.T) (Exec, string, string) {
	g, repo := setupRepo(t)
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	os.MkdirAll(bareDir, 0o755)
	mustGit(t, g, bareDir, "init", "--bare")
	mustGit(t, g, bareDir, "symbolic-ref", "HEAD", "refs/heads/main")
	mustGit(t, g, repo, "branch", "-M", "main")
	mustGit(t, g, repo, "remote", "add", "origin", "file://"+bareDir)
	if err := g.Push(repo, "main"); err != nil {
		t.Fatal(err)
	}
	return g, repo, bareDir
}

func mustGit(t *testing.T, g Exec, dir string, args ...string) {
	t.Helper()
	if _, err := g.Run(dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestBranchAndSync(t *testing.T) {
	g, repo, _ := setupWithRemote(t)

	if b, err := g.CurrentBranch(repo); err != nil || b != "main" {
		t.Fatalf("CurrentBranch = %q %v", b, err)
	}
	if b, err := g.DefaultBranch(repo); err != nil || b == "" {
		t.Fatalf("DefaultBranch = %q %v", b, err)
	}
	if err := g.Fetch(repo, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if n, err := g.BehindCount(repo, "main"); err != nil || n != 0 {
		t.Fatalf("BehindCount = %d %v", n, err)
	}
}

func TestCheckoutAndCommitNoop(t *testing.T) {
	g, repo := setupRepo(t)
	// Checkout the tag (detached) then back to a branch.
	commit, _ := g.RevParseCommit(repo, "v1.0.0")
	if err := g.Checkout(repo, commit); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	// Commit with nothing staged is a no-op (not an error).
	mustGit(t, g, repo, "checkout", "-")
	if err := g.Commit(repo, "empty"); err != nil {
		t.Fatalf("empty Commit should be a no-op: %v", err)
	}
}

func TestMirrorCloneAndRun(t *testing.T) {
	g, repo := setupRepo(t)
	mirror := filepath.Join(t.TempDir(), "m.git")
	if err := g.MirrorClone("file://"+repo, mirror); err != nil {
		t.Fatalf("MirrorClone: %v", err)
	}
	// Archive from the mirror works.
	commit, _ := g.RevParseCommit(mirror, "v1.0.0")
	dest := filepath.Join(t.TempDir(), "x")
	if err := g.ArchiveCommit(mirror, commit, dest); err != nil {
		t.Fatalf("ArchiveCommit from mirror: %v", err)
	}
	// Run surfaces errors.
	if _, err := g.Run(repo, "not-a-real-subcommand"); err == nil {
		t.Error("expected error from bogus git subcommand")
	}
}
