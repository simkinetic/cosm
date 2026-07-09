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
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return nil, types.BuildList{}, nil, err
	}
	ov, _, err := develop.Build(s.D, projectDir)
	if err != nil {
		return nil, types.BuildList{}, nil, err
	}
	bl, warns, err := resolve.Resolve(m, s.Loader, ov)
	return m, bl, warns, err
}

// RegistryChooser is called when a package is found in multiple registries.
type RegistryChooser func(name, version string, locs []registry.Location) (registry.Location, error)

// AddOpts narrows candidate selection non-interactively (a fast-path around the
// chooser prompt, for scripts/CI).
type AddOpts struct {
	Registry string // "" = any registry
	Major    int    // -1 = any major
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
		return "", "", fmt.Errorf("%w: %s%s (try 'cosm update')", errs.ErrPackageNotFound, name, filterSuffix(opts))
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
	major, err := semver.Major(loc.Specs.Version)
	if err != nil {
		return "", "", err
	}
	key := semver.UnitKey(loc.Specs.UUID, major)
	if _, exists := m.Deps[key]; exists {
		return "", "", fmt.Errorf("%w: %s@v%d already a dependency", errs.ErrUsage, name, major)
	}
	if m.Deps == nil {
		m.Deps = map[string]types.Dependency{}
	}
	m.Deps[key] = types.Dependency{Name: name, Version: loc.Specs.Version}
	if err := manifest.SaveManifest(filepath.Join(projectDir, "cosm.json"), m); err != nil {
		return "", "", err
	}
	return loc.Specs.Version, loc.Registry, nil
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
	for k, dp := range m.Deps {
		if dp.Name == name {
			keys = append(keys, k)
			deps = append(deps, dp)
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
	delete(m.Deps, key)
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
