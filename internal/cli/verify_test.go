// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cosm/internal/errs"
)

// TestVerify: a released version is built and tested in a throwaway depot from
// the registries alone, never touching the caller's depot or the global default —
// and it fails, with the cause, once a dependency it requires is gone.
func TestVerify(t *testing.T) {
	start := time.Now()
	home := setupEnv(t)
	depot := filepath.Join(home, ".cosm")
	ok := func(out string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return out
	}
	ok(runCLI(t, home, "setup"))
	buildLuaExt(t, depot)
	ok(runCLI(t, home, "registry", "init", "R", bare(t, home, "R.git")))

	release := func(name string, deps ...string) {
		dir := filepath.Join(home, name)
		_ = os.MkdirAll(dir, 0o755)
		ok(runCLI(t, dir, "init", name, "--build", "lua"))
		for _, d := range deps {
			ok(runCLI(t, dir, "add", d, "v0.1.0"))
		}
		gitRun(t, dir, "init", "-q", "-b", "main")
		gitRun(t, dir, "remote", "add", "origin", bare(t, home, name+".git"))
		gitRun(t, dir, "add", "-A")
		gitRun(t, dir, "commit", "-q", "-m", "init")
		gitRun(t, dir, "push", "-q", "-u", "origin", "main")
		ok(runCLI(t, dir, "release", "v0.1.0"))
		ok(runCLI(t, dir, "registry", "add", "R", "file://"+filepath.Join(home, "remotes", name+".git")))
	}
	release("lib")
	release("app", "lib")

	snapshot := func() string {
		ptr, _ := os.ReadFile(filepath.Join(home, ".config", "cosm", "config.json"))
		n := 0
		_ = filepath.Walk(depot, func(string, os.FileInfo, error) error { n++; return nil })
		return string(ptr) + "|" + strconv.Itoa(n)
	}

	remotes := filepath.Join(home, "remotes")
	for _, local := range []bool{false, true} {
		args := []string{"verify", "app"}
		if local {
			// --local promises offline use: with every remote unreachable it must
			// still work from the copied registry clones and package mirrors.
			args = append(args, "--local")
			if err := os.Rename(remotes, remotes+".offline"); err != nil {
				t.Fatal(err)
			}
		}
		before := snapshot()
		out, err := runCLI(t, home, args...)
		if local {
			if rerr := os.Rename(remotes+".offline", remotes); rerr != nil {
				t.Fatal(rerr)
			}
		}
		ok(out, err)
		for _, want := range []string{"Verifying app v0.1.0 from registry 'R'", "content hash verified",
			"resolved 1 dependencies from the registries alone", "Verified app v0.1.0"} {
			if !strings.Contains(out, want) {
				t.Errorf("verify (local=%v) output lacks %q:\n%s", local, want, out)
			}
		}
		if snapshot() != before {
			t.Errorf("verify (local=%v) changed the caller's depot or the global default", local)
		}
	}

	// Remove the dependency's only version: app can no longer be built from the
	// registries, and verify says so.
	ok(runCLI(t, home, "registry", "rm", "R", "lib", "v0.1.0", "-f"))
	_, err := runCLI(t, home, "verify", "app@v0.1.0", "--no-test")
	if !errors.Is(err, errs.ErrPackageNotFound) || errs.ExitCode(err) != 3 || !strings.Contains(err.Error(), "lib@v0.1.0") {
		t.Fatalf("want a not-found failure naming lib@v0.1.0, got %v", err)
	}
	// Every throwaway depot this test created was cleaned up.
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "cosm-verify-*"))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.ModTime().After(start) {
			t.Errorf("throwaway depot left behind: %s", m)
		}
	}
}
