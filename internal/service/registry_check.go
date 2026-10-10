// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/registry"
	"cosm/internal/resolve"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// RegistryProblem is one registered version that consumers can't resolve.
type RegistryProblem struct {
	Registry, Package, Version string
	Err                        error
}

// RegistryCheck is the outcome of CheckRegistries.
type RegistryCheck struct {
	Registries []string
	Packages   int
	Versions   int // versions checked
	Yanked     int // yanked versions, skipped: abandoned on purpose
	Problems   []RegistryProblem
}

// CheckRegistries confirms that every registered version resolves inside the
// registries: each declared dependency minimum is registered, and the version's
// whole closure resolves from registries alone. It reads the local clones as they
// are (run `cosm update` first for the latest). regName "" checks every registry;
// latestOnly checks only the newest version of each package's major line.
func (s *Service) CheckRegistries(regName string, latestOnly bool) (*RegistryCheck, error) {
	refs, err := s.Reg.ListRegistries()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, r := range refs {
		if regName == "" || r.Name == regName {
			names = append(names, r.Name)
		}
	}
	if regName != "" && len(names) == 0 {
		return nil, fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	sort.Strings(names)
	out := &RegistryCheck{Registries: names}
	for _, rn := range names {
		regDir := s.D.Registry(rn)
		reg, err := manifest.LoadRegistry(registry.MetaFile(regDir))
		if err != nil {
			return nil, fmt.Errorf("registry %s: %w", rn, err)
		}
		pkgs := make([]string, 0, len(reg.Packages))
		for p := range reg.Packages {
			pkgs = append(pkgs, p)
		}
		sort.Strings(pkgs)
		for _, pkg := range pkgs {
			out.Packages++
			vers, err := manifest.LoadVersions(registry.VersionsFile(regDir, pkg))
			if err != nil {
				out.Problems = append(out.Problems, RegistryProblem{rn, pkg, "", fmt.Errorf("versions list unreadable: %w", err)})
				continue
			}
			pol, _ := manifest.LoadPolicy(registry.PolicyFile(regDir, pkg))
			var live []string
			for _, v := range vers {
				if _, y := pol.Yanked[v]; y {
					out.Yanked++
					continue
				}
				live = append(live, v)
			}
			vers = live
			if latestOnly {
				vers = latestPerMajor(vers)
			}
			for _, v := range vers {
				out.Versions++
				for _, perr := range s.checkRegisteredVersion(regDir, pkg, reg.Packages[pkg].UUID, v) {
					out.Problems = append(out.Problems, RegistryProblem{rn, pkg, v, perr})
				}
			}
		}
	}
	return out, nil
}

// checkRegisteredVersion returns what keeps one registered version from resolving.
func (s *Service) checkRegisteredVersion(regDir, pkg, uuid, version string) []error {
	sp, err := manifest.LoadSpecs(registry.SpecsFile(regDir, pkg, version))
	if err != nil {
		return []error{fmt.Errorf("specs.json unreadable: %w", err)}
	}
	if sp.UUID != uuid {
		return []error{fmt.Errorf("%w: specs.json has uuid %s, the registry index %s", errs.ErrIntegrityMismatch, sp.UUID, uuid)}
	}
	keys := make([]string, 0, len(sp.Deps))
	for k := range sp.Deps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return sp.Deps[keys[i]].Name < sp.Deps[keys[j]].Name })
	var probs []error
	for _, k := range keys {
		d := sp.Deps[k]
		duuid, _, _ := semver.SplitUnitKey(k)
		if _, err := s.Loader.Load(d.Name, duuid, d.Version); errors.Is(err, errs.ErrPackageNotFound) {
			hint := "not registered at all"
			if _, _, vs, verr := s.Loader.SelectableVersions(d.Name); verr == nil && len(vs) > 0 {
				hint = "registered: " + strings.Join(vs, ", ")
			}
			probs = append(probs, kindf(errs.ErrPackageNotFound, "requires %s %s, which is in no registry (%s)", d.Name, d.Version, hint))
		}
	}
	if len(probs) > 0 {
		return probs
	}
	root := &types.Manifest{Name: sp.Name, UUID: sp.UUID, Version: sp.Version, Deps: sp.Deps}
	if _, _, err := resolve.Resolve(root, s.Loader, resolve.NoDev); err != nil {
		return []error{fmt.Errorf("its dependency closure doesn't resolve: %w", err)}
	}
	return nil
}

// latestPerMajor keeps the newest version of each major line.
func latestPerMajor(vers []string) []string {
	best := map[int]string{}
	for _, v := range vers {
		mj, err := semver.Major(v)
		if err != nil {
			continue
		}
		if cur, ok := best[mj]; !ok || semver.Compare(v, cur) > 0 {
			best[mj] = v
		}
	}
	out := make([]string, 0, len(best))
	for _, v := range best {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return semver.Compare(out[i], out[j]) < 0 })
	return out
}
