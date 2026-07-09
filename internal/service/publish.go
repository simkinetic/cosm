package service

import (
	"fmt"
	"os"
	"runtime"

	"cosm/internal/artifact"
	"cosm/internal/errs"
	"cosm/internal/ext"
	"cosm/internal/manifest"
	"cosm/internal/materialize"
	"cosm/internal/semver"
	"cosm/internal/treehash"
	"cosm/internal/types"
)

// PublishOpts configures a binary publish.
type PublishOpts struct {
	Registry  string
	Store     string // directory for the artifact store
	BuildType string // Release | Debug
}

// Publish builds the current project for the local platform, uploads the built
// artifact to the store, and records a binary in the registry (§8.5).
func (s *Service) Publish(projectDir string, opts PublishOpts) (string, error) {
	if opts.Registry == "" || opts.Store == "" {
		return "", fmt.Errorf("%w: --registry and --store are required", errs.ErrUsage)
	}
	if opts.BuildType == "" {
		opts.BuildType = "Release"
	}
	m, err := manifest.LoadManifestFromDir(projectDir)
	if err != nil {
		return "", err
	}
	if m.Build == "" {
		return "", fmt.Errorf("%w: project has no build system", errs.ErrUsage)
	}

	platform := types.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	mat := &materialize.Materializer{
		D: s.D, Git: s.Git, Run: ext.NewRunner(s.D), Specs: s.Loader,
		Opt: materialize.Options{Platform: platform, BuildType: opts.BuildType, Jobs: runtime.NumCPU()},
	}

	_, bl, _, err := s.Resolve(projectDir)
	if err != nil {
		return "", err
	}
	built, rootBuilt, err := mat.BuildProject(m, projectDir, bl)
	if err != nil {
		return "", err
	}

	// Pack the built prefix and upload it.
	tgz, digest, size, err := artifact.Pack(rootBuilt.Prefix)
	if err != nil {
		return "", err
	}
	defer os.Remove(tgz)
	url, err := artifact.DirStore{Root: opts.Store}.Put(tgz, digest)
	if err != nil {
		return "", err
	}

	info, err := ext.NewRunner(s.D).Info(m.Build)
	if err != nil {
		return "", err
	}

	// Record the resolved direct-dep build keys for provenance.
	var depRefs []types.BinaryDepRef
	for key := range m.Deps {
		e, ok := bl.Dependencies[key]
		if !ok {
			continue
		}
		depRefs = append(depRefs, types.BinaryDepRef{UUID: e.UUID, Version: e.Version, BuildKey: built[key].BuildKey})
	}

	tree, err := treehash.Tree(projectDir)
	if err != nil {
		return "", err
	}
	commit := ""
	if c, err := s.Git.RevParseCommit(projectDir, "HEAD"); err == nil {
		commit = c
	}

	specs := types.Specs{
		SchemaVersion: types.SchemaVersion,
		Name:          m.Name, UUID: m.UUID, Version: m.Version,
		Commit: commit, Tree: tree, Build: m.Build, Provides: m.Provides, Deps: m.Deps,
	}
	binary := types.BinaryArtifact{
		Platform:   platform,
		Toolchain:  info.ToolchainID,
		Config:     (materialize.Options{BuildType: opts.BuildType}).ConfigJSON(),
		BuildKey:   rootBuilt.BuildKey,
		Deps:       depRefs,
		Artifact:   types.ArtifactRef{URL: url, Hash: digest, Size: size},
		Descriptor: rootBuilt.Descriptor,
	}
	if err := semver.ValidateExact(m.Version); err != nil {
		return "", fmt.Errorf("%w: project version %q must be vX.Y.Z to publish", errs.ErrUsage, m.Version)
	}
	if err := s.Reg.PublishBinary(opts.Registry, specs, binary); err != nil {
		return "", err
	}
	return m.Version, nil
}
