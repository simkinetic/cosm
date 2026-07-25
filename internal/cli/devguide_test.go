// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/depot"
)

// TestDevGuideSync: `cosm setup` must keep dev/CLAUDE.md current for agents without
// ever silently discarding a user's edits — the four states it can find on disk.
func TestDevGuideSync(t *testing.T) {
	old := DevWorkspaceGuide
	DevWorkspaceGuide = "# guide v1\n"
	t.Cleanup(func() { DevWorkspaceGuide = old })

	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".cosm")
	t.Setenv("COSM_DEPOT", root)
	d := depot.New(root)
	guide := filepath.Join(d.Dev(), "CLAUDE.md")

	// 1. Absent -> written.
	out, err := runCLI(t, home, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Wrote agent guide") {
		t.Errorf("first setup should write the guide, got:\n%s", out)
	}
	if b, _ := os.ReadFile(guide); string(b) != "# guide v1\n" {
		t.Fatalf("guide = %q", b)
	}

	// 2. Unchanged -> silent (no noise on every setup).
	out, err = runCLI(t, home, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "agent guide") {
		t.Errorf("re-running setup should say nothing about the guide, got:\n%s", out)
	}

	// 3. cosm ships a newer guide and the on-disk one is untouched -> refreshed.
	DevWorkspaceGuide = "# guide v2\n"
	out, err = runCLI(t, home, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Updated agent guide") {
		t.Errorf("an untouched guide should be updated, got:\n%s", out)
	}
	if b, _ := os.ReadFile(guide); string(b) != "# guide v2\n" {
		t.Fatalf("guide not refreshed: %q", b)
	}

	// 4. User edited it -> kept, with a pointer to --force-guide.
	if err := os.WriteFile(guide, []byte("# my notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	DevWorkspaceGuide = "# guide v3\n"
	out, err = runCLI(t, home, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "--force-guide") {
		t.Errorf("an edited guide should be kept with guidance, got:\n%s", out)
	}
	if b, _ := os.ReadFile(guide); string(b) != "# my notes\n" {
		t.Fatalf("user edits were clobbered: %q", b)
	}

	// ...and --force-guide replaces it on request.
	out, err = runCLI(t, home, "setup", "--force-guide")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Refreshed agent guide") {
		t.Errorf("--force-guide should refresh, got:\n%s", out)
	}
	if b, _ := os.ReadFile(guide); string(b) != "# guide v3\n" {
		t.Fatalf("guide = %q", b)
	}
}
