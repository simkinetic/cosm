// Package gitx is the Git boundary (§16.1). Higher layers depend on the Git
// interface; Exec implements it by shelling out to the git binary. Tests use
// fakes or a real local git via file:// bare repos.
package gitx

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"cosm/internal/errs"
)

// Git is the set of git operations cosm needs.
type Git interface {
	Init(dir string) error
	CurrentBranch(dir string) (string, error)
	DefaultBranch(dir string) (string, error)
	StatusPorcelain(dir string) (string, error)
	Add(dir string, paths ...string) error
	Commit(dir, msg string) error
	Tag(dir, tag string) error
	ListTags(dir string) ([]string, error)
	RevParseCommit(dir, ref string) (string, error)
	BehindCount(dir, branch string) (int, error)
	Checkout(dir, ref string) error
	Clone(url, dst string, extraArgs ...string) error
	MirrorClone(url, dst string) error
	Fetch(dir string, args ...string) error
	Pull(dir, remote, branch string) error
	Push(dir, ref string) error
	LsRemoteTags(url string) ([]string, error)
	ArchiveCommit(dir, commit, destDir string) error
	ReadFileAtCommit(dir, commit, path string) ([]byte, error)
	Run(dir string, args ...string) (string, error)
}

// Exec runs the system git binary.
type Exec struct{}

var _ Git = Exec{}

func (Exec) raw(dir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var o, e bytes.Buffer
	cmd.Stdout = &o
	cmd.Stderr = &e
	err = cmd.Run()
	return o.String(), e.String(), err
}

func fail(dir string, args []string, stderr string, err error) error {
	return fmt.Errorf("git %s in %q: %v\n%s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr))
}

func failNet(dir string, args []string, stderr string, err error) error {
	return fmt.Errorf("%w: git %s in %q: %v\n%s", errs.ErrNetwork, strings.Join(args, " "), dir, err, strings.TrimSpace(stderr))
}

func (e Exec) Run(dir string, args ...string) (string, error) {
	o, se, err := e.raw(dir, args...)
	if err != nil {
		return strings.TrimSpace(o), fail(dir, args, se, err)
	}
	return strings.TrimSpace(o), nil
}

func (e Exec) Init(dir string) error {
	_, se, err := e.raw(dir, "init")
	if err != nil {
		return fail(dir, []string{"init"}, se, err)
	}
	return nil
}

func (e Exec) CurrentBranch(dir string) (string, error) {
	out, se, err := e.raw(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fail(dir, []string{"rev-parse"}, se, err)
	}
	b := strings.TrimSpace(out)
	if b == "HEAD" {
		return "", fmt.Errorf("detached HEAD in %q", dir)
	}
	return b, nil
}

func (e Exec) DefaultBranch(dir string) (string, error) {
	if out, _, err := e.raw(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		b := strings.TrimPrefix(strings.TrimSpace(out), "origin/")
		if b != "" {
			return b, nil
		}
	}
	return e.CurrentBranch(dir)
}

func (e Exec) StatusPorcelain(dir string) (string, error) {
	out, se, err := e.raw(dir, "status", "--porcelain")
	if err != nil {
		return "", fail(dir, []string{"status"}, se, err)
	}
	return out, nil
}

func (e Exec) Add(dir string, paths ...string) error {
	args := append([]string{"add"}, paths...)
	_, se, err := e.raw(dir, args...)
	if err != nil {
		return fail(dir, args, se, err)
	}
	return nil
}

func (e Exec) Commit(dir, msg string) error {
	o, se, err := e.raw(dir, "commit", "-m", msg)
	if err != nil {
		if strings.Contains(o+se, "nothing to commit") {
			return nil
		}
		return fail(dir, []string{"commit"}, se, err)
	}
	return nil
}

func (e Exec) Tag(dir, tag string) error {
	_, se, err := e.raw(dir, "tag", tag)
	if err != nil {
		return fail(dir, []string{"tag", tag}, se, err)
	}
	return nil
}

func (e Exec) ListTags(dir string) ([]string, error) {
	out, se, err := e.raw(dir, "tag", "--list")
	if err != nil {
		return nil, fail(dir, []string{"tag", "--list"}, se, err)
	}
	return splitLines(out), nil
}

func (e Exec) RevParseCommit(dir, ref string) (string, error) {
	out, se, err := e.raw(dir, "rev-list", "-n", "1", ref)
	if err != nil {
		return "", fail(dir, []string{"rev-list", ref}, se, err)
	}
	return strings.TrimSpace(out), nil
}

func (e Exec) BehindCount(dir, branch string) (int, error) {
	out, se, err := e.raw(dir, "rev-list", "--count", "HEAD..origin/"+branch)
	if err != nil {
		return 0, fail(dir, []string{"rev-list", "--count"}, se, err)
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &n); err != nil {
		return 0, fmt.Errorf("parse behind count %q: %v", out, err)
	}
	return n, nil
}

func (e Exec) Checkout(dir, ref string) error {
	_, se, err := e.raw(dir, "checkout", ref)
	if err != nil {
		return fail(dir, []string{"checkout", ref}, se, err)
	}
	return nil
}

func (e Exec) Clone(url, dst string, extraArgs ...string) error {
	args := append([]string{"clone"}, extraArgs...)
	args = append(args, url, dst)
	_, se, err := e.raw("", args...)
	if err != nil {
		return failNet("", args, se, err)
	}
	return nil
}

func (e Exec) MirrorClone(url, dst string) error {
	return e.Clone(url, dst, "--mirror")
}

func (e Exec) Fetch(dir string, args ...string) error {
	full := append([]string{"fetch"}, args...)
	_, se, err := e.raw(dir, full...)
	if err != nil {
		return failNet(dir, full, se, err)
	}
	return nil
}

func (e Exec) Pull(dir, remote, branch string) error {
	args := []string{"pull", remote, branch}
	_, se, err := e.raw(dir, args...)
	if err != nil {
		return failNet(dir, args, se, err)
	}
	return nil
}

func (e Exec) Push(dir, ref string) error {
	args := []string{"push", "origin", ref}
	o, se, err := e.raw(dir, args...)
	if err != nil {
		if strings.Contains(o+se, "up-to-date") {
			return nil
		}
		return failNet(dir, args, se, err)
	}
	return nil
}

func (e Exec) LsRemoteTags(url string) ([]string, error) {
	out, se, err := e.raw("", "ls-remote", "--tags", url)
	if err != nil {
		return nil, failNet("", []string{"ls-remote", url}, se, err)
	}
	var tags []string
	seen := map[string]bool{}
	for _, line := range splitLines(out) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		ref := strings.TrimPrefix(fields[1], "refs/tags/")
		ref = strings.TrimSuffix(ref, "^{}") // dereferenced annotated tag
		if ref == fields[1] || ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		tags = append(tags, ref)
	}
	return tags, nil
}

// ArchiveCommit exports a commit's tree into destDir via `git archive`, never
// touching the working tree (§8.1).
func (e Exec) ArchiveCommit(dir, commit, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("git", "archive", "--format=tar", commit)
	cmd.Dir = dir
	var errb bytes.Buffer
	cmd.Stderr = &errb
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if xerr := extractTar(pipe, destDir); xerr != nil {
		_ = cmd.Wait()
		return xerr
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git archive %s in %q: %v\n%s", commit, dir, err, strings.TrimSpace(errb.String()))
	}
	return nil
}

func extractTar(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)
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
			return fmt.Errorf("unsafe tar path %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777|0o700); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
}

func (e Exec) ReadFileAtCommit(dir, commit, path string) ([]byte, error) {
	o, se, err := e.raw(dir, "show", commit+":"+path)
	if err != nil {
		return nil, fail(dir, []string{"show", commit + ":" + path}, se, err)
	}
	return []byte(o), nil
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
