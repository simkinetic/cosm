// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package artifact

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("payload"))
	}))
	defer srv.Close()
	dst := filepath.Join(t.TempDir(), "a.tgz")
	if err := Fetch(srv.URL+"/a.tgz", dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "payload" {
		t.Errorf("http fetch content: %q", b)
	}

	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()
	if err := Fetch(notFound.URL, filepath.Join(t.TempDir(), "b")); err == nil {
		t.Error("expected error on HTTP 404")
	}
}

func TestPackStoreFetchUnpack(t *testing.T) {
	// Build a small tree.
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "lib"), 0o755)
	os.WriteFile(filepath.Join(src, "lib", "a.txt"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(src, "top"), []byte("t"), 0o755)
	_ = os.Symlink("lib/a.txt", filepath.Join(src, "alias")) // exercise symlink packing

	tgz, digest, size, err := Pack(src)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tgz)
	if size <= 0 || len(digest) < 10 || digest[:7] != "sha256:" {
		t.Fatalf("bad pack result: %s %d", digest, size)
	}

	// Store it, then fetch by the returned URL and verify.
	store := DirStore{Root: filepath.Join(t.TempDir(), "store")}
	url, err := store.Put(tgz, digest)
	if err != nil {
		t.Fatal(err)
	}
	fetched := filepath.Join(t.TempDir(), "f.tgz")
	if err := Fetch(url, fetched); err != nil {
		t.Fatal(err)
	}
	if err := VerifySha(fetched, digest); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Tampered digest must fail.
	if err := VerifySha(fetched, "sha256:deadbeef"); err == nil {
		t.Error("expected digest mismatch")
	}

	// Unpack and confirm contents.
	dst := filepath.Join(t.TempDir(), "out")
	if err := Unpack(fetched, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "lib", "a.txt")); string(b) != "hello" {
		t.Errorf("unpacked content wrong: %q", b)
	}
}

func TestFetchUnsupportedScheme(t *testing.T) {
	if err := Fetch("ftp://nope", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("expected unsupported scheme error")
	}
}

func TestPackMissingDir(t *testing.T) {
	if _, _, _, err := Pack(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected error packing a missing directory")
	}
}

func TestFetchMissingFile(t *testing.T) {
	if err := Fetch("file:///no/such/artifact.tgz", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("expected error fetching a missing file")
	}
}
