// Package manifest loads and saves cosm's JSON documents (§4), stamping the
// schema version and normalizing nil maps.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cosm/internal/errs"
	"cosm/internal/types"
)

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadManifest reads a cosm.json. A missing file yields ErrNoProject.
func LoadManifest(path string) (*types.Manifest, error) {
	var m types.Manifest
	if err := readJSON(path, &m); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", errs.ErrNoProject, path)
		}
		return nil, err
	}
	if m.Deps == nil {
		m.Deps = map[string]types.Dependency{}
	}
	return &m, nil
}

// LoadManifestFromDir reads <dir>/cosm.json.
func LoadManifestFromDir(dir string) (*types.Manifest, error) {
	return LoadManifest(filepath.Join(dir, "cosm.json"))
}

func SaveManifest(path string, m *types.Manifest) error {
	if m.SchemaVersion == 0 {
		m.SchemaVersion = types.SchemaVersion
	}
	return writeJSON(path, m)
}

func LoadRegistry(path string) (*types.Registry, error) {
	var r types.Registry
	if err := readJSON(path, &r); err != nil {
		return nil, err
	}
	if r.Packages == nil {
		r.Packages = map[string]types.PackageInfo{}
	}
	if r.Kind == "" {
		r.Kind = types.KindSource
	}
	return &r, nil
}

func SaveRegistry(path string, r *types.Registry) error {
	if r.SchemaVersion == 0 {
		r.SchemaVersion = types.SchemaVersion
	}
	return writeJSON(path, r)
}

func LoadSpecs(path string) (*types.Specs, error) {
	var s types.Specs
	if err := readJSON(path, &s); err != nil {
		return nil, err
	}
	if s.Deps == nil {
		s.Deps = map[string]types.Dependency{}
	}
	return &s, nil
}

func SaveSpecs(path string, s *types.Specs) error {
	if s.SchemaVersion == 0 {
		s.SchemaVersion = types.SchemaVersion
	}
	return writeJSON(path, s)
}

func LoadVersions(path string) ([]string, error) {
	var v []string
	if err := readJSON(path, &v); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return v, nil
}

func SaveVersions(path string, versions []string) error {
	if versions == nil {
		versions = []string{}
	}
	return writeJSON(path, versions)
}

func LoadRegistryRefs(path string) ([]types.RegistryRef, error) {
	var refs []types.RegistryRef
	if err := readJSON(path, &refs); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return refs, nil
}

func SaveRegistryRefs(path string, refs []types.RegistryRef) error {
	if refs == nil {
		refs = []types.RegistryRef{}
	}
	return writeJSON(path, refs)
}

func LoadWorkspace(path string) (*types.Workspace, error) {
	var w types.Workspace
	if err := readJSON(path, &w); err != nil {
		if os.IsNotExist(err) {
			return &types.Workspace{SchemaVersion: types.SchemaVersion}, nil
		}
		return nil, err
	}
	return &w, nil
}

func SaveWorkspace(path string, w *types.Workspace) error {
	if w.SchemaVersion == 0 {
		w.SchemaVersion = types.SchemaVersion
	}
	return writeJSON(path, w)
}

func LoadEnrollment(path string) (*types.Enrollment, error) {
	var e types.Enrollment
	if err := readJSON(path, &e); err != nil {
		if os.IsNotExist(err) {
			return &types.Enrollment{SchemaVersion: types.SchemaVersion}, nil
		}
		return nil, err
	}
	return &e, nil
}

func SaveEnrollment(path string, e *types.Enrollment) error {
	if e.SchemaVersion == 0 {
		e.SchemaVersion = types.SchemaVersion
	}
	return writeJSON(path, e)
}

// LoadBuildList / SaveBuildList handle the local resolved build list and the
// closure cache (§7.3).
func LoadBuildList(path string) (*types.BuildList, error) {
	var b types.BuildList
	if err := readJSON(path, &b); err != nil {
		return nil, err
	}
	if b.Dependencies == nil {
		b.Dependencies = map[string]types.BuildListEntry{}
	}
	return &b, nil
}

func SaveBuildList(path string, b *types.BuildList) error {
	return writeJSON(path, b)
}
