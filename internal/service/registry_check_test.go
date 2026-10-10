// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"errors"
	"strings"
	"testing"

	"cosm/internal/errs"
	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/registry"
)

// TestCheckRegistries: every registered version must resolve inside the
// registries. The field case: rte v0.1.0 still requires a removed macro v0.1.0,
// while rte v0.1.1 has moved on. One level down, x's direct dependency exists but
// its own requirement doesn't.
func TestCheckRegistries(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "macro", "u-macro", mkSpec("macro", "u-macro", "v0.2.0"))
	seedPkg(t, d, "R", "rte", "u-rte",
		mkSpec("rte", "u-rte", "v0.1.0", [3]string{"macro", "u-macro", "v0.1.0"}),
		mkSpec("rte", "u-rte", "v0.1.1", [3]string{"macro", "u-macro", "v0.2.0"}))
	seedPkg(t, d, "R", "app", "u-app", mkSpec("app", "u-app", "v1.0.0", [3]string{"rte", "u-rte", "v0.1.1"}))
	s := New(d, gitx.Exec{})

	res, err := s.CheckRegistries("", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Versions != 4 || res.Packages != 3 {
		t.Errorf("checked %d versions of %d packages, want 4 of 3", res.Versions, res.Packages)
	}
	if len(res.Problems) != 1 {
		t.Fatalf("want exactly rte v0.1.0 to fail, got %+v", res.Problems)
	}
	p := res.Problems[0]
	if p.Package != "rte" || p.Version != "v0.1.0" || !errors.Is(p.Err, errs.ErrPackageNotFound) ||
		!strings.Contains(p.Err.Error(), "requires macro v0.1.0") || !strings.Contains(p.Err.Error(), "registered: v0.2.0") {
		t.Errorf("problem = %+v", p)
	}

	// --latest skips superseded versions: rte v0.1.1 resolves.
	if res, err = s.CheckRegistries("R", true); err != nil || len(res.Problems) != 0 || res.Versions != 3 {
		t.Errorf("--latest: %+v %v", res, err)
	}

	// A broken requirement one level down fails both the version that has it and
	// the one depending on it.
	seedPkg(t, d, "R", "y", "u-y", mkSpec("y", "u-y", "v1.0.0", [3]string{"z", "u-z", "v1.0.0"}))
	seedPkg(t, d, "R", "x", "u-x", mkSpec("x", "u-x", "v1.0.0", [3]string{"y", "u-y", "v1.0.0"}))
	res, _ = s.CheckRegistries("R", true)
	got := map[string]string{}
	for _, p := range res.Problems {
		got[p.Package] = p.Err.Error()
	}
	if !strings.Contains(got["y"], "requires z v1.0.0") || !strings.Contains(got["x"], "closure doesn't resolve") {
		t.Errorf("transitive failure not reported for both: %v", got)
	}

	if _, err := s.CheckRegistries("nope", false); !errors.Is(err, errs.ErrRegistryNotFound) {
		t.Errorf("unknown registry: %v", err)
	}
}

// TestCheckRegistriesUUIDMismatch: a specs.json whose uuid disagrees with the
// registry index is an integrity problem, not a pass.
func TestCheckRegistriesUUIDMismatch(t *testing.T) {
	d := seedDepot(t)
	seedPkg(t, d, "R", "p", "u-p", mkSpec("p", "u-p", "v1.0.0"))
	sp := mkSpec("p", "u-OTHER", "v1.0.0")
	if err := manifest.SaveSpecs(registry.SpecsFile(d.Registry("R"), "p", "v1.0.0"), &sp); err != nil {
		t.Fatal(err)
	}
	res, err := New(d, gitx.Exec{}).CheckRegistries("R", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || !errors.Is(res.Problems[0].Err, errs.ErrIntegrityMismatch) {
		t.Fatalf("want an integrity problem, got %+v", res.Problems)
	}
}
