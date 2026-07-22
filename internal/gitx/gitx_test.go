// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// setupRepo creates a git repo with one commit and a tag, isolated from the
// user's real git config.
func setupRepo(t *testing.T) (Exec, string) {
	t.Helper()
	g := Exec{}
	tmp := t.TempDir()
	// Isolate git identity.
	cfg := filepath.Join(tmp, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[user]\n\tname = t\n\temail = t@t\n[init]\n\tdefaultBranch = main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := g.Init(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cosm.json"), []byte(`{"name":"p","uuid":"u","version":"v1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "p.lua"), []byte("return 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := g.Add(repo, "."); err != nil {
		t.Fatal(err)
	}
	if err := g.Commit(repo, "init"); err != nil {
		t.Fatal(err)
	}
	if err := g.Tag(repo, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	return g, repo
}

func TestCommitTagRevParse(t *testing.T) {
	g, repo := setupRepo(t)

	tags, err := g.ListTags(repo)
	if err != nil || len(tags) != 1 || tags[0] != "v1.0.0" {
		t.Fatalf("ListTags = %v %v", tags, err)
	}
	commit, err := g.RevParseCommit(repo, "v1.0.0")
	if err != nil || len(commit) != 40 {
		t.Fatalf("RevParseCommit = %q %v", commit, err)
	}
	// Commit is empty-worktree-clean after commit.
	status, err := g.StatusPorcelain(repo)
	if err != nil || status != "" {
		t.Fatalf("status = %q %v", status, err)
	}
}

func TestArchiveCommit(t *testing.T) {
	g, repo := setupRepo(t)
	commit, _ := g.RevParseCommit(repo, "v1.0.0")

	dest := filepath.Join(t.TempDir(), "export")
	if err := g.ArchiveCommit(repo, commit, dest); err != nil {
		t.Fatal(err)
	}
	// Exported tree must contain the files but NOT .git.
	if _, err := os.Stat(filepath.Join(dest, "cosm.json")); err != nil {
		t.Errorf("cosm.json not exported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "src", "p.lua")); err != nil {
		t.Errorf("src/p.lua not exported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should not be exported")
	}
}

func TestReadFileAtCommit(t *testing.T) {
	g, repo := setupRepo(t)
	commit, _ := g.RevParseCommit(repo, "v1.0.0")
	data, err := g.ReadFileAtCommit(repo, commit, "cosm.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[0] != '{' {
		t.Fatalf("unexpected file content: %q", string(data))
	}
}

func TestCloneAndLsRemoteTags(t *testing.T) {
	g, repo := setupRepo(t)
	// Clone via file:// then list remote tags.
	dst := filepath.Join(t.TempDir(), "clone")
	if err := g.Clone("file://"+repo, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "cosm.json")); err != nil {
		t.Errorf("clone missing cosm.json: %v", err)
	}
	tags, err := g.LsRemoteTags("file://" + repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] != "v1.0.0" {
		t.Fatalf("LsRemoteTags = %v", tags)
	}
}
