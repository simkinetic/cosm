// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cosm/internal/errs"
	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// stackRepo makes a git repo for a package at version with deps, optionally tagged
// (released) and with an origin remote.
func stackRepo(t *testing.T, name, version string, deps map[string]types.Dependency, tag, origin bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := manifest.SaveManifest(filepath.Join(dir, "cosm.json"), &types.Manifest{
		Name: name, UUID: "u-" + name, Version: version, Build: "lua", Deps: deps,
	}); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "init")
	if tag {
		gitT(t, dir, "tag", version)
	}
	if origin {
		bare := t.TempDir()
		gitT(t, bare, "init", "-q", "--bare")
		gitT(t, dir, "remote", "add", "origin", bare)
	}
	return dir
}

func dep(name, version string) map[string]types.Dependency {
	mj, _ := semver.Major(version)
	return map[string]types.Dependency{semver.UnitKey("u-"+name, mj): {Name: name, Version: version}}
}

func merge(ms ...map[string]types.Dependency) map[string]types.Dependency {
	out := map[string]types.Dependency{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// TestPlanChain: the bottom-up plan for top -> mid -> {base, util}. base has a
// commit since its release; util is unchanged but mid declares less than util's
// current release (it was developed against newer code than it requires); top was
// never released and has no origin. Releasing base forces mid to raise its floor,
// which forces a release of mid, which forces top's raise: change propagates up.
func TestPlanChain(t *testing.T) {
	_, _ = releaseRepo(t, false, nil) // sets git identity + COSM_OFFLINE for this test
	d := seedDepot(t)
	seedPkg(t, d, "R", "util", "u-util", mkSpec("util", "u-util", "v0.1.0"), mkSpec("util", "u-util", "v0.2.0"))
	seedPkg(t, d, "R", "mid", "u-mid", mkSpec("mid", "u-mid", "v0.1.0"))
	seedPkg(t, d, "R", "base", "u-base", mkSpec("base", "u-base", "v0.1.0"))
	s := New(d, gitx.Exec{})

	util := stackRepo(t, "util", "v0.2.0", nil, true, true)
	base := stackRepo(t, "base", "v0.1.0", nil, true, true)
	_ = os.WriteFile(filepath.Join(base, "new.lua"), []byte("x"), 0o644)
	gitT(t, base, "add", "-A")
	gitT(t, base, "commit", "-q", "-m", "improve")
	mid := stackRepo(t, "mid", "v0.1.0", merge(dep("base", "v0.1.0"), dep("util", "v0.1.0")), true, true)
	top := stackRepo(t, "top", "v0.1.0", dep("mid", "v0.1.0"), false, false)
	for name, dir := range map[string]string{"util": util, "base": base, "mid": mid} {
		if _, err := s.Develop(top, name, -1, "", "", dir); err != nil {
			t.Fatal(err)
		}
	}

	p, err := s.PlanChain(top, ReleaseOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	steps := map[string]ChainStep{}
	for _, st := range p.Steps {
		order = append(order, st.Name)
		steps[st.Name] = st
	}
	if strings.Join(order, ",") != "base,util,mid,top" {
		t.Fatalf("order = %v, want dependencies first", order)
	}
	if b := steps["base"]; !b.Release || b.To != "v0.1.1" || !strings.Contains(strings.Join(b.Reasons, ";"), "1 commit(s) since v0.1.0") {
		t.Errorf("base: %+v", b)
	}
	if u := steps["util"]; u.Release || u.Register {
		t.Errorf("util is released, registered and unchanged: %+v", u)
	}
	m := steps["mid"]
	if !m.Release || m.To != "v0.1.1" || len(m.Raises) != 2 ||
		m.Raises[0] != (FloorRaise{"base", "v0.1.0", "v0.1.1"}) || m.Raises[1] != (FloorRaise{"util", "v0.1.0", "v0.2.0"}) {
		t.Errorf("mid should raise base to its new release and util to its current one: %+v", m)
	}
	tp := steps["top"]
	if !tp.Release || !tp.FirstRelease || tp.To != "v0.1.0" || len(tp.Raises) != 1 || tp.Raises[0].To != "v0.1.1" {
		t.Errorf("top: %+v", tp)
	}
	if !strings.Contains(strings.Join(tp.Problems, ";"), "no 'origin' remote") {
		t.Errorf("top has no origin; want that flagged: %v", tp.Problems)
	}

	// The bump applies to every released package.
	if p, err = s.PlanChain(top, ReleaseOpts{Minor: true}); err != nil || p.Steps[0].To != "v0.2.0" || p.Bump != "--minor" {
		t.Errorf("--minor: %+v %v", p, err)
	}
	if _, err := s.PlanChain(top, ReleaseOpts{Version: "v1.0.0"}); !errors.Is(err, errs.ErrUsage) {
		t.Errorf("an explicit version can't apply to a whole stack: %v", err)
	}
	// Planning changed nothing.
	if out := gitT(t, base, "tag"); out != "v0.1.0" {
		t.Errorf("planning created tags: %q", out)
	}
}
