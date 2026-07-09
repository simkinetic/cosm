package service

import (
	"fmt"
	"os"
	"path/filepath"

	"cosm/internal/develop"
	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// Develop clones a dependency into the depot's shared workspace (at a branch or
// tag) and enrolls the current project to use it (§7.4, §12.7). Returns the
// checkout path.
func (s *Service) Develop(projectDir, name string, major int, branch, tag string) (string, error) {
	_, bl, _, err := s.Resolve(projectDir)
	if err != nil {
		return "", err
	}
	var matches []types.BuildListEntry
	for _, e := range bl.Dependencies {
		if e.Name == name && (major < 0 || e.Major == major) {
			matches = append(matches, e)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: %s is not a (resolved) dependency", errs.ErrUsage, name)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("%w: %s spans multiple majors; use --major", errs.ErrUsage, name)
	}
	e := matches[0]
	if e.GitURL == "" {
		return "", fmt.Errorf("%w: no git URL recorded for %s", errs.ErrUsage, name)
	}

	devDir := s.D.DevUnit(e.Name, e.Major)
	ref, refKind := branch, "branch"
	if tag != "" {
		ref, refKind = tag, "tag"
	}
	if _, err := os.Stat(devDir); os.IsNotExist(err) {
		if err := os.MkdirAll(s.D.Dev(), 0o755); err != nil {
			return "", err
		}
		if err := s.Git.Clone(e.GitURL, devDir); err != nil {
			return "", err
		}
		if ref != "" {
			if err := s.Git.Checkout(devDir, ref); err != nil {
				return "", err
			}
		}
	}
	if ref == "" {
		ref, _ = s.Git.CurrentBranch(devDir)
		refKind = "branch"
	}

	if err := s.upsertWorkspace(types.WorkspaceEntry{
		Name: e.Name, UUID: e.UUID, Major: e.Major, GitURL: e.GitURL,
		Ref: ref, RefKind: refKind, Path: filepath.Join("dev", fmt.Sprintf("%s@v%d", e.Name, e.Major)),
	}); err != nil {
		return "", err
	}
	if err := s.enroll(projectDir, semver.UnitKey(e.UUID, e.Major)); err != nil {
		return "", err
	}
	return devDir, nil
}

// Free un-enrolls the current project from a develop unit; with purge it also
// removes the shared checkout (§12.7).
func (s *Service) Free(projectDir, name string, major int, purge bool) error {
	ws, err := manifest.LoadWorkspace(s.D.WorkspaceFile())
	if err != nil {
		return err
	}
	var matches []types.WorkspaceEntry
	for _, w := range ws.Entries {
		if w.Name == name && (major < 0 || w.Major == major) {
			matches = append(matches, w)
		}
	}
	if len(matches) == 0 {
		return fmt.Errorf("%w: %s is not under development", errs.ErrUsage, name)
	}
	if len(matches) > 1 {
		return fmt.Errorf("%w: %s spans multiple majors; use --major", errs.ErrUsage, name)
	}
	w := matches[0]
	key := semver.UnitKey(w.UUID, w.Major)
	if err := s.unenroll(projectDir, key); err != nil {
		return err
	}
	if purge {
		if err := os.RemoveAll(s.D.DevUnit(w.Name, w.Major)); err != nil {
			return err
		}
		var kept []types.WorkspaceEntry
		for _, e := range ws.Entries {
			if semver.UnitKey(e.UUID, e.Major) != key {
				kept = append(kept, e)
			}
		}
		ws.Entries = kept
		return manifest.SaveWorkspace(s.D.WorkspaceFile(), ws)
	}
	return nil
}

func (s *Service) upsertWorkspace(entry types.WorkspaceEntry) error {
	ws, err := manifest.LoadWorkspace(s.D.WorkspaceFile())
	if err != nil {
		return err
	}
	key := semver.UnitKey(entry.UUID, entry.Major)
	replaced := false
	for i, e := range ws.Entries {
		if semver.UnitKey(e.UUID, e.Major) == key {
			ws.Entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		ws.Entries = append(ws.Entries, entry)
	}
	return manifest.SaveWorkspace(s.D.WorkspaceFile(), ws)
}

func (s *Service) enroll(projectDir, key string) error {
	p := develop.EnrollmentPath(projectDir)
	e, err := manifest.LoadEnrollment(p)
	if err != nil {
		return err
	}
	if containsStr(e.Enrolled, key) {
		return nil
	}
	e.Enrolled = append(e.Enrolled, key)
	return manifest.SaveEnrollment(p, e)
}

func (s *Service) unenroll(projectDir, key string) error {
	p := develop.EnrollmentPath(projectDir)
	e, err := manifest.LoadEnrollment(p)
	if err != nil {
		return err
	}
	var kept []string
	for _, k := range e.Enrolled {
		if k != key {
			kept = append(kept, k)
		}
	}
	e.Enrolled = kept
	return manifest.SaveEnrollment(p, e)
}
