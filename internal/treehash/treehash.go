// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

// Package treehash computes the deterministic content hash of a source tree
// (§6.4): sha256 over a canonical, sorted walk of path + mode + contents, with
// git metadata (.git) excluded. It is the integrity anchor and a build-key input.
package treehash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

type entry struct {
	rel  string
	kind byte // 'f' file, 'd' dir, 'l' symlink
	mode uint32
	data []byte
}

// Tree returns "sha256:<hex>" for the tree rooted at root. This is the integrity
// anchor recorded in registry specs and checked on fetch, so its definition must
// never change: every already-registered tree hash depends on it.
func Tree(root string) (string, error) { return walk(root, false) }

// Live hashes a working checkout (the project being built, or a develop/--source
// checkout) as a build-key input. It is Tree minus a top-level .cosm/, which holds
// cosm's own project-local state — env symlinks, develop enrollment, its
// .gitignore — and never source. Hashing it fed a loop: the .cosm/env link to the
// project's own build prefix is derived from this very hash, so each
// `cosm env`/`run`/`shell` re-pointed it, changed the tree, and forced a rebuild.
func Live(root string) (string, error) { return walk(root, true) }

func walk(root string, skipCosmState bool) (string, error) {
	var entries []entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if skipCosmState && d.IsDir() && rel == ".cosm" {
			return filepath.SkipDir
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			entries = append(entries, entry{rel: rel, kind: 'd', mode: uint32(info.Mode().Perm())})
		case info.Mode()&fs.ModeSymlink != 0:
			tgt, lerr := os.Readlink(p)
			if lerr != nil {
				return lerr
			}
			entries = append(entries, entry{rel: rel, kind: 'l', data: []byte(tgt)})
		case info.Mode().IsRegular():
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			entries = append(entries, entry{rel: rel, kind: 'f', mode: uint32(info.Mode().Perm()), data: b})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%c\x00%o\x00%d\x00", e.rel, e.kind, e.mode, len(e.data))
		h.Write(e.data)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
