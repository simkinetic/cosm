// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package extlib

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyTreeSymlinkedSource: a source reached through a symlink is copied as the
// directory it points to (it used to copy as an empty tree, so a lua build through
// dev/<name>@v<major> installed nothing).
func TestCopyTreeSymlinkedSource(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "src", "m.lua"), []byte("return 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := CopyTree(link, dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "src", "m.lua")); err != nil || string(b) != "return 1" {
		t.Fatalf("copied file = %q, %v", b, err)
	}
}
