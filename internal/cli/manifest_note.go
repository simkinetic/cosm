// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"

	"cosm/internal/types"
)

// unknownKeysNote describes cosm.json keys cosm doesn't use. They are preserved on
// every rewrite, but a misspelt key ("licence", "dpes") would otherwise be ignored
// without a word, so name them — with a suggestion when one is a near miss.
func unknownKeysNote(m *types.Manifest) string {
	keys := m.UnknownKeys()
	if len(keys) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "note: cosm.json keys not used by cosm (kept as-is): %s\n", strings.Join(keys, ", "))
	for _, k := range keys {
		if s := nearKey(k); s != "" {
			fmt.Fprintf(&b, "      %q looks like a misspelling of %q\n", k, s)
		}
	}
	return b.String()
}

// nearKey returns the modelled key closest to k when it is a likely typo: one edit
// away (transpositions count as one), or two for longer keys.
func nearKey(k string) string {
	best, bestD := "", 3
	for _, known := range types.ManifestKeys() {
		if d := osaDistance(strings.ToLower(k), strings.ToLower(known)); d < bestD {
			best, bestD = known, d
		}
	}
	if bestD == 1 || (bestD == 2 && len(k) >= 6) {
		return best
	}
	return ""
}

// osaDistance is the optimal-string-alignment edit distance (Levenshtein plus
// adjacent transpositions).
func osaDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
