// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package buildkey

import (
	"strings"
	"testing"

	"cosm/internal/types"
)

func base() Input {
	return Input{
		Tree:        "sha256:aaa",
		Platform:    types.Platform{OS: "darwin", Arch: "arm64"},
		ToolchainID: "clang-17",
		Config:      `{"buildType":"Release"}`,
		ExtID:       "cmake",
		ExtVersion:  "0.1.0",
		DepKeys:     []string{"sha256:dep1", "sha256:dep2"},
	}
}

func TestDeterministic(t *testing.T) {
	if Compute(base()) != Compute(base()) {
		t.Fatal("build key not deterministic")
	}
	if !strings.HasPrefix(Compute(base()), "sha256:") {
		t.Fatal("missing sha256 prefix")
	}
}

func TestDepOrderIndependent(t *testing.T) {
	a := base()
	b := base()
	b.DepKeys = []string{"sha256:dep2", "sha256:dep1"}
	if Compute(a) != Compute(b) {
		t.Fatal("build key should not depend on dep order")
	}
}

func TestInvalidationOnChange(t *testing.T) {
	orig := Compute(base())
	changed := []func(*Input){
		func(i *Input) { i.Tree = "sha256:bbb" },
		func(i *Input) { i.Platform.Arch = "amd64" },
		func(i *Input) { i.ToolchainID = "gcc-13" },
		func(i *Input) { i.Config = `{"buildType":"Debug"}` },
		func(i *Input) { i.ExtVersion = "0.2.0" },
		func(i *Input) { i.DepKeys = []string{"sha256:dep1", "sha256:dep3"} },
	}
	for idx, mut := range changed {
		in := base()
		mut(&in)
		if Compute(in) == orig {
			t.Errorf("change #%d did not invalidate the build key", idx)
		}
	}
}
