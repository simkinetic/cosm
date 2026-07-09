// Package materialize turns a resolved build list into built artifacts and an
// activation environment (§8): fetch/export/verify sources, topological build
// via extensions with a build-key artifact cache, binary-registry consumption
// (§8.5), and env assembly.
package materialize

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cosm/internal/artifact"
	"cosm/internal/buildkey"
	"cosm/internal/depot"
	"cosm/internal/errs"
	"cosm/internal/ext"
	"cosm/internal/gitx"
	"cosm/internal/treehash"
	"cosm/internal/types"
)

// SpecLoader loads a registry version's specs (for binary-artifact lookup).
type SpecLoader interface {
	Load(name, uuid, version string) (types.Specs, error)
}

// Options controls a build.
type Options struct {
	Platform  types.Platform
	BuildType string // "Release" | "Debug"
	Jobs      int
}

func (o Options) configJSON() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"buildType":%q}`, o.BuildType))
}
func (o Options) configCanonical() string { return fmt.Sprintf(`{"buildType":"%s"}`, o.BuildType) }

// ConfigJSON returns the build config as JSON (exported for publishers).
func (o Options) ConfigJSON() json.RawMessage { return o.configJSON() }

// Materializer performs materialization and building.
type Materializer struct {
	D     depot.Depot
	Git   gitx.Git
	Run   *ext.Runner
	Specs SpecLoader // optional: enables binary-registry consumption
	Opt   Options
	info  map[string]ext.Info
}

// Built is the result of building (or downloading) one node.
type Built struct {
	UnitKey    string
	BuildKey   string
	Prefix     string
	Descriptor json.RawMessage
}

func (m *Materializer) infoFor(id string) (ext.Info, error) {
	if m.info == nil {
		m.info = map[string]ext.Info{}
	}
	if i, ok := m.info[id]; ok {
		return i, nil
	}
	i, err := m.Run.Info(id)
	if err != nil {
		return ext.Info{}, err
	}
	m.info[id] = i
	return i, nil
}

// EnsureSource returns the source directory for a build-list entry, fetching and
// verifying it if necessary (§8.1). Develop nodes use their working tree.
func (m *Materializer) EnsureSource(e types.BuildListEntry) (string, error) {
	if e.Develop {
		return e.SourcePath, nil
	}
	src := m.D.Source(e.UUID, e.Commit)
	marker := src + ".ok"
	if _, err := os.Stat(marker); err == nil {
		return src, nil
	}
	if e.GitURL == "" {
		return "", fmt.Errorf("%w: no source for %s@%s", errs.ErrNoBinary, e.Name, e.Version)
	}
	mirror := m.D.Mirror(e.UUID)
	if _, err := os.Stat(mirror); os.IsNotExist(err) {
		if err := m.Git.Clone(e.GitURL, mirror); err != nil {
			return "", err
		}
	}
	os.RemoveAll(src)
	if err := m.Git.ArchiveCommit(mirror, e.Commit, src); err != nil {
		return "", err
	}
	if e.Tree != "" {
		got, err := treehash.Tree(src)
		if err != nil {
			return "", err
		}
		if got != e.Tree {
			os.RemoveAll(src)
			return "", fmt.Errorf("%w: %s@%s tree %s != recorded %s", errs.ErrIntegrityMismatch, e.Name, e.Version, got, e.Tree)
		}
	}
	_ = os.WriteFile(marker, []byte(e.Tree+"\n"), 0o644)
	return src, nil
}

// BuildAll builds every node in the build list in topological order.
func (m *Materializer) BuildAll(bl types.BuildList) (map[string]Built, error) {
	order, err := topoOrder(bl)
	if err != nil {
		return nil, err
	}
	built := map[string]Built{}
	for _, key := range order {
		e := bl.Dependencies[key]
		if e.Build == "" {
			return nil, fmt.Errorf("%w: dependency %s has no build system", errs.ErrUsage, e.Name)
		}
		b, err := m.buildNode(e, built, bl)
		if err != nil {
			return nil, err
		}
		b.UnitKey = key
		built[key] = b
	}
	return built, nil
}

// BuildProject builds the dependency graph and the root project, returning the
// dep results and the root's own Built (used by `cosm publish`).
func (m *Materializer) BuildProject(root *types.Manifest, projectDir string, bl types.BuildList) (map[string]Built, Built, error) {
	built, err := m.BuildAll(bl)
	if err != nil {
		return nil, Built{}, err
	}
	if root.Build == "" {
		return built, Built{}, nil
	}
	rootDepKeys := make([]string, 0, len(root.Deps))
	for k := range root.Deps {
		rootDepKeys = append(rootDepKeys, k)
	}
	rootEntry := types.BuildListEntry{
		Name: root.Name, UUID: root.UUID, Version: root.Version, Build: root.Build,
		Provides: root.Provides, Develop: true, SourcePath: projectDir, DepKeys: rootDepKeys,
	}
	rootBuilt, err := m.buildNode(rootEntry, built, bl)
	if err != nil {
		return nil, Built{}, err
	}
	return built, rootBuilt, nil
}

// EnsureBuilt builds the dependency graph and the root project.
func (m *Materializer) EnsureBuilt(root *types.Manifest, projectDir string, bl types.BuildList) (map[string]Built, error) {
	built, _, err := m.BuildProject(root, projectDir, bl)
	return built, err
}

func (m *Materializer) buildNode(e types.BuildListEntry, built map[string]Built, bl types.BuildList) (Built, error) {
	info, err := m.infoFor(e.Build)
	if err != nil {
		return Built{}, err
	}

	tree := e.Tree
	if e.Develop || tree == "" {
		if tree, err = treehash.Tree(e.SourcePath); err != nil {
			return Built{}, err
		}
	}
	seg := e.Commit
	if e.Develop || seg == "" {
		seg = "wt-" + shortHash(tree)
	}

	var deps []ext.DepCtx
	var depBKs []string
	for _, dk := range e.DepKeys {
		b, ok := built[dk]
		if !ok {
			continue
		}
		de := bl.Dependencies[dk]
		deps = append(deps, ext.DepCtx{Name: de.Name, UUID: de.UUID, Version: de.Version, Prefix: b.Prefix, Descriptor: b.Descriptor})
		depBKs = append(depBKs, b.BuildKey)
	}

	bk := buildkey.Compute(buildkey.Input{
		Tree: tree, Platform: m.Opt.Platform, ToolchainID: info.ToolchainID,
		Config: m.Opt.configCanonical(), ExtID: info.Extension, ExtVersion: info.Version, DepKeys: depBKs,
	})
	buildDir := m.D.Build(e.UUID, seg, bk)
	prefix := filepath.Join(buildDir, "artifacts")
	descPath := filepath.Join(buildDir, "descriptor.json")

	if data, err := os.ReadFile(descPath); err == nil { // artifact cache hit
		return Built{BuildKey: bk, Prefix: prefix, Descriptor: data}, nil
	}

	// Binary-registry consumption: if a matching prebuilt artifact exists, use it.
	if !e.Develop && m.Specs != nil {
		if sp, err := m.Specs.Load(e.Name, e.UUID, e.Version); err == nil {
			for _, b := range sp.Binaries {
				if b.BuildKey == bk && b.Platform.OS == m.Opt.Platform.OS && b.Platform.Arch == m.Opt.Platform.Arch {
					if err := m.consumeBinary(b, prefix); err != nil {
						return Built{}, err
					}
					_ = os.WriteFile(descPath, normalizeDescriptor(b.Descriptor), 0o644)
					writeMeta(buildDir, bk, m.Opt, e.Name, e.Version, depBKs, "binary")
					return Built{BuildKey: bk, Prefix: prefix, Descriptor: b.Descriptor}, nil
				}
			}
		}
	}

	// Source build.
	src := e.SourcePath
	if !e.Develop {
		if src, err = m.EnsureSource(e); err != nil {
			return Built{}, err
		}
	}
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		return Built{}, err
	}
	resp, err := m.Run.Build(e.Build, ext.BuildRequest{
		Package:  ext.PackageCtx{Name: e.Name, UUID: e.UUID, Version: e.Version, Source: src, Provides: e.Provides},
		Prefix:   prefix,
		BuildKey: bk,
		Platform: m.Opt.Platform,
		Config:   m.Opt.configJSON(),
		Deps:     deps,
		Jobs:     m.Opt.Jobs,
	})
	if err != nil {
		os.RemoveAll(buildDir)
		return Built{}, &errs.BuildFailedError{Package: e.Name, Phase: "build", LogPath: resp.Log, Err: err}
	}
	if err := os.WriteFile(descPath, normalizeDescriptor(resp.Descriptor), 0o644); err != nil {
		return Built{}, err
	}
	writeMeta(buildDir, bk, m.Opt, e.Name, e.Version, depBKs, "source")
	return Built{BuildKey: bk, Prefix: prefix, Descriptor: resp.Descriptor}, nil
}

func (m *Materializer) consumeBinary(b types.BinaryArtifact, prefix string) error {
	tmp := filepath.Join(os.TempDir(), "cosm-art-"+shortHash(b.Artifact.Hash)+".tar.gz")
	defer os.Remove(tmp)
	if err := artifact.Fetch(b.Artifact.URL, tmp); err != nil {
		return err
	}
	if err := artifact.VerifySha(tmp, b.Artifact.Hash); err != nil {
		return fmt.Errorf("%w: %v", errs.ErrIntegrityMismatch, err)
	}
	if err := os.MkdirAll(prefix, 0o755); err != nil {
		return err
	}
	return artifact.Unpack(tmp, prefix)
}

func normalizeDescriptor(d json.RawMessage) []byte {
	if len(d) == 0 {
		return []byte("{}")
	}
	return d
}

func writeMeta(buildDir, bk string, opt Options, name, version string, depBKs []string, source string) {
	meta := map[string]any{
		"buildKey": bk, "platform": opt.Platform, "buildType": opt.BuildType,
		"name": name, "version": version, "depBuildKeys": depBKs, "source": source,
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(filepath.Join(buildDir, "meta.json"), data, 0o644)
}

// Activate assembles the environment for the root project (§8.4). rootBuilt is
// the root project's own build; its artifacts (e.g. a C++ binary) are included
// so `cosm run -- <app>` finds them.
func (m *Materializer) Activate(root *types.Manifest, projectDir string, bl types.BuildList, built map[string]Built, rootBuilt Built) (ext.ActivateResponse, error) {
	if root.Build == "" {
		return ext.ActivateResponse{}, fmt.Errorf("%w: project has no build system (set 'build' in cosm.json)", errs.ErrUsage)
	}
	var deps []ext.DepCtx
	for key, e := range bl.Dependencies {
		b := built[key]
		deps = append(deps, ext.DepCtx{Name: e.Name, UUID: e.UUID, Version: e.Version, Prefix: b.Prefix, Descriptor: b.Descriptor})
	}
	if rootBuilt.Prefix != "" {
		deps = append(deps, ext.DepCtx{Name: root.Name, UUID: root.UUID, Version: root.Version, Prefix: rootBuilt.Prefix, Descriptor: rootBuilt.Descriptor})
	}
	return m.Run.Activate(root.Build, ext.ActivateRequest{
		Project:  ext.PackageCtx{Name: root.Name, UUID: root.UUID, Version: root.Version, Source: projectDir, Provides: root.Provides},
		Platform: m.Opt.Platform,
		Deps:     deps,
	})
}

func topoOrder(bl types.BuildList) ([]string, error) {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := map[string]int{}
	var order []string
	var visit func(string) error
	visit = func(k string) error {
		switch state[k] {
		case gray:
			return fmt.Errorf("%w: dependency cycle at %s", errs.ErrResolutionConflict, k)
		case black:
			return nil
		}
		state[k] = gray
		for _, dk := range bl.Dependencies[k].DepKeys {
			if _, ok := bl.Dependencies[dk]; ok {
				if err := visit(dk); err != nil {
					return err
				}
			}
		}
		state[k] = black
		order = append(order, k)
		return nil
	}
	keys := make([]string, 0, len(bl.Dependencies))
	for k := range bl.Dependencies {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		if err := visit(k); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func shortHash(tree string) string {
	h := strings.TrimPrefix(tree, "sha256:")
	if len(h) > 16 {
		return h[:16]
	}
	if h == "" {
		return "none"
	}
	return h
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// AssembleEnv applies an activation response onto a base environment (KEY=VALUE
// slice). env wins; prependPaths prepend with the OS path-list separator (§8.4).
func AssembleEnv(base []string, resp ext.ActivateResponse) []string {
	m := map[string]string{}
	var order []string
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k := kv[:i]
			if _, seen := m[k]; !seen {
				order = append(order, k)
			}
			m[k] = kv[i+1:]
		}
	}
	set := func(k, v string) {
		if _, seen := m[k]; !seen {
			order = append(order, k)
		}
		m[k] = v
	}
	for k, v := range resp.Env {
		set(k, v)
	}
	sep := string(os.PathListSeparator)
	for k, vals := range resp.PrependPaths {
		joined := strings.Join(vals, sep)
		if ex, ok := m[k]; ok && ex != "" {
			joined = joined + sep + ex
		}
		set(k, joined)
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+m[k])
	}
	return out
}
