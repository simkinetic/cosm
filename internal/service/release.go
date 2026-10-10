// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"fmt"
	"path/filepath"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/semver"
)

// ReleaseOpts selects the new version either explicitly or by increment.
type ReleaseOpts struct {
	Version string // explicit vX.Y.Z (wins if set)
	Patch   bool
	Minor   bool
	Major   bool
}

// Release bumps cosm.json, commits, tags, and pushes branch + tag (§12.4). It
// runs the full preflight first (PlanRelease) and refuses on any problem, listing
// all of them, before anything is changed.
func (s *Service) Release(projectDir string, opts ReleaseOpts) (string, error) {
	p, err := s.PlanRelease(projectDir, opts)
	if err != nil {
		return "", err
	}
	switch probs := p.Problems(); len(probs) {
	case 0:
	case 1:
		return "", probs[0]
	default:
		return "", &PreflightError{Problems: probs}
	}

	if err := manifest.SaveManifest(filepath.Join(projectDir, "cosm.json"), p.Released); err != nil {
		return "", err
	}
	if err := s.Git.Add(projectDir, "cosm.json"); err != nil {
		return "", err
	}
	if err := s.Git.Commit(projectDir, "Release "+p.To); err != nil {
		return "", err
	}
	if err := s.Git.Tag(projectDir, p.To); err != nil {
		return "", err
	}
	if err := s.Git.Push(projectDir, p.Branch); err != nil {
		return "", err
	}
	if err := s.Git.Push(projectDir, p.To); err != nil {
		return "", err
	}
	return p.To, nil
}

func nextVersion(current string, opts ReleaseOpts) (string, error) {
	if opts.Version != "" {
		return opts.Version, nil
	}
	cur, err := semver.Parse(current)
	if err != nil {
		return "", fmt.Errorf("current version %q: %w", current, err)
	}
	switch {
	case opts.Major:
		return semver.Version{Major: cur.Major + 1}.String(), nil
	case opts.Minor:
		return semver.Version{Major: cur.Major, Minor: cur.Minor + 1}.String(), nil
	case opts.Patch:
		return semver.Version{Major: cur.Major, Minor: cur.Minor, Patch: cur.Patch + 1}.String(), nil
	}
	return "", fmt.Errorf("%w: specify a version or --patch/--minor/--major", errs.ErrUsage)
}
