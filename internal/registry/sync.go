package registry

import (
	"fmt"
	"os"
	"sort"

	"cosm/internal/errs"
	"cosm/internal/manifest"
)

// SyncRegistry scans every registered package's git remote for new semver tags
// and registers any that are missing, then commits + pushes once (§12.6). It is
// intended to run server-side on a schedule (see docs/registry-ci.md) so that
// clients only ever read (`cosm update` = git pull), never push.
//
// Returns the versions added per package and non-fatal per-tag warnings. A tag
// whose cosm.json is malformed or whose version doesn't match the tag is skipped
// (as a warning), not fatal.
func (s *Service) SyncRegistry(regName string) (added map[string][]string, warnings []string, err error) {
	regDir := s.d.Registry(regName)
	reg, err := manifest.LoadRegistry(MetaFile(regDir))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	added = map[string][]string{}

	names := make([]string, 0, len(reg.Packages))
	for name := range reg.Packages {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		pkg := reg.Packages[name]
		mirror, merr := s.ensureMirror(pkg.UUID, pkg.GitURL)
		if merr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", name, merr))
			continue
		}
		if ferr := s.git.Fetch(mirror, "--tags"); ferr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: fetch: %v", name, ferr))
			continue
		}
		existing, _ := manifest.LoadVersions(VersionsFile(regDir, name))
		tags, terr := s.git.ListTags(mirror)
		if terr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: list tags: %v", name, terr))
			continue
		}
		for _, tag := range validSemverTags(tags) {
			if contains(existing, tag) {
				continue
			}
			if rerr := s.registerVersion(regDir, name, pkg.UUID, pkg.GitURL, tag, mirror); rerr != nil {
				warnings = append(warnings, fmt.Sprintf("%s@%s: %v", name, tag, rerr))
				continue
			}
			existing = append(existing, tag)
			added[name] = append(added[name], tag)
		}
	}

	total := 0
	for _, v := range added {
		total += len(v)
	}
	if total > 0 {
		if err := s.commitPush(regDir, fmt.Sprintf("Sync: registered %d new version(s)", total)); err != nil {
			return added, warnings, err
		}
	}
	return added, warnings, nil
}

// ensureMirror returns the local mirror path for a package, cloning it if absent.
func (s *Service) ensureMirror(uuid, giturl string) (string, error) {
	mirror := s.d.Mirror(uuid)
	if _, err := os.Stat(mirror); os.IsNotExist(err) {
		if giturl == "" {
			return "", fmt.Errorf("no git URL to fetch from")
		}
		if err := s.git.Clone(giturl, mirror); err != nil {
			return "", err
		}
	}
	return mirror, nil
}
