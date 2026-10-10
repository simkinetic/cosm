// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

// Package service composes the depot, git, registry, and resolver into the
// project-level operations the CLI drives (§12).
package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"cosm/internal/depot"
	"cosm/internal/develop"
	"cosm/internal/errs"
	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/registry"
	"cosm/internal/resolve"
	"cosm/internal/semver"
	"cosm/internal/types"
)

type Service struct {
	D      depot.Depot
	Git    gitx.Git
	Loader *registry.Loader
	Reg    *registry.Service
}

func New(d depot.Depot, g gitx.Git) *Service {
	return &Service{D: d, Git: g, Loader: registry.NewLoader(d), Reg: registry.NewService(d, g)}
}

// ResolveOption tunes a resolve call.
type ResolveOption func(*resolveCfg)

type resolveCfg struct {
	sources []string // per-invocation source overrides (see WithSources)
}

// WithSources redirects each named checkout's unit to that directory for this
// invocation, overriding the shared develop workspace. Use it to build/test a git
// worktree instead of the one checkout the workspace can hold (§7.4).
func WithSources(dirs []string) ResolveOption {
	return func(c *resolveCfg) { c.sources = append(c.sources, dirs...) }
}

// Resolve loads a project's cosm.json and computes its build list, applying any
// develop overrides enrolled by the project (§7).
func (s *Service) Resolve(projectDir string, opts ...ResolveOption) (*types.Manifest, types.BuildList, []resolve.Warning, error) {
	return s.resolve(projectDir, false, opts...)
}

// ResolveWithTests is Resolve but also includes the project's test-only
// dependencies (§7.6) — used by `cosm test`.
func (s *Service) ResolveWithTests(projectDir string, opts ...ResolveOption) (*types.Manifest, types.BuildList, []resolve.Warning, error) {
	return s.resolve(projectDir, true, opts...)
}

func (s *Service) resolve(projectDir string, includeTests bool, opts ...ResolveOption) (*types.Manifest, types.BuildList, []resolve.Warning, error) {
	var cfg resolveCfg
	for _, o := range opts {
		o(&cfg)
	}
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return nil, types.BuildList{}, nil, err
	}
	ov, missing, err := develop.Build(s.D, projectDir)
	if err != nil {
		return nil, types.BuildList{}, nil, err
	}
	// The project you are standing in always wins over a workspace checkout of the
	// same unit, so `cosm test` in a worktree tests *that* worktree. Best-effort:
	// the manifest already loaded, so a failure here only means an odd version.
	_, _ = ov.AddSource(projectDir)
	for _, dir := range cfg.sources {
		if _, err := ov.AddSource(dir); err != nil {
			return nil, types.BuildList{}, nil, fmt.Errorf("%w: --source %s: %v", errs.ErrUsage, dir, err)
		}
	}
	var bl types.BuildList
	var warns []resolve.Warning
	if includeTests {
		bl, warns, err = resolve.ResolveWithTests(m, s.Loader, ov)
	} else {
		bl, warns, err = resolve.Resolve(m, s.Loader, ov)
	}
	if err == nil {
		warns = append(warns, s.developAdvisories(projectDir, m, bl, missing)...)
	} else {
		err = s.explainResolveError(err, projectDir, m, bl, missing)
	}
	return m, bl, warns, err
}

// explainResolveError adds what the co-development state says about a failed
// resolution. An unpublished, develop-only dependency has no registry release to
// fall back to, so both of the usual causes surface as "not found in any registry",
// which points the wrong way: a sibling sitting in the workspace that this project
// isn't enrolled for, and an enrolled checkout that is missing (often: the wrong
// depot). The original error stays wrapped, so the exit code is unchanged.
func (s *Service) explainResolveError(err error, projectDir string, m *types.Manifest, bl types.BuildList, missing []develop.Missing) error {
	enr, _ := manifest.LoadEnrollment(develop.EnrollmentPath(projectDir))
	var notes []string
	var nf *errs.PackageNotFoundError
	if errors.As(err, &nf) {
		if ws, werr := manifest.LoadWorkspace(s.D.WorkspaceFile()); werr == nil {
			for _, we := range ws.Entries {
				if we.Name == nf.Name && !containsStr(enr.Enrolled, semver.UnitKey(we.UUID, we.Major)) {
					notes = append(notes, fmt.Sprintf("%s is in the develop workspace of depot %s, but this project isn't enrolled for it. "+
						"Run 'cosm develop %s', or 'cosm develop --all' to enroll the whole co-developed stack", we.Name, s.D.Root, we.Name))
				}
			}
		}
	}
	for _, mi := range missing {
		name := s.missingName(mi, m, bl, enr)
		notes = append(notes, fmt.Sprintf("%s is enrolled for development but %s", name, s.missingWhere(mi, name)))
	}
	if len(notes) == 0 {
		return err
	}
	sort.Strings(notes)
	tail := ""
	if len(missing) > 0 {
		tail = "\n  Run 'cosm develop <name>' to restore a checkout, or check that --depot/$COSM_DEPOT name the depot you expect"
	}
	return fmt.Errorf("%w\n  note: %s%s", err, strings.Join(notes, "\n  note: "), tail)
}

// developAdvisories surfaces the two easy-to-miss develop states, so build/test/
// run/env/status report the same thing: a dependency sitting in the shared
// workspace that this project isn't developing (silently using the registry
// version), and a unit this project IS enrolled for whose checkout is missing (so
// resolution fell back to the registry). Sorted for deterministic output.
func (s *Service) developAdvisories(projectDir string, m *types.Manifest, bl types.BuildList, missing []develop.Missing) []resolve.Warning {
	ws, _ := manifest.LoadWorkspace(s.D.WorkspaceFile())
	enr, _ := manifest.LoadEnrollment(develop.EnrollmentPath(projectDir))
	enrolled := map[string]bool{}
	for _, k := range enr.Enrolled {
		enrolled[k] = true
	}
	inWS := map[string]types.WorkspaceEntry{}
	for _, e := range ws.Entries {
		inWS[semver.UnitKey(e.UUID, e.Major)] = e
	}
	var warns []resolve.Warning
	for key, e := range bl.Dependencies {
		if e.Develop {
			continue
		}
		if _, ok := inWS[key]; ok && !enrolled[key] {
			warns = append(warns, resolve.Warning{
				Code: "W_DEVELOP_AVAILABLE",
				Message: fmt.Sprintf("%s@v%d is in the dev workspace but this project uses registry %s "+
					"(run 'cosm develop %s' to build against the live checkout)", e.Name, e.Major, e.Version, e.Name),
			})
		}
	}
	for _, mi := range missing {
		name := s.missingName(mi, m, bl, enr)
		warns = append(warns, resolve.Warning{
			Code: "W_DEVELOP_MISSING",
			Message: fmt.Sprintf("%s is enrolled for development but %s; using the registry version. "+
				"Run 'cosm develop %s' to restore it, or check that --depot/$COSM_DEPOT name the depot you expect",
				name, s.missingWhere(mi, name), name),
		})
	}
	sort.Slice(warns, func(i, j int) bool { return warns[i].Message < warns[j].Message })
	return warns
}

// missingName finds a readable name for an enrolled unit whose checkout is gone:
// from the workspace entry, the build list, the project's own declarations, the
// name recorded at enrollment, or a local registry clone. A transitive unit is
// in none of the first three when its checkout is missing, so without the last two
// it showed as a raw <uuid>@v<major>.
func (s *Service) missingName(mi develop.Missing, m *types.Manifest, bl types.BuildList, enr *types.Enrollment) string {
	if mi.Entry.Name != "" {
		return mi.Entry.Name
	}
	if e, ok := bl.Dependencies[mi.Key]; ok && e.Name != "" {
		return e.Name
	}
	if m != nil {
		if d, ok := m.Deps[mi.Key]; ok {
			return d.Name
		}
		if d, ok := m.TestDeps[mi.Key]; ok {
			return d.Name
		}
	}
	if enr != nil && enr.Names[mi.Key] != "" {
		return enr.Names[mi.Key]
	}
	if uuid, _, err := semver.SplitUnitKey(mi.Key); err == nil {
		if name, ok := s.Loader.NameByUUID(uuid); ok {
			return name
		}
	}
	return mi.Key
}

// missingWhere says exactly where cosm looked for a missing develop checkout. The
// usual cause of a "missing" checkout is running against a different depot than
// the one holding it, so the depot and the expected path are always named.
func (s *Service) missingWhere(mi develop.Missing, name string) string {
	if mi.InWS {
		return fmt.Sprintf("has no checkout at %s (depot %s)", s.D.DevUnit(mi.Entry.Name, mi.Entry.Major), s.D.Root)
	}
	if _, major, err := semver.SplitUnitKey(mi.Key); err == nil && name != mi.Key {
		return fmt.Sprintf("is not in the develop workspace of depot %s (expected at %s)", s.D.Root, s.D.DevUnit(name, major))
	}
	return fmt.Sprintf("is not in the develop workspace of depot %s", s.D.Root)
}

// RegistryChooser is called when a package is found in multiple registries.
type RegistryChooser func(name, version string, locs []registry.Location) (registry.Location, error)

// AddOpts narrows candidate selection non-interactively (a fast-path around the
// chooser prompt, for scripts/CI) and selects the dependency kind.
type AddOpts struct {
	Registry string // "" = any registry
	Major    int    // -1 = any major
	Test     bool   // add to testDeps (test-only, non-transitive) instead of deps
	Offline  bool   // don't pull registries on a local miss
}

// Add adds a dependency to the project's cosm.json (§12.2). When more than one
// candidate matches (multiple registries and/or majors), opts filters them; if a
// single candidate remains it is used without prompting, otherwise choose is
// called.
func (s *Service) Add(projectDir, name, version string, opts AddOpts, choose RegistryChooser) (chosenVersion, chosenRegistry string, err error) {
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return "", "", err
	}
	locs, err := s.findFiltered(name, version, opts)
	if err != nil {
		return "", "", err
	}
	if len(locs) == 0 {
		// An unpublished package adopted into the dev workspace
		// (`cosm develop <name> --path <dir>`). Source its identity locally (no net).
		if opts.Registry == "" {
			if v, ok, ferr := s.addFromWorkspace(m, projectDir, name, version, opts); ferr != nil {
				return "", "", ferr
			} else if ok {
				return v, "(develop)", nil
			}
		}
		// Lazy sync: the wanted package/version may have been published since the last
		// pull. Sync the relevant registries once (best-effort) and re-check, unless
		// the caller asked to stay offline. `add` is otherwise offline.
		if !opts.Offline {
			s.syncForAdd(name, opts.Registry)
			if locs, err = s.findFiltered(name, version, opts); err != nil {
				return "", "", err
			}
		}
	}
	if len(locs) == 0 {
		// A known package with an unfindable explicit version: say what's actually
		// available so a stale clone or a typo is obvious (vs a truly unknown name).
		if version != "" {
			if _, reg, vers, verr := s.Loader.Versions(name); verr == nil && len(vers) > 0 {
				hint := ""
				if opts.Offline {
					hint = "; run 'cosm update' or drop --offline"
				}
				return "", "", fmt.Errorf("%w: %s has no version %s in registry '%s' (available: %s%s)",
					errs.ErrPackageNotFound, name, version, reg, strings.Join(vers, ", "), hint)
			}
		}
		return "", "", fmt.Errorf("%w: %s%s (try 'cosm update', or 'cosm develop %s --path <dir>' for a local package)",
			errs.ErrPackageNotFound, name, filterSuffix(opts), name)
	}
	loc := locs[0]
	if len(locs) > 1 {
		if choose == nil {
			return "", "", fmt.Errorf("%w: %s is ambiguous; narrow it with --registry and/or --major (or a version)", errs.ErrUsage, name)
		}
		if loc, err = choose(name, version, locs); err != nil {
			return "", "", err
		}
	}
	if err := s.writeDep(m, projectDir, name, loc.Specs.UUID, loc.Specs.Version, opts); err != nil {
		return "", "", err
	}
	return loc.Specs.Version, loc.Registry, nil
}

// findFiltered locates a package in the registries and applies the registry/major
// filters from opts.
func (s *Service) findFiltered(name, version string, opts AddOpts) ([]registry.Location, error) {
	locs, err := s.Loader.Find(name, version)
	if err != nil {
		return nil, err
	}
	if opts.Registry != "" {
		locs = filterLocs(locs, func(l registry.Location) bool { return l.Registry == opts.Registry })
	}
	if opts.Major >= 0 {
		locs = filterLocs(locs, func(l registry.Location) bool {
			mj, e := semver.Major(l.Specs.Version)
			return e == nil && mj == opts.Major
		})
	}
	return locs, nil
}

// syncForAdd best-effort pulls the registries relevant to name so a version
// published since the last update becomes visible. Errors are ignored — the caller
// re-checks locally and reports an actionable not-found if the package is still
// absent. It pulls narrowly: the named registry if pinned, else the one that already
// has the package, else every registry (to discover a wholly new one).
func (s *Service) syncForAdd(name, regFilter string) {
	switch {
	case regFilter != "":
		_ = s.Reg.UpdateRegistry(regFilter)
	default:
		if _, reg, vers, err := s.Loader.Versions(name); err == nil && reg != "" && len(vers) > 0 {
			_ = s.Reg.UpdateRegistry(reg)
		} else {
			s.Reg.UpdateAll()
		}
	}
}

// addFromWorkspace declares a dependency on an unpublished package adopted into the
// dev workspace, reading its identity from the local checkout. ok is false when no
// workspace unit matches name.
func (s *Service) addFromWorkspace(m *types.Manifest, projectDir, name, version string, opts AddOpts) (string, bool, error) {
	we, ok, err := s.workspaceUnit(name, opts.Major)
	if err != nil || !ok {
		return "", ok, err
	}
	man, err := manifest.LoadManifestFromDir(s.D.DevUnit(we.Name, we.Major))
	if err != nil {
		return "", true, fmt.Errorf("%w: workspace checkout for %s is unreadable: %v", errs.ErrUsage, name, err)
	}
	if version != "" && version != man.Version {
		return "", true, fmt.Errorf("%w: %s is in development at %s, not %s (omit the version)", errs.ErrUsage, name, man.Version, version)
	}
	if err := s.writeDep(m, projectDir, name, man.UUID, man.Version, opts); err != nil {
		return "", true, err
	}
	return man.Version, true, nil
}

// writeDep records a dependency on <uuid>@<major-of-version> in the manifest and
// saves it. A compatibility unit may live in `deps` or `testDeps`, never both.
func (s *Service) writeDep(m *types.Manifest, projectDir, name, uuid, version string, opts AddOpts) error {
	major, err := semver.Major(version)
	if err != nil {
		return err
	}
	key := semver.UnitKey(uuid, major)
	if _, exists := m.Deps[key]; exists {
		return fmt.Errorf("%w: %s@v%d already a dependency", errs.ErrUsage, name, major)
	}
	if _, exists := m.TestDeps[key]; exists {
		return fmt.Errorf("%w: %s@v%d already a test dependency", errs.ErrUsage, name, major)
	}
	target := &m.Deps
	if opts.Test {
		target = &m.TestDeps
	}
	if *target == nil {
		*target = map[string]types.Dependency{}
	}
	(*target)[key] = types.Dependency{Name: name, Version: version}
	return manifest.SaveManifest(filepath.Join(projectDir, "cosm.json"), m)
}

// DepChooser is called when a name matches multiple majors on removal.
type DepChooser func(name string, keys []string, deps []types.Dependency) (string, error)

// Rm removes a dependency from the project (§12.2).
func (s *Service) Rm(projectDir, name string, choose DepChooser) error {
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return err
	}
	var keys []string
	var deps []types.Dependency
	isTest := map[string]bool{}
	for k, dp := range m.Deps {
		if dp.Name == name {
			keys = append(keys, k)
			deps = append(deps, dp)
		}
	}
	for k, dp := range m.TestDeps {
		if dp.Name == name {
			keys = append(keys, k)
			deps = append(deps, dp)
			isTest[k] = true
		}
	}
	if len(keys) == 0 {
		return fmt.Errorf("%w: dependency %q not found", errs.ErrUsage, name)
	}
	key := keys[0]
	if len(keys) > 1 {
		if key, err = choose(name, keys, deps); err != nil {
			return err
		}
	}
	if isTest[key] {
		delete(m.TestDeps, key)
	} else {
		delete(m.Deps, key)
	}
	return manifest.SaveManifest(filepath.Join(projectDir, "cosm.json"), m)
}

func filterLocs(locs []registry.Location, keep func(registry.Location) bool) []registry.Location {
	out := locs[:0:0]
	for _, l := range locs {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out
}

func filterSuffix(opts AddOpts) string {
	s := ""
	if opts.Registry != "" {
		s += fmt.Sprintf(" in registry '%s'", opts.Registry)
	}
	if opts.Major >= 0 {
		s += fmt.Sprintf(" at major v%d", opts.Major)
	}
	return s
}
