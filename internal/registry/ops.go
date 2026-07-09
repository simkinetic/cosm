package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/uuid"

	"cosm/internal/depot"
	"cosm/internal/errs"
	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/semver"
	"cosm/internal/treehash"
	"cosm/internal/types"
)

// Service performs registry mutations (§12.5) atomically-per-commit with no
// push-on-read.
type Service struct {
	d   depot.Depot
	git gitx.Git
}

func NewService(d depot.Depot, g gitx.Git) *Service { return &Service{d: d, git: g} }

// InitRegistry clones an empty remote, writes registry.json, registers it, and
// pushes the initial commit (§12.5). kind is source, binary, or mixed.
func (s *Service) InitRegistry(name, giturl string, kind types.RegistryKind) error {
	if name == "" || giturl == "" {
		return fmt.Errorf("%w: registry name and giturl required", errs.ErrUsage)
	}
	if kind == "" {
		kind = types.KindSource
	}
	regDir := s.d.Registry(name)
	if _, err := os.Stat(regDir); err == nil {
		return fmt.Errorf("registry %q already exists locally", name)
	}
	if err := s.git.Clone(giturl, regDir); err != nil {
		return err
	}
	ents, err := os.ReadDir(regDir)
	if err != nil {
		os.RemoveAll(regDir)
		return err
	}
	for _, e := range ents {
		if e.Name() != ".git" {
			os.RemoveAll(regDir)
			return fmt.Errorf("remote %q is not empty (contains %s)", giturl, e.Name())
		}
	}
	reg := &types.Registry{
		Name: name, UUID: uuid.NewString(), GitURL: giturl,
		Kind: kind, Packages: map[string]types.PackageInfo{},
	}
	if err := manifest.SaveRegistry(MetaFile(regDir), reg); err != nil {
		os.RemoveAll(regDir)
		return err
	}
	if err := s.registerRef(types.RegistryRef{Name: name, UUID: reg.UUID, GitURL: giturl}); err != nil {
		os.RemoveAll(regDir)
		return err
	}
	return s.commitPush(regDir, "Initialize registry "+name)
}

func (s *Service) registerRef(ref types.RegistryRef) error {
	refs, _ := manifest.LoadRegistryRefs(s.d.RegistriesFile())
	for _, r := range refs {
		if r.Name == ref.Name {
			return fmt.Errorf("registry %q already registered", ref.Name)
		}
	}
	refs = append(refs, ref)
	return manifest.SaveRegistryRefs(s.d.RegistriesFile(), refs)
}

// AddPackage registers a package from giturl and records every released version
// not already present. It is idempotent: on a package that's already registered
// it simply picks up any new tags (a per-package sync). Returns the package name
// and the versions newly added.
func (s *Service) AddPackage(regName, giturl string) (string, []string, error) {
	regDir := s.d.Registry(regName)
	reg, err := manifest.LoadRegistry(MetaFile(regDir))
	if err != nil {
		return "", nil, fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	tmp := filepath.Join(s.d.Root, "mirrors", "tmp-add")
	os.RemoveAll(tmp)
	if err := s.git.Clone(giturl, tmp); err != nil {
		return "", nil, err
	}
	moved := false
	defer func() {
		if !moved {
			os.RemoveAll(tmp)
		}
	}()
	if err := s.git.Fetch(tmp, "--tags"); err != nil {
		return "", nil, err
	}
	head, err := manifest.LoadManifestFromDir(tmp)
	if err != nil {
		return "", nil, err
	}
	if err := validateManifest(head); err != nil {
		return "", nil, err
	}
	// Guard against a name collision with a different package.
	if existing, ok := reg.Packages[head.Name]; ok && existing.UUID != head.UUID {
		return "", nil, fmt.Errorf("%w: %q already registered with a different UUID", errs.ErrUsage, head.Name)
	}
	tags, err := s.git.ListTags(tmp)
	if err != nil {
		return "", nil, err
	}
	registered, _ := manifest.LoadVersions(VersionsFile(regDir, head.Name))
	var added []string
	for _, tag := range validSemverTags(tags) {
		if contains(registered, tag) {
			continue
		}
		if err := s.registerVersion(regDir, head.Name, head.UUID, giturl, tag, tmp); err != nil {
			return "", nil, err
		}
		added = append(added, tag)
	}

	_, known := reg.Packages[head.Name]
	reg.Packages[head.Name] = types.PackageInfo{UUID: head.UUID, GitURL: giturl}
	if err := manifest.SaveRegistry(MetaFile(regDir), reg); err != nil {
		return "", nil, err
	}
	// Keep the clone as the package mirror for later fetches.
	mirror := s.d.Mirror(head.UUID)
	os.RemoveAll(mirror)
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err == nil {
		if os.Rename(tmp, mirror) == nil {
			moved = true
		}
	}
	if !known || len(added) > 0 {
		msg := fmt.Sprintf("Add package %s (%d versions)", head.Name, len(added))
		if known {
			msg = fmt.Sprintf("Add %d version(s) to package %s", len(added), head.Name)
		}
		if err := s.commitPush(regDir, msg); err != nil {
			return "", nil, err
		}
	}
	return head.Name, added, nil
}

// registerVersion writes specs.json (commit + tree hash) and appends versions.json.
func (s *Service) registerVersion(regDir, name, pkgUUID, giturl, tag, clonePath string) error {
	commit, err := s.git.RevParseCommit(clonePath, tag)
	if err != nil {
		return err
	}
	data, err := s.git.ReadFileAtCommit(clonePath, commit, "cosm.json")
	if err != nil {
		return fmt.Errorf("read cosm.json at %s: %w", tag, err)
	}
	var m types.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse cosm.json at %s: %w", tag, err)
	}
	if m.Name != name || m.UUID != pkgUUID {
		return fmt.Errorf("identity mismatch at tag %s (got %s/%s)", tag, m.Name, m.UUID)
	}
	if m.Version != tag {
		return fmt.Errorf("tag %s but cosm.json version is %s", tag, m.Version)
	}
	exp := filepath.Join(s.d.Root, "mirrors", "tmp-export")
	os.RemoveAll(exp)
	if err := s.git.ArchiveCommit(clonePath, commit, exp); err != nil {
		return err
	}
	tree, terr := treehash.Tree(exp)
	os.RemoveAll(exp)
	if terr != nil {
		return terr
	}
	deps := m.Deps
	if deps == nil {
		deps = map[string]types.Dependency{}
	}
	specs := &types.Specs{
		SchemaVersion: types.SchemaVersion,
		Name:          name, UUID: pkgUUID, Version: tag, GitURL: giturl,
		Commit: commit, Tree: tree, Build: m.Build, Provides: m.Provides, Deps: deps, Ext: m.Ext,
	}
	if err := manifest.SaveSpecs(SpecsFile(regDir, name, tag), specs); err != nil {
		return err
	}
	vs, _ := manifest.LoadVersions(VersionsFile(regDir, name))
	if !contains(vs, tag) {
		vs = append(vs, tag)
		sortSemver(vs)
		if err := manifest.SaveVersions(VersionsFile(regDir, name), vs); err != nil {
			return err
		}
	}
	return nil
}

// PackageStatus summarizes a package for `cosm registry status`.
type PackageStatus struct {
	Name     string
	UUID     string
	GitURL   string
	Versions []string
}

// Status returns registry metadata and per-package versions (§12.5).
func (s *Service) Status(regName string) (types.Registry, []PackageStatus, error) {
	regDir := s.d.Registry(regName)
	reg, err := manifest.LoadRegistry(MetaFile(regDir))
	if err != nil {
		return types.Registry{}, nil, fmt.Errorf("%w: %s", errs.ErrRegistryNotFound, regName)
	}
	var out []PackageStatus
	for name, info := range reg.Packages {
		vs, _ := manifest.LoadVersions(VersionsFile(regDir, name))
		sortSemver(vs)
		out = append(out, PackageStatus{Name: name, UUID: info.UUID, GitURL: info.GitURL, Versions: vs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return *reg, out, nil
}

func (s *Service) commitPush(regDir, msg string) error {
	if err := s.git.Add(regDir, "."); err != nil {
		return err
	}
	if err := s.git.Commit(regDir, msg); err != nil {
		return err
	}
	branch, err := s.git.CurrentBranch(regDir)
	if err != nil {
		return err
	}
	return s.git.Push(regDir, branch)
}

func validateManifest(m *types.Manifest) error {
	if m.Name == "" {
		return fmt.Errorf("cosm.json missing name")
	}
	if m.UUID == "" {
		return fmt.Errorf("cosm.json missing uuid")
	}
	if err := semver.ValidateExact(m.Version); err != nil {
		return fmt.Errorf("cosm.json version invalid: %w", err)
	}
	return nil
}

// validSemverTags keeps only full vX.Y.Z identity tags (§6.1).
func validSemverTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if semver.ValidateExact(t) == nil {
			out = append(out, t)
		}
	}
	sortSemver(out)
	return out
}

func sortSemver(vs []string) {
	sort.Slice(vs, func(i, j int) bool { return semver.Compare(vs[i], vs[j]) < 0 })
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
