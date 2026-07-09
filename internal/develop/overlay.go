// Package develop builds the resolve.DevOverlay from the depot's shared
// workspace and a project's per-project enrollment (§7.4). Resolution stays
// offline: checkouts are materialized separately (by `cosm develop` or an
// ensure step), not inside Override.
package develop

import (
	"os"
	"path/filepath"

	"cosm/internal/depot"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

type entry struct {
	man  types.Manifest
	path string
}

// Overlay satisfies resolve.DevOverlay.
type Overlay struct {
	entries map[string]entry
}

// Override reports whether unit uuid@major is develop-overridden by this project.
func (o *Overlay) Override(uuid string, major int) (types.Manifest, string, bool) {
	if o == nil {
		return types.Manifest{}, "", false
	}
	e, ok := o.entries[semver.UnitKey(uuid, major)]
	if !ok {
		return types.Manifest{}, "", false
	}
	return e.man, e.path, true
}

// Missing is an enrolled unit whose shared checkout is absent (needs cloning).
type Missing struct {
	Key   string
	Entry types.WorkspaceEntry // zero if the unit is not yet in the workspace
	InWS  bool
}

// EnrollmentPath is a project's git-ignored develop.json.
func EnrollmentPath(projectDir string) string {
	return filepath.Join(projectDir, ".cosm", "develop.json")
}

// Build assembles the overlay for projectDir from the depot workspace and the
// project's enrollment. Enrolled units whose checkout is missing/unloadable are
// returned in `missing` for the caller to materialize before relying on them.
func Build(d depot.Depot, projectDir string) (*Overlay, []Missing, error) {
	enr, err := manifest.LoadEnrollment(EnrollmentPath(projectDir))
	if err != nil {
		return nil, nil, err
	}
	ws, err := manifest.LoadWorkspace(d.WorkspaceFile())
	if err != nil {
		return nil, nil, err
	}
	byKey := map[string]types.WorkspaceEntry{}
	for _, e := range ws.Entries {
		byKey[semver.UnitKey(e.UUID, e.Major)] = e
	}
	ov := &Overlay{entries: map[string]entry{}}
	var missing []Missing
	for _, key := range enr.Enrolled {
		we, inWS := byKey[key]
		if !inWS {
			missing = append(missing, Missing{Key: key})
			continue
		}
		path := d.DevUnit(we.Name, we.Major)
		// A path-adopted unit is a symlink to the working checkout; resolve it so
		// the source path is the real directory (treehash/build won't descend a
		// symlinked root). Only touch actual symlinks, so clone-based units keep
		// their exact dev path.
		if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			if real, rerr := filepath.EvalSymlinks(path); rerr == nil {
				path = real
			}
		}
		man, err := manifest.LoadManifestFromDir(path)
		if err != nil {
			missing = append(missing, Missing{Key: key, Entry: we, InWS: true})
			continue
		}
		ov.entries[key] = entry{man: *man, path: path}
	}
	return ov, missing, nil
}
