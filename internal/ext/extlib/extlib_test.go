package extlib

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWriteRequest(t *testing.T) {
	// WriteResponse to a pipe, ReadRequest from it.
	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	type msg struct {
		A int `json:"a"`
	}
	if err := WriteResponse(msg{A: 7}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	os.Stdout = oldOut

	oldIn := os.Stdin
	os.Stdin = r
	var got msg
	err := ReadRequest(&got)
	os.Stdin = oldIn
	if err != nil || got.A != 7 {
		t.Fatalf("round-trip: %+v %v", got, err)
	}
}

func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "src", "pkg@v0"), 0o755)
	os.MkdirAll(filepath.Join(src, ".git"), 0o755)
	os.WriteFile(filepath.Join(src, "src", "pkg@v0", "a.lua"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(src, ".git", "junk"), []byte("no"), 0o644)

	dst := filepath.Join(t.TempDir(), "out")
	if err := CopyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "src", "pkg@v0", "a.lua")); err != nil {
		t.Errorf("file not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should be excluded")
	}
}

func TestCopyTreeSymlink(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "real.txt"), []byte("hi"), 0o644)
	if err := os.Symlink("real.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Skip("symlinks unsupported")
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := CopyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(dst, "link.txt"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink not preserved: %v", err)
	}
}
