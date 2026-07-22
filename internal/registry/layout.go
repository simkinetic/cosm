// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

// Package registry implements the sharded on-disk registry layout (§4.3), a
// disk-backed SpecLoader for the resolver (§7), and registry mutations (§12.5).
package registry

import (
	"path/filepath"
	"strings"
)

// Shard is the uppercase first letter of a package name (§4.3).
func Shard(name string) string {
	if name == "" {
		return "_"
	}
	return strings.ToUpper(name[:1])
}

func MetaFile(registryDir string) string { return filepath.Join(registryDir, "registry.json") }

func PackageDir(registryDir, name string) string {
	return filepath.Join(registryDir, Shard(name), name)
}

func VersionsFile(registryDir, name string) string {
	return filepath.Join(PackageDir(registryDir, name), "versions.json")
}

func VersionDir(registryDir, name, version string) string {
	return filepath.Join(PackageDir(registryDir, name), version)
}

func SpecsFile(registryDir, name, version string) string {
	return filepath.Join(VersionDir(registryDir, name, version), "specs.json")
}
