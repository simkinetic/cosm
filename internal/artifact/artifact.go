// Package artifact packs, stores, fetches, and verifies binary build artifacts
// for binary registries (§8.5). Artifacts are gzip-compressed tarballs addressed
// by their sha256; the store is any directory (referenced by file:// URLs) or an
// http(s) endpoint on the consume side.
package artifact

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Pack writes srcDir into a temporary .tar.gz and returns its path, "sha256:<hex>"
// digest, and size.
func Pack(srcDir string) (tgzPath, digest string, size int64, err error) {
	f, err := os.CreateTemp("", "cosm-artifact-*.tar.gz")
	if err != nil {
		return "", "", 0, err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	walkErr := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(srcDir, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: rel + "/", Typeflag: tar.TypeDir, Mode: int64(info.Mode().Perm())})
		case info.Mode()&fs.ModeSymlink != 0:
			link, lerr := os.Readlink(p)
			if lerr != nil {
				return lerr
			}
			return tw.WriteHeader(&tar.Header{Name: rel, Typeflag: tar.TypeSymlink, Linkname: link})
		case info.Mode().IsRegular():
			if err := tw.WriteHeader(&tar.Header{Name: rel, Typeflag: tar.TypeReg, Mode: int64(info.Mode().Perm()), Size: info.Size()}); err != nil {
				return err
			}
			in, oerr := os.Open(p)
			if oerr != nil {
				return oerr
			}
			defer in.Close()
			_, cerr := io.Copy(tw, in)
			return cerr
		}
		return nil
	})
	if walkErr != nil {
		tw.Close()
		gz.Close()
		f.Close()
		os.Remove(f.Name())
		return "", "", 0, walkErr
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return "", "", 0, err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return "", "", 0, err
	}
	if err := f.Close(); err != nil {
		return "", "", 0, err
	}
	digest, err = Sha256File(f.Name())
	if err != nil {
		return "", "", 0, err
	}
	fi, _ := os.Stat(f.Name())
	return f.Name(), digest, fi.Size(), nil
}

// Unpack extracts a .tar.gz into destDir.
func Unpack(tgz, destDir string) error {
	f, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(hdr.Name)
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		target := filepath.Join(destDir, clean)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe artifact path %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777|0o700); err != nil {
				return err
			}
		case tar.TypeSymlink:
			os.MkdirAll(filepath.Dir(target), 0o755)
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0o755)
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
}

// Store persists an artifact and returns a URL to fetch it later.
type Store interface {
	Put(localFile, key string) (url string, err error)
}

// DirStore stores artifacts as files under Root, referenced by file:// URLs.
type DirStore struct{ Root string }

func (s DirStore) Put(localFile, key string) (string, error) {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(s.Root, sanitize(key)+".tar.gz")
	if err := copyFile(localFile, dst); err != nil {
		return "", err
	}
	abs, _ := filepath.Abs(dst)
	return "file://" + abs, nil
}

// Fetch downloads url (file:// or http(s)://) into destFile.
func Fetch(url, destFile string) error {
	switch {
	case strings.HasPrefix(url, "file://"):
		return copyFile(strings.TrimPrefix(url, "file://"), destFile)
	case strings.HasPrefix(url, "http://"), strings.HasPrefix(url, "https://"):
		resp, err := http.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
		}
		out, err := os.Create(destFile)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, resp.Body)
		return err
	default:
		return fmt.Errorf("unsupported artifact URL scheme: %s", url)
	}
}

// Sha256File returns "sha256:<hex>" for a file.
func Sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// VerifySha checks a file against an expected "sha256:<hex>" digest.
func VerifySha(path, expected string) error {
	got, err := Sha256File(path)
	if err != nil {
		return err
	}
	if got != expected {
		return fmt.Errorf("artifact digest %s != expected %s", got, expected)
	}
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func sanitize(key string) string {
	r := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	return r.Replace(key)
}
