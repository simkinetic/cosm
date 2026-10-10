// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"cosm/internal/types"
)

func TestNearKey(t *testing.T) {
	for k, want := range map[string]string{
		"licence":  "license", // British spelling
		"dpes":     "deps",    // transposition
		"testdeps": "",        // case-variant is consumed by json, never reaches here
		"provide":  "provides",
		"homepage": "",
		"url":      "", // short keys need an exact one-edit miss
	} {
		if got := nearKey(k); got != want {
			t.Errorf("nearKey(%q) = %q, want %q", k, got, want)
		}
	}
}

func TestUnknownKeysNote(t *testing.T) {
	var m types.Manifest
	if err := json.Unmarshal([]byte(`{"name":"x","licence":"MIT","homepage":"h"}`), &m); err != nil {
		t.Fatal(err)
	}
	note := unknownKeysNote(&m)
	if !strings.Contains(note, "homepage, licence") || !strings.Contains(note, `"licence" looks like a misspelling of "license"`) {
		t.Errorf("note = %q", note)
	}
	if strings.Contains(note, `"homepage" looks like`) {
		t.Errorf("homepage is not a typo: %q", note)
	}
	if unknownKeysNote(&types.Manifest{Name: "x"}) != "" {
		t.Error("no unknown keys -> no note")
	}
}
