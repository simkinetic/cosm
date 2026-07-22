package cli

import (
	"os"
	"sort"

	"github.com/spf13/cobra"

	"cosm/internal/manifest"
	"cosm/internal/types"
)

// wireCompletions attaches dynamic shell-completion (beyond cobra's built-in
// command/flag completion) for the arguments users most often tab through:
// dependency names and registry names. Enable it with `cosm completion <shell>`.
func wireCompletions(root *cobra.Command) {
	sub := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		sub[c.Name()] = c
	}
	// Commands whose first arg is a dependency name.
	for _, n := range []string{"rm", "upgrade", "downgrade", "develop", "free"} {
		if c := sub[n]; c != nil {
			c.ValidArgsFunction = completeDeps
		}
	}
	// The --registry flag takes a registry name.
	for _, n := range []string{"add", "publish"} {
		if c := sub[n]; c != nil {
			_ = c.RegisterFlagCompletionFunc("registry", completeRegistries)
		}
	}
	// `cosm update [registry]`.
	if c := sub["update"]; c != nil {
		c.ValidArgsFunction = completeRegistriesFirst
	}
	// Registry subcommands whose first arg is a registry name.
	if reg := sub["registry"]; reg != nil {
		rsub := map[string]*cobra.Command{}
		for _, c := range reg.Commands() {
			rsub[c.Name()] = c
		}
		for _, n := range []string{"status", "rm", "delete", "sync", "add"} {
			if c := rsub[n]; c != nil {
				c.ValidArgsFunction = completeRegistriesFirst
			}
		}
	}
}

// completeDeps suggests the current project's direct dependency names.
func completeDeps(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cwd, _ := os.Getwd()
	m, err := manifest.LoadManifestFromDir(cwd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]bool{}
	var out []string
	collect := func(deps map[string]types.Dependency) {
		for _, d := range deps {
			if !seen[d.Name] {
				seen[d.Name] = true
				out = append(out, d.Name)
			}
		}
	}
	collect(m.Deps)
	collect(m.TestDeps)
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeRegistriesFirst(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeRegistries(cmd, args, toComplete)
}

// completeRegistries suggests the locally-known registry names.
func completeRegistries(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	d, err := openDepot()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	refs, _ := manifest.LoadRegistryRefs(d.RegistriesFile())
	var out []string
	for _, r := range refs {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}
