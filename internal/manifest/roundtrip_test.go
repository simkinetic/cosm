// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/types"
)

// TestManifestPreservesUnknownKeys: release/upgrade/add load cosm.json and write it
// back. Keys cosm doesn't model used to vanish on that rewrite (a "license" field was
// silently dropped at release). Now license is modelled and anything else survives.
func TestManifestPreservesUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cosm.json")
	in := `{
  "schemaVersion": 1,
  "name": "lib",
  "uuid": "u-lib",
  "version": "v0.1.0",
  "license": "MIT",
  "licence": "typo-kept",
  "homepage": "https://example.org",
  "metadata": {"team": "kinetics", "tags": ["a", "b"]}
}
`
	if err := os.WriteFile(p, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.License != "MIT" {
		t.Errorf("license = %q, want MIT", m.License)
	}
	if got := strings.Join(m.UnknownKeys(), ","); got != "homepage,licence,metadata" {
		t.Errorf("unknown keys = %q", got)
	}

	// The release path: mutate a modelled field, save, reload.
	m.Version = "v0.2.0"
	if err := SaveManifest(p, m); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	for _, want := range []string{`"license": "MIT"`, `"licence": "typo-kept"`, `"homepage": "https://example.org"`, `"team": "kinetics"`, `"version": "v0.2.0"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("rewritten cosm.json lost %s:\n%s", want, out)
		}
	}
	// Modelled fields keep their usual position: name before any extra key.
	if strings.Index(string(out), `"name"`) > strings.Index(string(out), `"homepage"`) {
		t.Errorf("extras should follow the modelled fields:\n%s", out)
	}
	// Stable: a second save is byte-identical.
	m2, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(p, m2); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(p); string(again) != string(out) {
		t.Errorf("save is not stable:\n%s\nvs\n%s", out, again)
	}
}

// TestManifestCaseVariantKeyNotDuplicated: encoding/json fills fields
// case-insensitively, so "Version" is the version — it must not also be preserved
// as an extra key, or the rewrite would carry a stale copy that wins on reload.
func TestManifestCaseVariantKeyNotDuplicated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cosm.json")
	if err := os.WriteFile(p, []byte(`{"schemaVersion":1,"name":"lib","uuid":"u","Version":"v0.1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "v0.1.0" || len(m.UnknownKeys()) != 0 {
		t.Fatalf("version = %q, unknown = %v", m.Version, m.UnknownKeys())
	}
	m.Version = "v0.2.0"
	if err := SaveManifest(p, m); err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != "v0.2.0" {
		t.Fatalf("stale case-variant key won on reload: version = %q", again.Version)
	}
}

// TestSaveEnrollmentIgnoresCosmDir: `cosm develop` writes .cosm/develop.json, often
// before anything else in the project. Without a .cosm/.gitignore at that point, a
// `git add -A` committed the developer's enrollment into the release.
func TestSaveEnrollmentIgnoresCosmDir(t *testing.T) {
	proj := t.TempDir()
	p := filepath.Join(proj, ".cosm", "develop.json")
	if err := SaveEnrollment(p, &types.Enrollment{Enrolled: []string{"u@v0"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(proj, ".cosm", ".gitignore"))
	if err != nil || string(b) != "*\n" {
		t.Fatalf(".cosm/.gitignore = %q, %v", b, err)
	}
	// A user's own .gitignore there is left alone.
	if err := os.WriteFile(filepath.Join(proj, ".cosm", ".gitignore"), []byte("custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveEnrollment(p, &types.Enrollment{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(proj, ".cosm", ".gitignore")); string(b) != "custom\n" {
		t.Errorf("overwrote a custom .gitignore: %q", b)
	}
}
