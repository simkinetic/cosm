// Package service composes the depot, git, registry, and resolver into the
// project-level operations the CLI drives (§12).
package service

import (
	"fmt"
	"path/filepath"

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

// Resolve loads a project's cosm.json and computes its build list, applying any
// develop overrides enrolled by the project (§7).
func (s *Service) Resolve(projectDir string) (*types.Manifest, types.BuildList, []resolve.Warning, error) {
	return s.resolve(projectDir, false)
}

// ResolveWithTests is Resolve but also includes the project's test-only
// dependencies (§7.6) — used by `cosm test`.
func (s *Service) ResolveWithTests(projectDir string) (*types.Manifest, types.BuildList, []resolve.Warning, error) {
	return s.resolve(projectDir, true)
}

func (s *Service) resolve(projectDir string, includeTests bool) (*types.Manifest, types.BuildList, []resolve.Warning, error) {
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return nil, types.BuildList{}, nil, err
	}
	ov, _, err := develop.Build(s.D, projectDir)
	if err != nil {
		return nil, types.BuildList{}, nil, err
	}
	var bl types.BuildList
	var warns []resolve.Warning
	if includeTests {
		bl, warns, err = resolve.ResolveWithTests(m, s.Loader, ov)
	} else {
		bl, warns, err = resolve.Resolve(m, s.Loader, ov)
	}
	return m, bl, warns, err
}

// RegistryChooser is called when a package is found in multiple registries.
type RegistryChooser func(name, version string, locs []registry.Location) (registry.Location, error)

// AddOpts narrows candidate selection non-interactively (a fast-path around the
// chooser prompt, for scripts/CI) and selects the dependency kind.
type AddOpts struct {
	Registry string // "" = any registry
	Major    int    // -1 = any major
	Test     bool   // add to testDeps (test-only, non-transitive) instead of deps
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
	locs, err := s.Loader.Find(name, version)
	if err != nil {
		return "", "", err
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
	if len(locs) == 0 {
		// Fallback: an unpublished package adopted into the dev workspace
		// (`cosm develop <name> --path <dir>`). Source its identity locally.
		if opts.Registry == "" {
			if v, ok, ferr := s.addFromWorkspace(m, projectDir, name, version, opts); ferr != nil {
				return "", "", ferr
			} else if ok {
				return v, "(develop)", nil
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
