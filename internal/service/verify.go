// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"cosm/internal/depot"
	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// ThrowawayDepot creates a fresh, temporary depot for a clean-room check (cosm
// verify): the same registries as s — cloned anew from their remotes, so it sees
// what is actually published, or with local copied from s's clones (and package
// mirrors) for offline use — plus s's extensions, linked. It is built with
// depot.New directly, never through depot resolution, so it can't touch the
// global default depot, $COSM_DEPOT, or s's depot. The returned func deletes it.
func (s *Service) ThrowawayDepot(local bool) (*Service, string, func(), error) {
	dir, err := os.MkdirTemp("", "cosm-verify-")
	if err != nil {
		return nil, "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	fail := func(err error) (*Service, string, func(), error) {
		cleanup()
		return nil, "", nil, err
	}
	d := depot.New(filepath.Join(dir, ".cosm"))
	if err := d.Setup(); err != nil {
		return fail(err)
	}
	t := New(d, s.Git)

	refs, err := s.Reg.ListRegistries()
	if err != nil {
		return fail(err)
	}
	if local {
		for _, r := range refs {
			if err := copyAll(s.D.Registry(r.Name), d.Registry(r.Name)); err != nil {
				return fail(fmt.Errorf("copy registry %s: %w", r.Name, err))
			}
		}
		if err := manifest.SaveRegistryRefs(d.RegistriesFile(), refs); err != nil {
			return fail(err)
		}
		if mirrors := filepath.Join(s.D.Root, "mirrors"); dirExists(mirrors) {
			if err := copyAll(mirrors, filepath.Join(d.Root, "mirrors")); err != nil {
				return fail(fmt.Errorf("copy package mirrors: %w", err))
			}
		}
	} else {
		for _, r := range refs {
			if _, err := t.Reg.CloneRegistry(r.GitURL); err != nil {
				return fail(fmt.Errorf("clone registry %s from %s: %w (use --local to verify against your local clones)", r.Name, r.GitURL, err))
			}
		}
	}

	// Extensions are tools, not state: link the caller's in.
	if ents, err := os.ReadDir(s.D.Extensions()); err == nil {
		for _, e := range ents {
			src, _ := filepath.Abs(filepath.Join(s.D.Extensions(), e.Name()))
			_ = os.Symlink(src, filepath.Join(d.Extensions(), e.Name()))
		}
	}
	return t, dir, cleanup, nil
}

// RegisteredEntry finds name at version (or its newest selectable version) in the
// service's registries and returns it as a build-list entry, plus the registry it
// came from.
func (s *Service) RegisteredEntry(name, version string) (types.BuildListEntry, string, error) {
	if version == "" {
		_, _, vers, err := s.Loader.SelectableVersions(name)
		if err != nil {
			return types.BuildListEntry{}, "", err
		}
		for _, v := range vers {
			if version == "" || semver.Compare(v, version) > 0 {
				version = v
			}
		}
		if version == "" {
			return types.BuildListEntry{}, "", fmt.Errorf("%w: %s has no selectable (non-yanked) version", errs.ErrPackageNotFound, name)
		}
	}
	locs, err := s.Loader.Find(name, version)
	if err != nil {
		return types.BuildListEntry{}, "", err
	}
	if len(locs) == 0 {
		return types.BuildListEntry{}, "", fmt.Errorf("%w: %s %s", errs.ErrPackageNotFound, name, version)
	}
	sp := locs[0].Specs
	major, err := semver.Major(sp.Version)
	if err != nil {
		return types.BuildListEntry{}, "", err
	}
	return types.BuildListEntry{
		Name: sp.Name, UUID: sp.UUID, Major: major, Version: sp.Version,
		GitURL: sp.GitURL, Commit: sp.Commit, Tree: sp.Tree, Build: sp.Build, Provides: sp.Provides,
	}, locs[0].Registry, nil
}

// copyAll copies a directory tree exactly — including .git directories, which
// extlib.CopyTree (meant for package sources) skips; the registry clones and
// package mirrors are git repositories and must stay usable.
func copyAll(src, dst string) error {
	return filepath.WalkDir(src, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := de.Info()
		if err != nil {
			return err
		}
		switch {
		case de.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			tgt, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(tgt, target)
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm())
		}
	})
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
