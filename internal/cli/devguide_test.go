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

// currentGuideSHA is the hash of docs/dev-workspace.md as it stands. It exists so
// that editing the guide forces you to record the previous text in
// shippedGuideSHAs — otherwise depots holding that text would never be updated.
const currentGuideSHA = "f2b15a143670f5424b8c5cd7e0b567a22a31fa819b3c0b3883ba08e10da5e3b2"

func TestGuideHistoryIsCurrent(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(moduleRoot, "docs", "dev-workspace.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := sha256Hex(b)
	if got != currentGuideSHA {
		t.Fatalf("docs/dev-workspace.md changed. Add its previous hash to shippedGuideSHAs in devguide.go:\n"+
			"\t%q: true,\nthen set currentGuideSHA in this test to %q.", currentGuideSHA, got)
	}
	if len(shippedGuideSHAs) == 0 {
		t.Fatal("shippedGuideSHAs is empty")
	}
	if shippedGuideSHAs[got] {
		t.Fatal("the current guide must not be listed as an earlier version")
	}
}

// setGuide installs a fake shipped guide and history for one test.
func setGuide(t *testing.T, text string, earlier ...string) {
	t.Helper()
	oldText, oldHist := DevWorkspaceGuide, shippedGuideSHAs
	DevWorkspaceGuide = text
	shippedGuideSHAs = map[string]bool{}
	for _, e := range earlier {
		shippedGuideSHAs[sha256Hex([]byte(e))] = true
	}
	t.Cleanup(func() { DevWorkspaceGuide, shippedGuideSHAs = oldText, oldHist })
}

func guideDepot(t *testing.T) (string, depot.Depot, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COSM_DEPOT", filepath.Join(home, ".cosm"))
	d := depot.New(filepath.Join(home, ".cosm"))
	return home, d, filepath.Join(d.Dev(), "CLAUDE.md")
}

func readGuide(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

// TestDevGuideSync: setup keeps dev/CLAUDE.md current — written when absent,
// updated when it is a known earlier version — and never discards edits.
func TestDevGuideSync(t *testing.T) {
	setGuide(t, "# guide v2\n", "# guide v1\n")
	home, _, guide := guideDepot(t)

	out, err := runCLI(t, home, "setup")
	if err != nil || !strings.Contains(out, "Wrote agent guide") || readGuide(t, guide) != "# guide v2\n" {
		t.Fatalf("absent guide should be written: %v\n%s", err, out)
	}
	if out, _ = runCLI(t, home, "setup"); strings.Contains(out, "agent guide") {
		t.Errorf("a current guide should be left without a word:\n%s", out)
	}

	// An untouched earlier version is updated, no flag needed.
	_ = os.WriteFile(guide, []byte("# guide v1\n"), 0o644)
	if out, _ = runCLI(t, home, "setup"); !strings.Contains(out, "Updated agent guide") || readGuide(t, guide) != "# guide v2\n" {
		t.Errorf("an earlier guide should be updated:\n%s", out)
	}

	// User edits are kept, with the way out.
	_ = os.WriteFile(guide, []byte("# my notes\n"), 0o644)
	if out, _ = runCLI(t, home, "setup"); !strings.Contains(out, "--force-guide") || readGuide(t, guide) != "# my notes\n" {
		t.Errorf("an edited guide must be kept:\n%s", out)
	}
	if out, _ = runCLI(t, home, "setup", "--force-guide"); !strings.Contains(out, "Refreshed agent guide") || readGuide(t, guide) != "# guide v2\n" {
		t.Errorf("--force-guide should replace it:\n%s", out)
	}
}

// TestDevGuideNeverDowngrades: an older cosm meeting a guide written by a newer
// one must leave it alone. To the older binary that guide is unknown text — not
// its own, not any earlier one — exactly like a user edit.
func TestDevGuideNeverDowngrades(t *testing.T) {
	home, _, guide := guideDepot(t)
	setGuide(t, "# guide v3\n", "# guide v1\n", "# guide v2\n") // a newer cosm
	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}

	setGuide(t, "# guide v2\n", "# guide v1\n") // now an older cosm runs setup
	out, err := runCLI(t, home, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if readGuide(t, guide) != "# guide v3\n" {
		t.Fatalf("an older cosm downgraded the guide to %q:\n%s", readGuide(t, guide), out)
	}
}

// TestDevGuideLegacyDepot: v0.1.4/v0.1.5 recorded devGuideSha and replaced any
// guide matching it — even with an older text of their own. Newer cosm updates
// such a depot from the history and clears the hash, so those old binaries then
// leave the new guide alone. A legacy guide with no hash at all also updates.
func TestDevGuideLegacyDepot(t *testing.T) {
	setGuide(t, "# guide v2\n", "# guide v1\n")
	home, d, guide := guideDepot(t)
	if err := d.Setup(); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Dir(guide), 0o755)
	_ = os.WriteFile(guide, []byte("# guide v1\n"), 0o644)
	cfg, _ := d.LoadConfig()
	cfg.DevGuideSHA = sha256Hex([]byte("# guide v1\n"))
	if err := d.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	if out, _ := runCLI(t, home, "setup"); !strings.Contains(out, "Updated agent guide") {
		t.Fatalf("legacy depot should update:\n%s", out)
	}
	if cfg, _ := d.LoadConfig(); cfg.DevGuideSHA != "" {
		t.Errorf("devGuideSha must be cleared so v0.1.4/5 can't revert the guide, got %q", cfg.DevGuideSHA)
	}
}
