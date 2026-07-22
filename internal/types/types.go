// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

// Package types holds the pure on-disk data model for cosm (§4 of SPEC.md).
// These structs are serialized to/from JSON and carry no behavior.
package types

import "encoding/json"

// SchemaVersion is the current on-disk schema version for all manifests.
const SchemaVersion = 1

// Author is a structured package author (§4.1).
type Author struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

// Dependency is a single direct requirement value in a deps map.
// The map key is the compatibility unit "<uuid>@v<major>" (§6.3).
type Dependency struct {
	Name    string `json:"name"`    // locates the package (registry shard/path)
	Version string `json:"version"` // declared minimum version (MVS floor)
}

// Manifest is cosm.json — a project/package manifest (§4.1).
type Manifest struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Name          string                     `json:"name"`
	UUID          string                     `json:"uuid"`
	Version       string                     `json:"version"`
	Authors       []Author                   `json:"authors,omitempty"`
	Build         string                     `json:"build,omitempty"`    // extension id
	Provides      []string                   `json:"provides,omitempty"` // module namespaces
	Deps          map[string]Dependency      `json:"deps,omitempty"`
	TestDeps      map[string]Dependency      `json:"testDeps,omitempty"` // test-only, non-transitive (§7.6)
	Ext           map[string]json.RawMessage `json:"ext,omitempty"`      // opaque to core
}

// RegistryKind marks how a registry's packages are materialized (§4.2, §8.5).
type RegistryKind string

const (
	KindSource RegistryKind = "source"
	KindBinary RegistryKind = "binary"
	KindMixed  RegistryKind = "mixed"
)

// PackageInfo is a registry.json packages entry.
type PackageInfo struct {
	UUID   string `json:"uuid"`
	GitURL string `json:"giturl,omitempty"`
}

// Registry is registry.json — the authoritative package index (§4.2).
type Registry struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Name          string                 `json:"name"`
	UUID          string                 `json:"uuid"`
	GitURL        string                 `json:"giturl"`
	Kind          RegistryKind           `json:"kind"`
	Packages      map[string]PackageInfo `json:"packages"`
}

// Platform identifies an OS/arch target for a binary artifact (§4.5).
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// BinaryDepRef records a transitive dep's identity+buildKey for a binary (§4.5).
type BinaryDepRef struct {
	UUID     string `json:"uuid"`
	Version  string `json:"version"`
	BuildKey string `json:"buildKey"`
}

// ArtifactRef points at prebuilt bytes in an external artifact store (§4.5).
type ArtifactRef struct {
	URL  string `json:"url"`
	Hash string `json:"hash"`
	Size int64  `json:"size,omitempty"`
}

// BinaryArtifact is one prebuilt {platform,toolchain,config} of a version (§4.5).
type BinaryArtifact struct {
	Platform   Platform        `json:"platform"`
	Toolchain  string          `json:"toolchain"`
	Config     json.RawMessage `json:"config,omitempty"`
	BuildKey   string          `json:"buildKey"`
	Deps       []BinaryDepRef  `json:"deps,omitempty"`
	Artifact   ArtifactRef     `json:"artifact"`
	Descriptor json.RawMessage `json:"descriptor"`
}

// Specs is specs.json — immutable per-version registry metadata (§4.4, §4.5).
type Specs struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Name          string                     `json:"name"`
	UUID          string                     `json:"uuid"`
	Version       string                     `json:"version"`
	GitURL        string                     `json:"giturl,omitempty"` // source-fetch URL; omitted in binary-only
	Commit        string                     `json:"commit"`           // immutable identifier
	Tree          string                     `json:"tree,omitempty"`   // content hash (integrity + build-key input)
	Build         string                     `json:"build,omitempty"`
	Provides      []string                   `json:"provides,omitempty"`
	Deps          map[string]Dependency      `json:"deps"`
	Ext           map[string]json.RawMessage `json:"ext,omitempty"`
	Binaries      []BinaryArtifact           `json:"binaries,omitempty"`
}

// BuildListEntry is one resolved dependency in a build list.
type BuildListEntry struct {
	Name       string   `json:"name"`
	UUID       string   `json:"uuid"`
	Major      int      `json:"major"`
	Version    string   `json:"version"`
	GitURL     string   `json:"giturl,omitempty"`
	Commit     string   `json:"commit,omitempty"`
	Tree       string   `json:"tree,omitempty"`
	Build      string   `json:"build,omitempty"`
	Provides   []string `json:"provides,omitempty"`
	Develop    bool     `json:"develop,omitempty"`
	SourcePath string   `json:"sourcePath,omitempty"`
	DepKeys    []string `json:"depKeys,omitempty"` // resolved unit keys of this entry's direct deps
}

// BuildList is the MVS result: unit key "<uuid>@v<major>" -> resolved entry.
type BuildList struct {
	Dependencies map[string]BuildListEntry `json:"dependencies"`
}
