// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package ext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/depot"
)

// TestRunnerEnv: Runner.Env reaches the extension (and so whatever it spawns),
// without touching the calling process's own environment.
func TestRunnerEnv(t *testing.T) {
	d := depot.New(t.TempDir())
	dir := filepath.Join(d.Extensions(), "envdump")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "env.txt")
	script := "#!/bin/sh\ncat >/dev/null\nenv > '" + out + "'\necho '{\"status\":\"ok\",\"tests\":1}'\n"
	if err := os.WriteFile(filepath.Join(dir, "cosm-ext-envdump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(d)
	r.Env = []string{"COSM_TEST_JOBS=7", "COSM_TEST_CPUS=64"}
	if _, err := r.Test("envdump", TestRequest{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	for _, want := range []string{"COSM_TEST_JOBS=7", "COSM_TEST_CPUS=64", "PATH="} {
		if !strings.Contains(string(b), want+"") {
			t.Errorf("extension env lacks %s", want)
		}
	}
	if os.Getenv("COSM_TEST_JOBS") != "" {
		t.Error("Runner.Env must not leak into the calling process")
	}
}
