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

// Add adds a dependency to the project's cosm.json (§12.2).
func (s *Service) Add(projectDir, name, version string, choose RegistryChooser) (chosenVersion, chosenRegistry string, err error) {
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return "", "", err
	}
	locs, err := s.Loader.Find(name, version)
	if err != nil {
		return "", "", err
	}
	if len(locs) == 0 {
		return "", "", fmt.Errorf("%w: %s (try 'cosm update')", errs.ErrPackageNotFound, name)
	}
	loc := locs[0]
	if len(locs) > 1 {
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
