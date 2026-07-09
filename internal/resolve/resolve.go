// Package resolve implements Minimal Version Selection (§7).
//
// The algorithm is a visited-once traversal over direct-requirement edges
// (canonical MVS, as in cmd/go/internal/mvs) — NOT expansion of stored closures.
// It is a pure function over data: all I/O is behind the SpecLoader/DevOverlay
// interfaces so the resolver is exhaustively unit-testable without git or a
// filesystem (§16.1).
package resolve

import (
	"fmt"
	"sort"

	"cosm/internal/semver"
	"cosm/internal/types"
)

func depKeysOf(deps map[string]types.Dependency) []string {
	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SpecLoader loads immutable per-version specs for a dependency. The name
// locates the package (registry shard); implementations verify the uuid.
type SpecLoader interface {
	Load(name, uuid, version string) (types.Specs, error)
}

// DevOverlay reports whether a compatibility unit is under develop override
// (§7.4). When ok, the unit resolves to the working tree at sourcePath using
// the live manifest's deps, winning regardless of version.
type DevOverlay interface {
	Override(uuid string, major int) (man types.Manifest, sourcePath string, ok bool)
}

// Warning is a non-fatal advisory emitted during resolution (e.g. v0 bumps).
type Warning struct {
	Code    string
	Message string
}

type noDev struct{}

func (noDev) Override(string, int) (types.Manifest, string, bool) {
	return types.Manifest{}, "", false
}

// NoDev is a DevOverlay with no overrides.
var NoDev DevOverlay = noDev{}

type workItem struct {
	key     string // <uuid>@v<major>
	name    string
	uuid    string
	version string
}

type devInfo struct {
	man  types.Manifest
	path string
}

// Resolve computes the build list for root using MVS. loader supplies specs;
// dev supplies develop overrides (pass NoDev or nil for none). It returns the
// build list, any warnings, and an error (e.g. a missing version).
func Resolve(root *types.Manifest, loader SpecLoader, dev DevOverlay) (types.BuildList, []Warning, error) {
	return resolve(root, loader, dev, false)
}

// ResolveWithTests is Resolve but also seeds the root's test-only dependencies
// (§7.6). Test deps are non-transitive: only the root's are included, and a
// dependency's own testDeps are never followed (the resolver only traverses
// regular `deps` edges). A test dep's regular dependency closure IS pulled in.
func ResolveWithTests(root *types.Manifest, loader SpecLoader, dev DevOverlay) (types.BuildList, []Warning, error) {
	return resolve(root, loader, dev, true)
}

func resolve(root *types.Manifest, loader SpecLoader, dev DevOverlay, includeTests bool) (types.BuildList, []Warning, error) {
	if dev == nil {
		dev = NoDev
	}

	var work []workItem
	push := func(key string, d types.Dependency) error {
		uuid, _, err := semver.SplitUnitKey(key)
		if err != nil {
			return err
		}
		work = append(work, workItem{key: key, name: d.Name, uuid: uuid, version: d.Version})
		return nil
	}
	for key, d := range root.Deps {
		if err := push(key, d); err != nil {
			return types.BuildList{}, nil, err
		}
	}
	if includeTests {
		for key, d := range root.TestDeps {
			if err := push(key, d); err != nil {
				return types.BuildList{}, nil, err
			}
		}
	}

	selected := map[string]string{}       // key -> chosen (max) version, non-dev
	specsAt := map[string]types.Specs{}    // key\x00version -> specs
	seen := map[string]bool{}              // key\x00version already expanded
	devUnits := map[string]devInfo{}       // key -> dev override
	devExpanded := map[string]bool{}       // key -> dev deps already pushed
	requested := map[string][]string{}     // key -> every requested version (for v0 warning)

	for len(work) > 0 {
		it := work[len(work)-1]
		work = work[:len(work)-1]

		_, major, err := semver.SplitUnitKey(it.key)
		if err != nil {
			return types.BuildList{}, nil, err
		}
		requested[it.key] = append(requested[it.key], it.version)

		// Develop override wins regardless of version (§7.4).
		if man, path, ok := dev.Override(it.uuid, major); ok {
			if !devExpanded[it.key] {
				devExpanded[it.key] = true
				devUnits[it.key] = devInfo{man: man, path: path}
				for k, d := range man.Deps {
					if err := push(k, d); err != nil {
						return types.BuildList{}, nil, err
					}
				}
			}
			continue
		}

		// MVS max: keep the highest floor seen for this unit.
		if cur, has := selected[it.key]; !has || semver.Compare(it.version, cur) > 0 {
			selected[it.key] = it.version
		}

		// Expand each distinct (unit, version) exactly once — a lower version's
		// requirements can still raise a transitive floor (§7.1).
		vkey := it.key + "\x00" + it.version
		if seen[vkey] {
			continue
		}
		seen[vkey] = true

		sp, err := loader.Load(it.name, it.uuid, it.version)
		if err != nil {
			return types.BuildList{}, nil, err
		}
		specsAt[vkey] = sp
		for k, d := range sp.Deps {
			if err := push(k, d); err != nil {
				return types.BuildList{}, nil, err
			}
		}
	}

	bl := types.BuildList{Dependencies: make(map[string]types.BuildListEntry)}
	var warns []Warning

	// Develop-overridden units (win over any registry version).
	for key, di := range devUnits {
		uuid, major, _ := semver.SplitUnitKey(key)
		bl.Dependencies[key] = types.BuildListEntry{
			Name:       di.man.Name,
			UUID:       uuid,
			Major:      major,
			Version:    di.man.Version,
			Build:      di.man.Build,
			Provides:   di.man.Provides,
			Develop:    true,
			SourcePath: di.path,
			DepKeys:    depKeysOf(di.man.Deps),
		}
	}

	// Registry-resolved units.
	for key, ver := range selected {
		if _, isDev := devUnits[key]; isDev {
			continue
		}
		uuid, major, _ := semver.SplitUnitKey(key)
		sp := specsAt[key+"\x00"+ver]
		bl.Dependencies[key] = types.BuildListEntry{
			Name:     sp.Name,
			UUID:     uuid,
			Major:    major,
			Version:  ver,
			GitURL:   sp.GitURL,
			Commit:   sp.Commit,
			Tree:     sp.Tree,
			Build:    sp.Build,
			Provides: sp.Provides,
			DepKeys:  depKeysOf(sp.Deps),
		}
		// v0 policy: warn if a higher v0.y was selected than something requested (§6.3).
		if major == 0 {
			for _, rv := range requested[key] {
				if semver.Compare(rv, ver) < 0 {
					warns = append(warns, Warning{
						Code:    "W_V0_MINOR_BUMP",
						Message: fmt.Sprintf("%s: selected %s over requested %s (v0.x has no compatibility guarantee)", sp.Name, ver, rv),
					})
					break
				}
			}
		}
	}

	return bl, warns, nil
}
