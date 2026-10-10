// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"cosm/internal/treehash"
	"cosm/internal/types"
)

// BuildListHash is a stable digest of a resolved build list, for scripts that must
// notice when anything below a project changes (a regression cache, say). Each unit
// contributes its key, name, version, build system, and content identity: the
// registered tree (or commit) for a released version, or a fresh hash of the
// checkout for a develop or --source override — whose version string stays put
// while its content changes. The project's own sources are not included, and no
// machine-specific path is, so the same resolution hashes identically anywhere.
func BuildListHash(bl types.BuildList) (string, error) {
	keys := make([]string, 0, len(bl.Dependencies))
	for k := range bl.Dependencies {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	fmt.Fprintln(h, "cosm-buildlist-v1")
	for _, k := range keys {
		e := bl.Dependencies[k]
		id := e.Tree
		if id == "" {
			id = "commit:" + e.Commit
		}
		if e.Develop {
			t, err := treehash.Live(e.SourcePath)
			if err != nil {
				return "", fmt.Errorf("hash develop checkout of %s: %w", e.Name, err)
			}
			id = "develop:" + t
		}
		fmt.Fprintf(h, "%s\t%s\t%s\t%s\t%s\n", k, e.Name, e.Version, e.Build, id)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
