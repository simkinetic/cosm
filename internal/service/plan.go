// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// FloorRaise is a dependency minimum a consumer must raise before its release.
type FloorRaise struct{ Dep, From, To string }

// ChainStep is one package of a co-developed stack in a release plan.
type ChainStep struct {
	Name, UUID, Dir string
	From, To        string // To == From when it needs no release
	Release         bool   // needs `cosm release`
	Register        bool   // its To version must (then) be registered
	FirstRelease    bool   // never released: released at its current version
	Reasons         []string
	Raises          []FloorRaise
	Registries      []string // registries already listing it
	Remote          string
	Problems        []string // to fix before this step can run
}

// ChainPlan is the bottom-up release order of the stack under a project.
type ChainPlan struct {
	Root  string
	Bump  string // "--patch" / "--minor" / "--major"
	Steps []ChainStep
}

// PlanChain works out how to release the co-developed stack under projectDir: the
// project plus every dependency it builds from a develop checkout. It reads each
// checkout and its git state and changes nothing. In dependency order (deps
// first), a package needs a release when it has commits since its release tag,
// uncommitted changes, or was never released — or when it must raise a
// dependency minimum: to a dependency's new release, or because it declares less
// than that dependency's current release (it was developed against newer code than
// it requires). So a change anywhere propagates up to every consumer in the stack.
func (s *Service) PlanChain(projectDir string, opts ReleaseOpts) (*ChainPlan, error) {
	if opts.Version != "" {
		return nil, fmt.Errorf("%w: --plan covers several packages; use --patch/--minor/--major, not a version", errs.ErrUsage)
	}
	bump := "--patch"
	switch {
	case opts.Major:
		bump = "--major"
	case opts.Minor:
		bump = "--minor"
	default:
		opts.Patch = true
	}
	root, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return nil, err
	}
	_, bl, _, err := s.ResolveWithTests(projectDir)
	if err != nil {
		return nil, err
	}

	// The set: the project and every dependency built from a checkout.
	type unit struct {
		dir string
		man *types.Manifest
	}
	units := map[string]unit{}
	rootMajor, err := semver.Major(root.Version)
	if err != nil {
		return nil, fmt.Errorf("%w: project version %q: %v", errs.ErrUsage, root.Version, err)
	}
	rootKey := semver.UnitKey(root.UUID, rootMajor)
	units[rootKey] = unit{projectDir, root}
	for key, e := range bl.Dependencies {
		if !e.Develop || e.SourcePath == "" || key == rootKey {
			continue
		}
		m, err := manifest.LoadManifestFromDir(e.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name, err)
		}
		units[key] = unit{e.SourcePath, m}
	}
	depsIn := func(m *types.Manifest) map[string]string { // in-set dep key -> declared minimum
		out := map[string]string{}
		for k, d := range m.Deps {
			if _, ok := units[k]; ok {
				out[k] = d.Version
			}
		}
		for k, d := range m.TestDeps {
			if _, ok := units[k]; ok {
				out[k] = d.Version
			}
		}
		return out
	}

	order, err := topoUnits(units, func(k string) []string {
		var ks []string
		for d := range depsIn(units[k].man) {
			ks = append(ks, d)
		}
		return ks
	}, func(k string) string { return units[k].man.Name })
	if err != nil {
		return nil, err
	}

	plan := &ChainPlan{Root: root.Name, Bump: bump}
	target := map[string]string{}
	for _, key := range order {
		u := units[key]
		st := ChainStep{Name: u.man.Name, UUID: u.man.UUID, Dir: u.dir, From: u.man.Version, To: u.man.Version}
		tags, terr := s.Git.ListTags(u.dir)
		if terr != nil {
			st.Problems = append(st.Problems, "not a git repository with tags: "+terr.Error())
		}
		released := containsStr(tags, u.man.Version)
		if terr == nil && !released {
			st.FirstRelease = true
			st.Reasons = append(st.Reasons, "never released (no tag "+u.man.Version+")")
		}
		if released {
			if out, err := s.Git.Run(u.dir, "rev-list", "--count", u.man.Version+"..HEAD"); err == nil {
				if n, _ := strconv.Atoi(strings.TrimSpace(out)); n > 0 {
					st.Reasons = append(st.Reasons, fmt.Sprintf("%d commit(s) since %s", n, u.man.Version))
				}
			}
		}
		if status, err := s.Git.StatusPorcelain(u.dir); err == nil && status != "" {
			st.Reasons = append(st.Reasons, "uncommitted changes")
			st.Problems = append(st.Problems, "uncommitted changes: commit them first (they belong in the release)")
		}
		deps := depsIn(u.man)
		var depKeys []string
		for d := range deps {
			depKeys = append(depKeys, d)
		}
		sort.Slice(depKeys, func(i, j int) bool { return units[depKeys[i]].man.Name < units[depKeys[j]].man.Name })
		for _, d := range depKeys {
			if want := target[d]; semver.Compare(deps[d], want) < 0 {
				st.Raises = append(st.Raises, FloorRaise{Dep: units[d].man.Name, From: deps[d], To: want})
			}
		}
		st.Release = len(st.Reasons) > 0 || len(st.Raises) > 0
		if st.Release && !st.FirstRelease {
			if next, err := nextVersion(u.man.Version, opts); err == nil {
				st.To = next
			}
		}
		if st.Release {
			st.Register = true
		} else if _, err := s.Loader.Load(u.man.Name, u.man.UUID, u.man.Version); errors.Is(err, errs.ErrPackageNotFound) {
			st.Register = true
			st.Reasons = append(st.Reasons, "tag "+u.man.Version+" exists but isn't registered")
		}
		if url, err := s.Git.Run(u.dir, "remote", "get-url", "origin"); err == nil && strings.TrimSpace(url) != "" {
			st.Remote = strings.TrimSpace(url)
		} else if st.Release {
			st.Problems = append(st.Problems, "no 'origin' remote: add one with git remote add origin <url>")
		}
		if b, err := s.Git.CurrentBranch(u.dir); st.Release && (err != nil || b == "" || b == "HEAD") {
			st.Problems = append(st.Problems, "HEAD is detached: check out the branch to release")
		}
		st.Registries = s.registriesListing(u.man.UUID)
		target[key] = st.To
		plan.Steps = append(plan.Steps, st)
	}
	return plan, nil
}

// topoUnits orders keys dependencies-first, breaking ties by name for a stable plan.
func topoUnits[T any](units map[string]T, deps func(string) []string, name func(string) string) ([]string, error) {
	indeg := map[string]int{}
	users := map[string][]string{}
	for k := range units {
		ds := deps(k)
		indeg[k] = len(ds)
		for _, d := range ds {
			users[d] = append(users[d], k)
		}
	}
	var ready, order []string
	for k, n := range indeg {
		if n == 0 {
			ready = append(ready, k)
		}
	}
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return name(ready[i]) < name(ready[j]) })
		k := ready[0]
		ready = ready[1:]
		order = append(order, k)
		for _, u := range users[k] {
			if indeg[u]--; indeg[u] == 0 {
				ready = append(ready, u)
			}
		}
	}
	if len(order) != len(units) {
		return nil, fmt.Errorf("%w: the co-developed packages depend on each other in a cycle", errs.ErrUsage)
	}
	return order, nil
}
