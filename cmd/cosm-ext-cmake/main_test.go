// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"cosm/internal/ext"
)

// TestTestKeySeparatesCheckouts: the test tree is keyed by the source checkout, so
// two worktrees of the same name@version — or two concurrent runs — never share
// (and wipe) one directory. It must stay deterministic per invocation, since
// --keep-build hands the path to coverage tooling.
func TestTestKeySeparatesCheckouts(t *testing.T) {
	req := func(src, cxx, ld string) ext.TestRequest {
		return ext.TestRequest{
			Project:  ext.PackageCtx{Name: "lib", Version: "v0.1.0", Source: src},
			CxxFlags: cxx, LdFlags: ld,
		}
	}
	base := testKey(req("/w/a", "", ""), "Release")

	if got := testKey(req("/w/a", "", ""), "Release"); got != base {
		t.Errorf("not deterministic: %q vs %q", got, base)
	}
	for name, k := range map[string]string{
		"other checkout": testKey(req("/w/b", "", ""), "Release"),
		"build type":     testKey(req("/w/a", "", ""), "Debug"),
		"cxxflags":       testKey(req("/w/a", "-fprofile-instr-generate", ""), "Release"),
		"ldflags":        testKey(req("/w/a", "", "-fprofile-instr-generate"), "Release"),
	} {
		if k == base {
			t.Errorf("%s must not share a test tree with the base run (both %q)", name, k)
		}
	}
	if base == "" {
		t.Fatal("empty key")
	}
}
