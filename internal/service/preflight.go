// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/registry"
	"cosm/internal/resolve"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// ReleaseCheck is one preflight check: what was checked, and the problem when it
// failed (nil when it passed).
type ReleaseCheck struct {
	Label string
	Err   error
}

// ReleasePlan is what `cosm release` will do, and what it checked first. Planning
// changes nothing, so `--dry-run` is just a plan that is printed and not executed.
type ReleasePlan struct {
	Name, UUID string
	From, To   string // current and new version
	Branch     string
	Remote     string // origin's URL; "" when there is none
	Checks     []ReleaseCheck
	Warnings   []string
	// Released is cosm.json as it will be committed (version set to To).
	Released *types.Manifest
	// Registries lists the registries that already know this package.
	Registries []string
}

// Problems returns the failed checks' errors, in check order.
func (p *ReleasePlan) Problems() []error {
	var out []error
	for _, c := range p.Checks {
		if c.Err != nil {
			out = append(out, c.Err)
		}
	}
	return out
}

// PreflightError reports every problem the release preflight found, not just the
// first. It unwraps to each of them, so errors.Is (and the exit code) still see
// the underlying kinds.
type PreflightError struct{ Problems []error }

func (e *PreflightError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "release preflight found %d problems; nothing was changed:", len(e.Problems))
	for _, p := range e.Problems {
		fmt.Fprintf(&b, "\n  - %v", p)
	}
	return b.String()
}

func (e *PreflightError) Unwrap() []error { return e.Problems }

// PlanRelease runs the release preflight and describes the release, without
// changing anything. All checks run even after one fails, so a single run lists
// every problem. It checks that:
//
//   - the worktree is clean and HEAD is on a branch;
//   - the new tag is free, locally and on origin;
//   - an origin remote exists, is reachable, and the branch isn't behind it —
//     checked up front, because pushing is the last step and a failure there used
//     to leave the version commit and tag behind locally;
//   - every declared dependency minimum (deps and testDeps) is in a registry, and
//     the whole closure resolves from the registries alone. Develop overlays and
//     --source never apply: consumers resolve the release from registries, so that
//     is what has to work.
func (s *Service) PlanRelease(projectDir string, opts ReleaseOpts) (*ReleasePlan, error) {
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return nil, err
	}
	newVer, err := nextVersion(m.Version, opts)
	if err != nil {
		return nil, err
	}
	if err := semver.ValidateExact(newVer); err != nil {
		return nil, fmt.Errorf("%w: %v", errs.ErrUsage, err)
	}
	if semver.Compare(newVer, m.Version) < 0 {
		return nil, fmt.Errorf("%w: new version %s is below current %s", errs.ErrUsage, newVer, m.Version)
	}
	p := &ReleasePlan{Name: m.Name, UUID: m.UUID, From: m.Version, To: newVer}
	check := func(label string, err error) { p.Checks = append(p.Checks, ReleaseCheck{label, err}) }

	// The tree.
	switch status, err := s.Git.StatusPorcelain(projectDir); {
	case err != nil:
		check("worktree is clean", err)
	case status != "":
		check("worktree is clean", fmt.Errorf("%w: commit or stash changes before releasing:\n%s",
			errs.ErrDirtyWorktree, indentLines(firstLines(status, 8), "  ")))
	default:
		check("worktree is clean", nil)
	}
	if branch, err := s.Git.CurrentBranch(projectDir); err != nil || branch == "" || branch == "HEAD" {
		check("HEAD is on a branch", fmt.Errorf("%w: HEAD is detached; check out the branch to release", errs.ErrUsage))
	} else {
		p.Branch = branch
		check("on branch "+branch, nil)
	}
	tags, err := s.Git.ListTags(projectDir)
	if err != nil {
		return nil, err
	}
	if containsStr(tags, newVer) {
		check("tag "+newVer+" is free", fmt.Errorf("%w: tag %s exists", errs.ErrVersionExists, newVer))
	} else {
		check("tag "+newVer+" is free", nil)
	}

	// The remote. Pushing comes last, so check it before anything is changed.
	url, rerr := s.Git.Run(projectDir, "remote", "get-url", "origin")
	url = strings.TrimSpace(url)
	if rerr != nil || url == "" {
		check("an origin remote exists", fmt.Errorf("%w: no 'origin' remote; cosm release pushes the release "+
			"branch and tag there. Add it with: git remote add origin <url>", errs.ErrUsage))
	} else {
		p.Remote = url
		check("origin is "+url, nil)
		if ferr := s.Git.Fetch(projectDir, "origin"); ferr != nil {
			check("origin is reachable", fmt.Errorf("%w: can't fetch from origin (%s): %v", errs.ErrNetwork, url, ferr))
		} else {
			check("origin is reachable", nil)
			if p.Branch != "" {
				if behind, berr := s.Git.BehindCount(projectDir, p.Branch); berr == nil && behind > 0 {
					check("in sync with origin/"+p.Branch, fmt.Errorf("%w: pull %d commits from origin/%s first", errs.ErrNotInSync, behind, p.Branch))
				} else {
					check("in sync with origin/"+p.Branch, nil)
				}
			}
			if rtags, lerr := s.Git.LsRemoteTags(url); lerr == nil && containsStr(rtags, newVer) {
				check("tag "+newVer+" is free on origin", fmt.Errorf("%w: tag %s already exists on origin", errs.ErrVersionExists, newVer))
			}
		}
	}

	// The dependencies, as consumers will see them.
	rbl, rerrDeps := s.checkReleaseDeps(p, m)
	if rerrDeps == nil {
		s.warnDevelopedDeps(p, projectDir, m, rbl)
	}

	released := *m
	released.Version = newVer
	p.Released = &released
	p.Registries = s.registriesListing(m.UUID)
	return p, nil
}

// checkReleaseDeps adds a check per declared dependency (its minimum must be in a
// registry), then one for the whole closure resolved from registries alone. A miss
// may only mean a stale local clone, so it pulls the registries once (read-only,
// skipped under COSM_OFFLINE) before calling it a problem. Returns the
// registry-only build list when the closure resolves.
func (s *Service) checkReleaseDeps(p *ReleasePlan, m *types.Manifest) (types.BuildList, error) {
	type decl struct {
		key  string
		dep  types.Dependency
		test bool
	}
	var decls []decl
	for k, d := range m.Deps {
		decls = append(decls, decl{k, d, false})
	}
	for k, d := range m.TestDeps {
		decls = append(decls, decl{k, d, true})
	}
	sort.Slice(decls, func(i, j int) bool {
		if decls[i].test != decls[j].test {
			return !decls[i].test
		}
		return decls[i].dep.Name < decls[j].dep.Name
	})
	published := func(x decl) bool {
		uuid, _, _ := semver.SplitUnitKey(x.key)
		_, err := s.Loader.Load(x.dep.Name, uuid, x.dep.Version)
		return !errors.Is(err, errs.ErrPackageNotFound)
	}
	missing := map[string]bool{}
	for _, x := range decls {
		if !published(x) {
			missing[x.key] = true
		}
	}
	if len(missing) > 0 && os.Getenv("COSM_OFFLINE") == "" {
		s.Reg.UpdateAll()
		for _, x := range decls {
			if missing[x.key] && published(x) {
				delete(missing, x.key)
			}
		}
	}
	for _, x := range decls {
		kind := "dependency"
		if x.test {
			kind = "test dependency"
		}
		label := fmt.Sprintf("%s %s %s is in a registry", kind, x.dep.Name, x.dep.Version)
		if !missing[x.key] {
			p.Checks = append(p.Checks, ReleaseCheck{Label: label})
			uuid, _, _ := semver.SplitUnitKey(x.key)
			if reason, y := s.Loader.Yanked(x.dep.Name, uuid, x.dep.Version); y {
				p.Warnings = append(p.Warnings, fmt.Sprintf("%s %s: its minimum %s is yanked (%s); consumers will be warned. "+
					"Raise it with: cosm upgrade %s", kind, x.dep.Name, x.dep.Version, reason, x.dep.Name))
			}
			continue
		}
		who := "consumers can't resolve this release"
		if x.test {
			who = "the release's own tests can't be built from the registries"
		}
		hint := "It isn't published at all: release and register it first."
		if _, _, vers, err := s.Loader.SelectableVersions(x.dep.Name); err == nil && len(vers) > 0 {
			hint = fmt.Sprintf("Published: %s. Raise the minimum with: cosm upgrade %s",
				strings.Join(vers, ", "), x.dep.Name)
		}
		p.Checks = append(p.Checks, ReleaseCheck{label, kindf(errs.ErrPackageNotFound, "%s %s: its declared minimum %s is in no registry, so %s. %s",
			kind, x.dep.Name, x.dep.Version, who, hint)})
	}
	if len(missing) > 0 {
		return types.BuildList{}, errors.New("declared dependencies missing")
	}
	// The whole closure, registry-only: a released dependency's own requirements
	// must resolve too.
	const label = "the full dependency closure resolves from the registries alone"
	bl, _, err := resolve.ResolveWithTests(m, s.Loader, resolve.NoDev)
	if err != nil {
		p.Checks = append(p.Checks, ReleaseCheck{label, fmt.Errorf("%w (resolved from the registries only, as consumers will)", err)})
		return types.BuildList{}, err
	}
	p.Checks = append(p.Checks, ReleaseCheck{Label: label})
	return bl, nil
}

// warnDevelopedDeps flags dependencies this project is building against a live
// develop checkout: the release resolves them from the registries instead, so what
// was tested here may not be what consumers get.
func (s *Service) warnDevelopedDeps(p *ReleasePlan, projectDir string, m *types.Manifest, released types.BuildList) {
	_, local, _, err := s.ResolveWithTests(projectDir)
	if err != nil {
		return
	}
	var names []string
	for key, e := range local.Dependencies {
		if !e.Develop || e.UUID == m.UUID {
			continue
		}
		reg := "a registry version"
		if r, ok := released.Dependencies[key]; ok {
			reg = r.Version
		}
		names = append(names, fmt.Sprintf("%s is enrolled for development here, but the release resolves it from the "+
			"registries (%s), not your checkout; make sure that is what you built and tested against", e.Name, reg))
	}
	sort.Strings(names)
	p.Warnings = append(p.Warnings, names...)
}

// registriesListing returns the local registries whose index lists uuid.
func (s *Service) registriesListing(uuid string) []string {
	refs, _ := s.Reg.ListRegistries()
	var out []string
	for _, r := range refs {
		reg, err := manifest.LoadRegistry(registry.MetaFile(s.D.Registry(r.Name)))
		if err != nil {
			continue
		}
		for _, pkg := range reg.Packages {
			if pkg.UUID == uuid {
				out = append(out, r.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("… and %d more", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

func indentLines(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// kindErr carries an error kind (for errors.Is and the exit code) with a message
// that reads on its own, instead of prefixing the kind's text to a sentence that
// already says it ("not found in any registry: …, which is in no registry").
type kindErr struct {
	kind error
	msg  string
}

func (e *kindErr) Error() string { return e.msg }
func (e *kindErr) Unwrap() error { return e.kind }

func kindf(kind error, format string, args ...any) error {
	return &kindErr{kind, fmt.Sprintf(format, args...)}
}

// KindError is kindf for callers outside the package.
func KindError(kind error, msg string) error { return &kindErr{kind, msg} }
