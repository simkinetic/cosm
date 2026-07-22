// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/resolve"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// Upgrade raises the floor of a dependency to the newest version within its
// current major (§12.2). A constraint (v1, v1.2, v1.2.3) narrows the choice;
// --latest ignores the finer constraint. Returns (old, new).
func (s *Service) Upgrade(projectDir, name, constraint string, latest bool) (string, string, error) {
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return "", "", err
	}
	wantMajor := -1
	if constraint != "" {
		if mj, err := semver.Major(constraint); err == nil {
			wantMajor = mj
		} else {
			return "", "", fmt.Errorf("%w: bad version constraint %q", errs.ErrUsage, constraint)
		}
	}
	key := ""
	count := 0
	for k, dp := range m.Deps {
		if dp.Name != name {
			continue
		}
		if wantMajor >= 0 {
			if _, mj, _ := semver.SplitUnitKey(k); mj != wantMajor {
				continue
			}
		}
		key = k
		count++
	}
	if count == 0 {
		return "", "", fmt.Errorf("%w: %s is not a dependency", errs.ErrUsage, name)
	}
	if count > 1 {
		return "", "", fmt.Errorf("%w: %s has multiple majors; include a version like v1", errs.ErrUsage, name)
	}
	_, major, _ := semver.SplitUnitKey(key)
	cur := m.Deps[key].Version
	_, _, versions, err := s.Loader.Versions(name)
	if err != nil {
		return "", "", err
	}
	target := pickUpgrade(versions, major, constraint, latest)
	if target == "" {
		return "", "", fmt.Errorf("%w: no version of %s in v%d matches", errs.ErrPackageNotFound, name, major)
	}
	if semver.Compare(target, cur) <= 0 {
		return cur, cur, nil // already up to date
	}
	d := m.Deps[key]
	d.Version = target
	m.Deps[key] = d
	if err := manifest.SaveManifest(filepath.Join(projectDir, "cosm.json"), m); err != nil {
		return "", "", err
	}
	return cur, target, nil
}

// Downgrade lowers a dependency to an earlier version using the MVS downgrade
// algorithm (§7.5): the target is lowered, and any direct dependency that pins
// it higher is cascade-downgraded to its highest still-compatible version. Each
// forced downgrade is returned as a warning. A genuinely unsatisfiable
// dependency (no version is compatible with the target) is a hard error.
func (s *Service) Downgrade(projectDir, name, version string) ([]string, error) {
	if err := semver.ValidateExact(version); err != nil {
		return nil, fmt.Errorf("%w: %v", errs.ErrUsage, err)
	}
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return nil, err
	}
	_, bl, _, err := s.Resolve(projectDir)
	if err != nil {
		return nil, err
	}
	major, _ := semver.Major(version)

	// Locate the target unit in the resolved build list (it may be transitive).
	uKey := ""
	for k, e := range bl.Dependencies {
		if e.Name == name && e.Major == major {
			uKey = k
			break
		}
	}
	if uKey == "" {
		return nil, fmt.Errorf("%w: %s v%d is not a dependency", errs.ErrUsage, name, major)
	}
	curSel := bl.Dependencies[uKey].Version
	if semver.Compare(version, curSel) >= 0 {
		return nil, fmt.Errorf("%w: %s is not below the current %s", errs.ErrUsage, version, curSel)
	}
	if _, _, uvers, err := s.Loader.Versions(name); err != nil {
		return nil, err
	} else if !containsStr(uvers, version) {
		return nil, fmt.Errorf("%w: %s@%s", errs.ErrPackageNotFound, name, version)
	}

	newDeps := map[string]types.Dependency{}
	for k, v := range m.Deps {
		newDeps[k] = v
	}
	var warns, blockers []string

	for aKey, dep := range m.Deps {
		if aKey == uKey { // the target itself (if a direct dep): pin to the new version
			if dep.Version != version {
				newDeps[aKey] = types.Dependency{Name: dep.Name, Version: version}
				warns = append(warns, fmt.Sprintf("%s: %s -> %s", dep.Name, dep.Version, version))
			}
			continue
		}
		// Does this direct dep already keep the target at or below the goal?
		okc, err := s.closureAtMost(aKey, dep.Name, dep.Version, uKey, version)
		if err != nil {
			return nil, err
		}
		if okc {
			continue
		}
		// Find the highest version of this dep (not above its current floor) whose
		// closure keeps the target at or below the goal.
		cand, err := s.highestCompatible(aKey, dep.Name, dep.Version, uKey, version)
		if err != nil {
			return nil, err
		}
		if cand == "" {
			blockers = append(blockers, dep.Name)
			continue
		}
		if cand != dep.Version {
			newDeps[aKey] = types.Dependency{Name: dep.Name, Version: cand}
			warns = append(warns, fmt.Sprintf("%s: %s -> %s (to allow %s %s)", dep.Name, dep.Version, cand, name, version))
		}
	}
	if len(blockers) > 0 {
		return nil, fmt.Errorf("%w: cannot downgrade %s to %s; no compatible version of %s",
			errs.ErrResolutionConflict, name, version, strings.Join(blockers, ", "))
	}

	m.Deps = newDeps
	if err := manifest.SaveManifest(filepath.Join(projectDir, "cosm.json"), m); err != nil {
		return nil, err
	}
	// Confirm and report the actual resolved version.
	if _, bl2, _, err := s.Resolve(projectDir); err == nil {
		if e, ok := bl2.Dependencies[uKey]; ok {
			if semver.Compare(e.Version, version) > 0 {
				return nil, fmt.Errorf("%w: %s still resolves to %s", errs.ErrResolutionConflict, name, e.Version)
			}
			if semver.Compare(e.Version, version) < 0 {
				warns = append(warns, fmt.Sprintf("%s resolved to %s (below the requested %s)", name, e.Version, version))
			}
		}
	}
	return warns, nil
}

// closureAtMost reports whether depending on aKey@version keeps the unit uKey at
// or below target in the resolved closure.
func (s *Service) closureAtMost(aKey, name, version, uKey, target string) (bool, error) {
	probe := &types.Manifest{Name: "__probe", Version: "v0.0.0",
		Deps: map[string]types.Dependency{aKey: {Name: name, Version: version}}}
	bl, _, err := resolve.Resolve(probe, s.Loader, nil)
	if err != nil {
		return false, err
	}
	e, ok := bl.Dependencies[uKey]
	if !ok {
		return true, nil // the target isn't pulled in at all
	}
	return semver.Compare(e.Version, target) <= 0, nil
}

// highestCompatible returns the highest version of a dep (same major, not above
// maxVer) whose closure keeps uKey at or below target, or "" if none.
func (s *Service) highestCompatible(aKey, name, maxVer, uKey, target string) (string, error) {
	_, aMajor, _ := semver.SplitUnitKey(aKey)
	_, _, vers, err := s.Loader.Versions(name)
	if err != nil {
		return "", err
	}
	var cands []string
	for _, v := range vers {
		if mj, e := semver.Major(v); e == nil && mj == aMajor && semver.Compare(v, maxVer) <= 0 {
			cands = append(cands, v)
		}
	}
	sort.Slice(cands, func(i, j int) bool { return semver.Compare(cands[i], cands[j]) > 0 }) // descending
	for _, v := range cands {
		ok, err := s.closureAtMost(aKey, name, v, uKey, target)
		if err != nil {
			return "", err
		}
		if ok {
			return v, nil
		}
	}
	return "", nil
}

func pickUpgrade(versions []string, major int, constraint string, latest bool) string {
	best := ""
	for _, v := range versions {
		mj, err := semver.Major(v)
		if err != nil || mj != major {
			continue
		}
		if !latest && constraint != "" && !constraintMatches(v, constraint) {
			continue
		}
		if best == "" || semver.Compare(v, best) > 0 {
			best = v
		}
	}
	return best
}

func constraintMatches(v, c string) bool {
	comps := func(s string) []string {
		s = strings.TrimPrefix(s, "v")
		if i := strings.IndexAny(s, "-+"); i >= 0 {
			s = s[:i]
		}
		return strings.Split(s, ".")
	}
	cc, vv := comps(c), comps(v)
	for i := range cc {
		if i >= len(vv) || cc[i] != vv[i] {
			return false
		}
	}
	return true
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
