// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/depot"
)

// TestSetupDoesNotHijackDefault: setting up a depot named explicitly (a throwaway
// verification depot) must leave the global default alone — otherwise every later
// command without $COSM_DEPOT silently runs against the throwaway depot.
func TestSetupDoesNotHijackDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("COSM_DEPOT", "")

	// The user's normal depot, found via the default (nothing named explicitly).
	main := filepath.Join(home, ".cosm")
	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}
	if got, _ := depot.DefaultPointer(); got != main {
		t.Fatalf("plain setup should set the default to %s, got %q", main, got)
	}

	// A throwaway depot via $COSM_DEPOT: default must not move.
	throwaway := filepath.Join(t.TempDir(), "verify-depot")
	t.Setenv("COSM_DEPOT", throwaway)
	out, err := runCLI(t, home, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := depot.DefaultPointer(); got != main {
		t.Fatalf("COSM_DEPOT setup hijacked the default: %q (want %s)", got, main)
	}
	if !strings.Contains(out, "Default depot unchanged") || !strings.Contains(out, "--set-default") {
		t.Errorf("should explain the default was kept, got:\n%s", out)
	}

	// Same via --depot.
	t.Setenv("COSM_DEPOT", "")
	other := filepath.Join(t.TempDir(), "flag-depot")
	if _, err := runCLI(t, home, "--depot", other, "setup"); err != nil {
		t.Fatal(err)
	}
	if got, _ := depot.DefaultPointer(); got != main {
		t.Fatalf("--depot setup hijacked the default: %q (want %s)", got, main)
	}

	// Opting in moves it.
	out, err = runCLI(t, home, "--depot", other, "setup", "--set-default")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := depot.DefaultPointer(); got != other {
		t.Fatalf("--set-default should move the default to %s, got %q", other, got)
	}
	if !strings.Contains(out, "Default depot set to") {
		t.Errorf("should announce the new default, got:\n%s", out)
	}
}
