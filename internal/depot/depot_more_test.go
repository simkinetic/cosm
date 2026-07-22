// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package depot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRootFromXDGConfig(t *testing.T) {
	os.Unsetenv("COSM_DEPOT")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// Write an XDG pointer to a depot.
	want := filepath.Join(t.TempDir(), "mydepot")
	dir := filepath.Join(xdg, "cosm")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"schemaVersion":1,"depot":"`+want+`"}`), 0o644)

	got, err := ResolveRoot("")
	if err != nil || got != want {
		t.Fatalf("ResolveRoot from XDG = %q %v, want %q", got, err, want)
	}
}

func TestSanitizeAndPaths(t *testing.T) {
	d := New("/depot")
	if got := d.BuildlistCache("u", "c"); got != "/depot/cache/buildlists/u/c.json" {
		t.Errorf("BuildlistCache = %s", got)
	}
	if got := d.Source("u", "c"); got != "/depot/sources/u/c" {
		t.Errorf("Source = %s", got)
	}
	if UnitDirName("p", 2) != "p@v2" {
		t.Errorf("UnitDirName = %s", UnitDirName("p", 2))
	}
	if _, err := MajorFromVersion("v3.1.0"); err != nil {
		t.Errorf("MajorFromVersion: %v", err)
	}
}
