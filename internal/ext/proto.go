// Package ext defines the extension protocol (§9.3) and a Runner that invokes
// out-of-process extensions over JSON on stdin/stdout. Reference extensions in
// cmd/cosm-ext-* import these same types.
package ext

import (
	"encoding/json"

	"cosm/internal/types"
)

// Protocol is the current protocol major version.
const Protocol = 1

// Info is the response to the `info` verb.
type Info struct {
	Extension    string   `json:"extension"`
	Version      string   `json:"version"`
	Protocol     int      `json:"protocol"`
	Languages    []string `json:"languages,omitempty"`
	ToolchainID  string   `json:"toolchainId,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// PackageCtx describes the package being built or activated.
type PackageCtx struct {
	Name     string          `json:"name"`
	UUID     string          `json:"uuid"`
	Version  string          `json:"version"`
	Source   string          `json:"source"`
	Provides []string        `json:"provides,omitempty"`
	Ext      json.RawMessage `json:"ext,omitempty"`
}

// DepCtx is a dependency's identity, its install prefix on THIS machine, and its
// consumption descriptor. Descriptors use prefix-relative paths (so artifacts are
// relocatable); extensions join Prefix with the descriptor's relative paths.
type DepCtx struct {
	Name       string          `json:"name"`
	UUID       string          `json:"uuid"`
	Version    string          `json:"version"`
	Prefix     string          `json:"prefix"`
	Descriptor json.RawMessage `json:"descriptor"`
}

// BuildRequest is the `build` verb input.
type BuildRequest struct {
	Package  PackageCtx      `json:"package"`
	Prefix   string          `json:"prefix"`
	BuildKey string          `json:"buildKey"`
	Platform types.Platform  `json:"platform"`
	Config   json.RawMessage `json:"config,omitempty"`
	Deps     []DepCtx        `json:"deps"`
	Jobs     int             `json:"jobs"`
}

// BuildResponse is the `build` verb output.
type BuildResponse struct {
	Status     string          `json:"status"`
	Descriptor json.RawMessage `json:"descriptor"`
	Artifacts  []string        `json:"artifacts,omitempty"`
	Log        string          `json:"log,omitempty"`
}

// ActivateRequest is the `activate` verb input.
type ActivateRequest struct {
	Project  PackageCtx     `json:"project"`
	Platform types.Platform `json:"platform"`
	Deps     []DepCtx       `json:"deps"`
}

// ActivateResponse is the `activate` verb output. env is applied verbatim;
// prependPaths are prepended (OS path-list separator) to the inherited value.
type ActivateResponse struct {
	Env          map[string]string   `json:"env,omitempty"`
	PrependPaths map[string][]string `json:"prependPaths,omitempty"`
}

// ScaffoldRequest is the `scaffold` verb input.
type ScaffoldRequest struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	Version string `json:"version"`
	Dir     string `json:"dir"`
}

// ScaffoldResponse is the `scaffold` verb output. The extension creates the
// language-specific source layout and reports it in Files; it also declares the
// module namespaces (Provides) and any default ext config for the manifest, which
// the core writes into cosm.json (the extension does not write cosm.json itself).
type ScaffoldResponse struct {
	Files    []string                   `json:"files,omitempty"`
	Provides []string                   `json:"provides,omitempty"`
	Ext      map[string]json.RawMessage `json:"ext,omitempty"`
}

// TestRequest is the `test` verb input.
type TestRequest struct {
	Project  PackageCtx     `json:"project"`
	Platform types.Platform `json:"platform"`
	Deps     []DepCtx       `json:"deps"`
	Args     []string       `json:"args,omitempty"`
}

// TestResponse is the `test` verb output.
type TestResponse struct {
	Status string `json:"status"`
}
