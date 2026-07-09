// Package buildkey computes the content hash that keys a built artifact (§8.3).
package buildkey

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"cosm/internal/types"
)

// Input is everything a build key depends on. Changing any field (including a
// transitive dep's build key) yields a different key, giving DAG-aware rebuilds.
type Input struct {
	Tree        string         // specs.tree, or a working-tree hash for dev/root nodes
	Platform    types.Platform // target os/arch
	ToolchainID string         // extension-reported (compiler id+version, etc.)
	Config      string         // canonical JSON of build type & options
	ExtID       string         // extension id
	ExtVersion  string         // extension version
	DepKeys     []string       // direct deps' build keys (order-independent)
}

// Compute returns the deterministic "sha256:<hex>" build key for in.
func Compute(in Input) string {
	deps := append([]string(nil), in.DepKeys...)
	sort.Strings(deps) // order-independent
	parts := []string{
		"cosm-buildkey-v1",
		in.Tree,
		in.Platform.OS, in.Platform.Arch,
		in.ToolchainID,
		in.Config,
		in.ExtID, in.ExtVersion,
	}
	parts = append(parts, deps...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
