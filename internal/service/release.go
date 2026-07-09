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

// Release bumps cosm.json, commits, tags, and pushes branch + tag (§12.4).
func (s *Service) Release(projectDir string, opts ReleaseOpts) (string, error) {
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return "", err
	}
	newVer, err := nextVersion(m.Version, opts)
	if err != nil {
		return "", err
	}
	if err := semver.ValidateExact(newVer); err != nil {
		return "", fmt.Errorf("%w: %v", errs.ErrUsage, err)
	}
	if semver.Compare(newVer, m.Version) < 0 {
		return "", fmt.Errorf("%w: new version %s is below current %s", errs.ErrUsage, newVer, m.Version)
	}

	// Clean worktree required.
	status, err := s.Git.StatusPorcelain(projectDir)
	if err != nil {
		return "", err
	}
	if status != "" {
		return "", fmt.Errorf("%w: commit or stash changes before releasing", errs.ErrDirtyWorktree)
	}
	// Tag must not already exist.
	tags, err := s.Git.ListTags(projectDir)
	if err != nil {
		return "", err
	}
	for _, t := range tags {
		if t == newVer {
			return "", fmt.Errorf("%w: tag %s exists", errs.ErrVersionExists, newVer)
		}
	}
	branch, err := s.Git.CurrentBranch(projectDir)
	if err != nil {
		return "", err
	}
	// In-sync with origin (best-effort: only if an upstream exists).
	if err := s.Git.Fetch(projectDir, "origin"); err == nil {
		if behind, berr := s.Git.BehindCount(projectDir, branch); berr == nil && behind > 0 {
			return "", fmt.Errorf("%w: pull %d commits from origin/%s first", errs.ErrNotInSync, behind, branch)
		}
	}

	m.Version = newVer
	if err := manifest.SaveManifest(filepath.Join(projectDir, "cosm.json"), m); err != nil {
		return "", err
	}
	if err := s.Git.Add(projectDir, "cosm.json"); err != nil {
		return "", err
	}
	if err := s.Git.Commit(projectDir, "Release "+newVer); err != nil {
		return "", err
	}
	if err := s.Git.Tag(projectDir, newVer); err != nil {
		return "", err
	}
	if err := s.Git.Push(projectDir, branch); err != nil {
		return "", err
	}
	if err := s.Git.Push(projectDir, newVer); err != nil {
		return "", err
	}
	return newVer, nil
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
