package registry

import (
	"fmt"
	"os"

	"cosm/internal/depot"
	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// Loader reads specs from the local registry clones. It satisfies
// resolve.SpecLoader and is the resolver's offline backend (§7.1).
type Loader struct {
	d depot.Depot
}

func NewLoader(d depot.Depot) *Loader { return &Loader{d: d} }

func (l *Loader) refs() ([]types.RegistryRef, error) {
	return manifest.LoadRegistryRefs(l.d.RegistriesFile())
}

// Load implements resolve.SpecLoader: find name in a registry (verifying uuid)
// and return its specs.json at version.
func (l *Loader) Load(name, uuid, version string) (types.Specs, error) {
	refs, err := l.refs()
	if err != nil {
		return types.Specs{}, err
	}
	for _, ref := range refs {
		regDir := l.d.Registry(ref.Name)
		reg, err := manifest.LoadRegistry(MetaFile(regDir))
		if err != nil {
			continue
		}
		pkg, ok := reg.Packages[name]
		if !ok || (uuid != "" && pkg.UUID != uuid) {
			continue
		}
		sp, err := manifest.LoadSpecs(SpecsFile(regDir, name, version))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return types.Specs{}, err
		}
		if sp.Version != version || (uuid != "" && sp.UUID != uuid) {
			continue
		}
		return *sp, nil
	}
	return types.Specs{}, fmt.Errorf("%w: %s@%s", errs.ErrPackageNotFound, name, version)
}

// Location is a package found in a registry (for `cosm add` selection).
type Location struct {
	Registry string
	Specs    types.Specs
}

// Find returns all registry locations providing name at version (or the latest
// version when version is ""). Multiple results mean the CLI must prompt.
func (l *Loader) Find(name, version string) ([]Location, error) {
	refs, err := l.refs()
	if err != nil {
		return nil, err
	}
	var locs []Location
	for _, ref := range refs {
		regDir := l.d.Registry(ref.Name)
		reg, err := manifest.LoadRegistry(MetaFile(regDir))
		if err != nil {
			continue
		}
		if _, ok := reg.Packages[name]; !ok {
			continue
		}
		v := version
		if v == "" {
			lv, err := l.Latest(regDir, name)
			if err != nil || lv == "" {
				continue
			}
			v = lv
		}
		sp, err := manifest.LoadSpecs(SpecsFile(regDir, name, v))
		if err != nil || sp.Version != v {
			continue
		}
		locs = append(locs, Location{Registry: ref.Name, Specs: *sp})
	}
	return locs, nil
}

// Versions returns the package's uuid, the registry it lives in, and its
// registered versions (from the first registry that has it).
func (l *Loader) Versions(name string) (uuid, regName string, versions []string, err error) {
	refs, err := l.refs()
	if err != nil {
		return "", "", nil, err
	}
	for _, ref := range refs {
		regDir := l.d.Registry(ref.Name)
		reg, err := manifest.LoadRegistry(MetaFile(regDir))
		if err != nil {
			continue
		}
		pkg, ok := reg.Packages[name]
		if !ok {
			continue
		}
		vs, _ := manifest.LoadVersions(VersionsFile(regDir, name))
		return pkg.UUID, ref.Name, vs, nil
	}
	return "", "", nil, fmt.Errorf("%w: %s", errs.ErrPackageNotFound, name)
}

// Latest returns the highest registered version of name in a registry dir.
func (l *Loader) Latest(regDir, name string) (string, error) {
	vs, err := manifest.LoadVersions(VersionsFile(regDir, name))
	if err != nil {
		return "", err
	}
	best := ""
	for _, v := range vs {
		if best == "" || semver.Compare(v, best) > 0 {
			best = v
		}
	}
	return best, nil
}
