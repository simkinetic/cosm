// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"sort"
	"strings"

	"cosm/internal/service"
	"cosm/internal/types"
)

// dryRunFailed makes `cosm release --dry-run` exit non-zero when the preflight
// fails (so CI can gate on it). The problems were already printed with the plan,
// so the message stays short; it unwraps to them for the exit code.
type dryRunFailed struct{ problems []error }

func (e *dryRunFailed) Error() string {
	return fmt.Sprintf("the release would be refused: %d preflight problem(s) above; nothing was changed", len(e.problems))
}

func (e *dryRunFailed) Unwrap() []error { return e.problems }

// formatReleasePlan renders a release plan: the preflight, the git steps, and the
// registry entry that registering the new tag will write.
func formatReleasePlan(p *service.ReleasePlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Release plan for %s: %s -> %s (dry run: nothing is changed)\n\n", p.Name, p.From, p.To)

	b.WriteString("Preflight\n")
	for _, c := range p.Checks {
		if c.Err == nil {
			fmt.Fprintf(&b, "  ok    %s\n", c.Label)
			continue
		}
		fmt.Fprintf(&b, "  FAIL  %s\n", c.Label)
		fmt.Fprintf(&b, "        %s\n", strings.ReplaceAll(c.Err.Error(), "\n", "\n        "))
	}
	if len(p.Warnings) > 0 {
		b.WriteString("\nWarnings\n")
		for _, w := range p.Warnings {
			fmt.Fprintf(&b, "  warn  %s\n", w)
		}
	}

	branch, remote := p.Branch, p.Remote
	if branch == "" {
		branch = "<branch>"
	}
	if remote == "" {
		remote = "<no origin remote>"
	}
	verb := "Would do"
	if len(p.Problems()) > 0 {
		verb = "Would do, once the problems above are fixed"
	}
	fmt.Fprintf(&b, "\n%s\n", verb)
	fmt.Fprintf(&b, "  1. set \"version\": %q in cosm.json and commit it as \"Release %s\"\n", p.To, p.To)
	fmt.Fprintf(&b, "  2. create tag %s\n", p.To)
	fmt.Fprintf(&b, "  3. push branch %s, then tag %s, to origin (%s)\n", branch, p.To, remote)

	b.WriteString("\nRegistry entry written when the tag is registered (cosm registry add, or the registry's sync)\n")
	m := p.Released
	fmt.Fprintf(&b, "  %s %s  uuid %s", m.Name, m.Version, m.UUID)
	if m.Build != "" {
		fmt.Fprintf(&b, "  build %s", m.Build)
	}
	if m.License != "" {
		fmt.Fprintf(&b, "  license %s", m.License)
	}
	b.WriteString("\n")
	if len(m.Provides) > 0 {
		fmt.Fprintf(&b, "  provides: %s\n", strings.Join(m.Provides, ", "))
	}
	if deps := depList(m.Deps); deps != "" {
		fmt.Fprintf(&b, "  deps:     %s\n", deps)
	} else {
		b.WriteString("  deps:     (none)\n")
	}
	if len(m.TestDeps) > 0 {
		fmt.Fprintf(&b, "  (test deps are not recorded: %s)\n", depList(m.TestDeps))
	}
	b.WriteString("  commit and tree: those of the release commit\n")
	if len(p.Registries) > 0 {
		fmt.Fprintf(&b, "  already listed in: %s\n", strings.Join(p.Registries, ", "))
	} else {
		fmt.Fprintf(&b, "  not in any registry yet: register it with cosm registry add <registry> %s\n", remote)
	}
	return b.String()
}

func depList(deps map[string]types.Dependency) string {
	var out []string
	for _, d := range deps {
		out = append(out, fmt.Sprintf("%s >= %s", d.Name, d.Version))
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
