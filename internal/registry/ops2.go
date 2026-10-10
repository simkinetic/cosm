// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package registry

import (
	"fmt"
	"os"
	"path/filepath"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/types"
)

// CloneRegistry clones an existing registry and registers it locally (§12.5).
func (s *Service) CloneRegistry(giturl string) (string, error) {
	if giturl == "" {
		return "", fmt.Errorf("%w: giturl required", errs.ErrUsage)
	}
	tmp := filepath.Join(s.d.Registries(), "tmp-clone")
	os.RemoveAll(tmp)
	if err := s.git.Clone(giturl, tmp); err != nil {
		return "", err
	}
	moved := false
	defer func() {
		if !moved {
			os.RemoveAll(tmp)
		}
	}()
	reg, err := manifest.LoadRegistry(MetaFile(tmp))
	if err != nil {
		return "", fmt.Errorf("cloned repo has no valid registry.json: %w", err)
	}
	if reg.Name == "" {
		return "", fmt.Errorf("registry.json missing name")
	}
	final := s.d.Registry(reg.Name)
	if _, err := os.Stat(final); err == nil {
		return "", fmt.Errorf("registry %q already exists locally", reg.Name)
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	moved = true
	if err := s.registerRef(types.RegistryRef{Name: reg.Name, UUID: reg.UUID, GitURL: reg.GitURL}); err != nil {
		return "", err
	}
	return reg.Name, nil
}

// RemovePackage removes an entire package from a registry (§12.5).
func (s *Service) RemovePackage(regName, pkgName string) error {
	regDir := s.d.Registry(regName)
	reg, err := manifest.LoadRegistry(MetaFile(regDir))
	if err != nil {
		return fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	if _, ok := reg.Packages[pkgName]; !ok {
		return fmt.Errorf("%w: %s in %s", errs.ErrPackageNotFound, pkgName, regName)
	}
	if err := os.RemoveAll(PackageDir(regDir, pkgName)); err != nil {
		return err
	}
	delete(reg.Packages, pkgName)
	if err := manifest.SaveRegistry(MetaFile(regDir), reg); err != nil {
		return err
	}
	return s.commitPush(regDir, "Remove package "+pkgName)
}

// RemoveVersion removes a single version of a package (§12.5).
func (s *Service) RemoveVersion(regName, pkgName, version string) error {
	regDir := s.d.Registry(regName)
	if _, err := manifest.LoadRegistry(MetaFile(regDir)); err != nil {
		return fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	vs, _ := manifest.LoadVersions(VersionsFile(regDir, pkgName))
	if !contains(vs, version) {
		return fmt.Errorf("%w: %s@%s not in %s", errs.ErrPackageNotFound, pkgName, version, regName)
	}
	if err := os.RemoveAll(VersionDir(regDir, pkgName, version)); err != nil {
		return err
	}
	var kept []string
	for _, v := range vs {
		if v != version {
			kept = append(kept, v)
		}
	}
	if err := manifest.SaveVersions(VersionsFile(regDir, pkgName), kept); err != nil {
		return err
	}
	// Remember the removal, so the next add or sync doesn't register the tag again
	// (it still exists in the package's repository). `add --tag` undoes it.
	pol, err := manifest.LoadPolicy(PolicyFile(regDir, pkgName))
	if err != nil {
		return err
	}
	if !contains(pol.Excluded, version) {
		pol.Excluded = append(pol.Excluded, version)
		sortSemver(pol.Excluded)
	}
	delete(pol.Yanked, version)
	if err := manifest.SavePolicy(PolicyFile(regDir, pkgName), pol); err != nil {
		return err
	}
	return s.commitPush(regDir, fmt.Sprintf("Remove %s@%s", pkgName, version))
}

// Yank marks a registered version as yanked, with a reason. A yanked version stays
// registered and keeps resolving for anything that already requires it — builds
// stay reproducible — but is never newly selected (cosm add, upgrade), and
// resolving it warns. Prefer it to RemoveVersion for a bad release.
func (s *Service) Yank(regName, pkgName, version, reason string) error {
	return s.setYank(regName, pkgName, version, reason, true)
}

// Unyank clears a yank.
func (s *Service) Unyank(regName, pkgName, version string) error {
	return s.setYank(regName, pkgName, version, "", false)
}

func (s *Service) setYank(regName, pkgName, version, reason string, yank bool) error {
	regDir := s.d.Registry(regName)
	if _, err := manifest.LoadRegistry(MetaFile(regDir)); err != nil {
		return fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	vs, _ := manifest.LoadVersions(VersionsFile(regDir, pkgName))
	if !contains(vs, version) {
		return fmt.Errorf("%w: %s@%s not in %s", errs.ErrPackageNotFound, pkgName, version, regName)
	}
	pol, err := manifest.LoadPolicy(PolicyFile(regDir, pkgName))
	if err != nil {
		return err
	}
	_, was := pol.Yanked[version]
	if yank {
		if reason == "" {
			reason = "yanked"
		}
		if was && pol.Yanked[version] == reason {
			return nil
		}
		if pol.Yanked == nil {
			pol.Yanked = map[string]string{}
		}
		pol.Yanked[version] = reason
	} else {
		if !was {
			return nil
		}
		delete(pol.Yanked, version)
	}
	if err := manifest.SavePolicy(PolicyFile(regDir, pkgName), pol); err != nil {
		return err
	}
	verb := "Yank"
	if !yank {
		verb = "Unyank"
	}
	return s.commitPush(regDir, fmt.Sprintf("%s %s@%s", verb, pkgName, version))
}

// UpdateRegistry pulls a registry from its remote (§12.6).
func (s *Service) UpdateRegistry(regName string) error {
	regDir := s.d.Registry(regName)
	if _, err := os.Stat(regDir); os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	branch, err := s.git.CurrentBranch(regDir)
	if err != nil {
		return err
	}
	return s.git.Pull(regDir, "origin", branch)
}

// ListRegistries returns the registered registry refs.
func (s *Service) ListRegistries() ([]types.RegistryRef, error) {
	return manifest.LoadRegistryRefs(s.d.RegistriesFile())
}

// UpdateAll pulls every registered registry from its remote. It attempts all
// registries and returns the names updated plus a map of per-registry failures,
// so one unreachable remote does not abort the rest (§12.6).
func (s *Service) UpdateAll() (updated []string, failures map[string]error) {
	refs, _ := manifest.LoadRegistryRefs(s.d.RegistriesFile())
	failures = map[string]error{}
	for _, r := range refs {
		if err := s.UpdateRegistry(r.Name); err != nil {
			failures[r.Name] = err
			continue
		}
		updated = append(updated, r.Name)
	}
	return updated, failures
}

// DeleteRegistryLocal removes a registry clone and its ref (no remote change).
func (s *Service) DeleteRegistryLocal(regName string) error {
	refs, _ := manifest.LoadRegistryRefs(s.d.RegistriesFile())
	found := false
	var kept []types.RegistryRef
	for _, r := range refs {
		if r.Name == regName {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	if err := os.RemoveAll(s.d.Registry(regName)); err != nil {
		return err
	}
	return manifest.SaveRegistryRefs(s.d.RegistriesFile(), kept)
}
