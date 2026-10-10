// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersionText: --version must identify the binary, not just say "dev" — the
// release version when set, and always the path it runs from.
func TestVersionText(t *testing.T) {
	old := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = old })

	out := versionText()
	if !strings.HasPrefix(out, "cosm version v9.9.9") {
		t.Errorf("first line should carry the release version: %q", out)
	}
	exe, _ := os.Executable()
	if !strings.Contains(out, "binary: "+realPath(exe)) {
		t.Errorf("should print the running binary's path %s: %q", realPath(exe), out)
	}
}

// TestVersionTextFlagsShadowing: when `cosm` on PATH is a different binary than the
// one answering (a stale install earlier on PATH), say so.
func TestVersionTextFlagsShadowing(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "cosm")
	if err := os.WriteFile(stale, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if out := versionText(); !strings.Contains(out, "note: 'cosm' on PATH is "+realPath(stale)) {
		t.Errorf("should flag the shadowing binary %s: %q", stale, out)
	}
}
