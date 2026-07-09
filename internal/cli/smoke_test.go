package cli_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal real-binary check; the full flow is covered in-process (inproc_test.go).

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cosm")
	root, _ := filepath.Abs("../..")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cosm: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, bin, dir, home string, args ...string) (string, string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+home, "COSM_DEPOT="+filepath.Join(home, ".cosm"),
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
	)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func TestBinary_VersionAndSetup(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	out, _, err := run(t, bin, home, home, "--version")
	if err != nil || !strings.Contains(out, "cosm version") {
		t.Fatalf("--version: %q %v", out, err)
	}
	if _, _, err := run(t, bin, home, home, "setup"); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestBinary_UninitializedHint(t *testing.T) {
	bin := buildBinary(t)
	home := t.TempDir()
	_, errOut, err := run(t, bin, home, home, "status")
	if err == nil || !strings.Contains(errOut, "cosm setup") {
		t.Fatalf("expected setup hint, got err=%v stderr=%q", err, errOut)
	}
}
