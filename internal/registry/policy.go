// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package registry

import (
	"encoding/json"
	"sort"

	"cosm/internal/semver"
	"cosm/internal/types"
)

// registrable reports whether tag may be registered under a package's policy, and
// if not, why.
func registrable(p *types.PackagePolicy, tag string) (bool, string) {
	if contains(p.Excluded, tag) {
		return false, "removed earlier (registry rm); register it again with --tag " + tag
	}
	if p.Since != "" && semver.Compare(tag, p.Since) < 0 {
		return false, "below the registration floor " + p.Since
	}
	return true, ""
}

// policyKey is a canonical form of a policy, to tell whether it changed.
func policyKey(p *types.PackagePolicy) string {
	c := *p
	c.Excluded = append([]string(nil), p.Excluded...)
	sort.Strings(c.Excluded)
	b, _ := json.Marshal(c) // map keys marshal sorted
	return string(b)
}

func removeStr(xs []string, x string) []string {
	var out []string
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
