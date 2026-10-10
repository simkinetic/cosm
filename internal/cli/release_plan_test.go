// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/errs"
	"cosm/internal/gitx"
)

// TestReleaseDryRun: --dry-run prints the preflight, the steps, and the registry
// entry; exits non-zero when the release would be refused (a CI gate); and never
// changes the repository, even when the plan is clean.
func TestReleaseDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COSM_DEPOT", filepath.Join(home, ".cosm"))
	t.Setenv("COSM_OFFLINE", "1")
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t"} {
		t.Setenv(k, v)
	}
	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(home, "lib")
	_ = os.MkdirAll(pkg, 0o755)
	if _, err := runCLI(t, pkg, "init", "lib"); err != nil {
		t.Fatal(err)
	}
	gitRun(t, pkg, "init", "-q", "-b", "main")
	gitRun(t, pkg, "add", "-A")
	gitRun(t, pkg, "commit", "-q", "-m", "init")
	head := func() string {
		out, _ := (gitx.Exec{}).Run(pkg, "rev-parse", "HEAD")
		return out
	}
	before := head()

	// No origin: refused, non-zero, with the fix — and nothing changed.
	out, err := runCLI(t, pkg, "release", "--patch", "--dry-run")
	var dr *dryRunFailed
	if !errors.As(err, &dr) || errs.ExitCode(err) != 2 {
		t.Fatalf("want a usage-coded dry-run failure, got %v (exit %d)", err, errs.ExitCode(err))
	}
	for _, want := range []string{"Release plan for lib: v0.1.0 -> v0.1.1", "FAIL  an origin remote exists",
		"git remote add origin", "Would do, once the problems above are fixed", "create tag v0.1.1",
		"not in any registry yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output lacks %q:\n%s", want, out)
		}
	}

	// With an origin it passes, exits zero, and still changes nothing.
	bare := filepath.Join(home, "remotes", "lib.git")
	_ = os.MkdirAll(bare, 0o755)
	gitRun(t, bare, "init", "-q", "--bare")
	gitRun(t, pkg, "remote", "add", "origin", bare)
	gitRun(t, pkg, "push", "-q", "-u", "origin", "main")
	out, err = runCLI(t, pkg, "release", "--patch", "--dry-run")
	if err != nil || strings.Contains(out, "FAIL") || !strings.Contains(out, "push branch main, then tag v0.1.1, to origin ("+bare+")") {
		t.Fatalf("clean dry run: %v\n%s", err, out)
	}
	if head() != before {
		t.Error("--dry-run changed the repository")
	}
	if tags, _ := (gitx.Exec{}).Run(pkg, "tag"); strings.TrimSpace(tags) != "" {
		t.Errorf("--dry-run created tags: %q", tags)
	}
}
