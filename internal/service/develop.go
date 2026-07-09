package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cosm/internal/develop"
	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/types"
)

// Develop makes a dependency available for co-development and enrolls the current
// project to use it (§7.4, §12.7). It has three entry paths, in precedence order:
//
//  1. path != "": adopt a local checkout (possibly unpublished) into the workspace
//     by symlinking it under dev/<name>@v<major> — the bootstrap for a new sibling.
//  2. the unit is already in the workspace: just (re-)enroll this project, cloning
//     the shared checkout first if it is missing.
//  3. otherwise it must be a resolved dependency: clone it from its registry URL.
//
// Returns the checkout path.
func (s *Service) Develop(projectDir, name string, major int, branch, tag, path string) (string, error) {
	if path != "" {
		return s.developFromPath(projectDir, name, path)
	}

	// Already adopted in the workspace (covers unpublished siblings and enrolling a
	// second project) — don't require it to be a resolved dependency here.
	if we, ok, err := s.workspaceUnit(name, major); err != nil {
		return "", err
	} else if ok {
		devDir := s.D.DevUnit(we.Name, we.Major)
		if _, statErr := os.Lstat(devDir); os.IsNotExist(statErr) {
			if we.GitURL == "" {
				return "", fmt.Errorf("%w: %s is in the workspace but its checkout is missing and it has no git URL", errs.ErrUsage, name)
			}
			if err := s.cloneDevUnit(we, branch, tag); err != nil {
				return "", err
			}
		}
		if err := s.enroll(projectDir, semver.UnitKey(we.UUID, we.Major)); err != nil {
			return "", err
		}
		return devDir, nil
	}

	// A resolved dependency: clone it from its recorded registry URL.
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
		return "", fmt.Errorf("%w: %s is not a resolved dependency or a workspace package "+
			"(for a local package use 'cosm develop %s --path <dir>')", errs.ErrUsage, name, name)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("%w: %s spans multiple majors; use --major", errs.ErrUsage, name)
	}
	e := matches[0]
	if e.GitURL == "" {
		return "", fmt.Errorf("%w: no git URL recorded for %s", errs.ErrUsage, name)
	}

	we := types.WorkspaceEntry{Name: e.Name, UUID: e.UUID, Major: e.Major, GitURL: e.GitURL}
	devDir := s.D.DevUnit(e.Name, e.Major)
	if _, statErr := os.Stat(devDir); os.IsNotExist(statErr) {
		if err := s.cloneDevUnit(we, branch, tag); err != nil {
			return "", err
		}
	}
	ref, refKind := branch, "branch"
	if tag != "" {
		ref, refKind = tag, "tag"
	}
	if ref == "" {
		ref, _ = s.Git.CurrentBranch(devDir)
		refKind = "branch"
	}
	we.Ref, we.RefKind = ref, refKind
	we.Path = filepath.Join("dev", fmt.Sprintf("%s@v%d", e.Name, e.Major))
	if err := s.upsertWorkspace(we); err != nil {
		return "", err
	}
	if err := s.enroll(projectDir, semver.UnitKey(e.UUID, e.Major)); err != nil {
		return "", err
	}
	return devDir, nil
}

// developFromPath adopts a local package checkout (possibly never published) into
// the workspace by symlinking it under dev/<name>@v<major>, then enrolls the
// project. Identity (name/uuid/version) comes from the checkout's cosm.json.
func (s *Service) developFromPath(projectDir, name, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	man, err := manifest.LoadManifestFromDir(abs)
	if err != nil {
		return "", err
	}
	if name != "" && man.Name != name {
		return "", fmt.Errorf("%w: %s declares name %q, not %q", errs.ErrUsage, abs, man.Name, name)
	}
	major, err := semver.Major(man.Version)
	if err != nil {
		return "", err
	}
	devDir := s.D.DevUnit(man.Name, major)
	if _, statErr := os.Lstat(devDir); os.IsNotExist(statErr) {
		if err := os.MkdirAll(s.D.Dev(), 0o755); err != nil {
			return "", err
		}
		if err := os.Symlink(abs, devDir); err != nil {
			return "", err
		}
	}
	giturl, _ := s.Git.Run(abs, "config", "--get", "remote.origin.url")
	ref := ""
	if b, berr := s.Git.CurrentBranch(abs); berr == nil {
		ref = b
	}
	if err := s.upsertWorkspace(types.WorkspaceEntry{
		Name: man.Name, UUID: man.UUID, Major: major, GitURL: strings.TrimSpace(giturl),
		Ref: ref, RefKind: "branch", Local: true,
		Path: filepath.Join("dev", fmt.Sprintf("%s@v%d", man.Name, major)),
	}); err != nil {
		return "", err
	}
	if err := s.enroll(projectDir, semver.UnitKey(man.UUID, major)); err != nil {
		return "", err
	}
	return devDir, nil
}

// workspaceUnit finds the single workspace entry for name (optionally a specific
// major). ok is false when none match; an error is returned when several do.
func (s *Service) workspaceUnit(name string, major int) (types.WorkspaceEntry, bool, error) {
	ws, err := manifest.LoadWorkspace(s.D.WorkspaceFile())
	if err != nil {
		return types.WorkspaceEntry{}, false, err
	}
	var matches []types.WorkspaceEntry
	for _, w := range ws.Entries {
		if w.Name == name && (major < 0 || w.Major == major) {
			matches = append(matches, w)
		}
	}
	switch len(matches) {
	case 0:
		return types.WorkspaceEntry{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		return types.WorkspaceEntry{}, false, fmt.Errorf("%w: %s spans multiple majors in the workspace; use --major", errs.ErrUsage, name)
	}
}

func (s *Service) cloneDevUnit(we types.WorkspaceEntry, branch, tag string) error {
	devDir := s.D.DevUnit(we.Name, we.Major)
	if err := os.MkdirAll(s.D.Dev(), 0o755); err != nil {
		return err
	}
	if err := s.Git.Clone(we.GitURL, devDir); err != nil {
		return err
	}
	ref := branch
	if tag != "" {
		ref = tag
	}
	if ref != "" {
		return s.Git.Checkout(devDir, ref)
	}
	return nil
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
