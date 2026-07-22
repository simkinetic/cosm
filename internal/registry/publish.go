// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package registry

import (
	"fmt"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/types"
)

// PublishBinary records (or updates) a binary artifact for a version in a
// binary/mixed registry (§8.5). It merges into any existing specs, replacing a
// binary with the same build key, and commits atomically.
func (s *Service) PublishBinary(regName string, specs types.Specs, binary types.BinaryArtifact) error {
	regDir := s.d.Registry(regName)
	reg, err := manifest.LoadRegistry(MetaFile(regDir))
	if err != nil {
		return fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	if reg.Kind == types.KindSource {
		return fmt.Errorf("%w: registry %q is source-only; use a binary or mixed registry", errs.ErrUsage, regName)
	}

	// Ensure the package is listed.
	if _, ok := reg.Packages[specs.Name]; !ok {
		reg.Packages[specs.Name] = types.PackageInfo{UUID: specs.UUID, GitURL: specs.GitURL}
		if err := manifest.SaveRegistry(MetaFile(regDir), reg); err != nil {
			return err
		}
	}

	// Merge into any existing specs for this version.
	specsPath := SpecsFile(regDir, specs.Name, specs.Version)
	if existing, err := manifest.LoadSpecs(specsPath); err == nil {
		specs.Binaries = existing.Binaries
		if specs.GitURL == "" {
			specs.GitURL = existing.GitURL
		}
	}
	replaced := false
	for i, b := range specs.Binaries {
		if b.BuildKey == binary.BuildKey {
			specs.Binaries[i] = binary
			replaced = true
			break
		}
	}
	if !replaced {
		specs.Binaries = append(specs.Binaries, binary)
	}
	if specs.Deps == nil {
		specs.Deps = map[string]types.Dependency{}
	}
	if err := manifest.SaveSpecs(specsPath, &specs); err != nil {
		return err
	}

	// Track the version.
	vs, _ := manifest.LoadVersions(VersionsFile(regDir, specs.Name))
	if !contains(vs, specs.Version) {
		vs = append(vs, specs.Version)
		sortSemver(vs)
		if err := manifest.SaveVersions(VersionsFile(regDir, specs.Name), vs); err != nil {
			return err
		}
	}

	msg := fmt.Sprintf("Publish binary %s@%s (%s/%s)", specs.Name, specs.Version, binary.Platform.OS, binary.Platform.Arch)
	return s.commitPush(regDir, msg)
}
