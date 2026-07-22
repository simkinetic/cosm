// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"os"
	"path/filepath"
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
	// Commands whose positional args are names/versions/none — never files. Without
	// this, cobra returns ShellCompDirectiveDefault and the shell falls back to file
	// completion (so `cosm status <TAB>` would list the directory). Flag-value
	// completion (e.g. --path) is unaffected.
	for _, n := range []string{"setup", "init", "status", "add", "build", "env", "shell", "test", "release", "publish"} {
		if c := sub[n]; c != nil && c.ValidArgsFunction == nil {
			c.ValidArgsFunction = completeNoFile
		}
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
	// Registry subcommands.
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
		for _, n := range []string{"init", "clone"} {
			if c := rsub[n]; c != nil && c.ValidArgsFunction == nil {
				c.ValidArgsFunction = completeNoFile
			}
		}
	}
}

// completionHint returns a one-line, shell-aware instruction for enabling tab
// completion — printed by `cosm setup` since the shell script isn't auto-installed.
func completionHint() string {
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return "add to ~/.zshrc:  source <(cosm completion zsh)"
	case "bash":
		return "add to ~/.bashrc: source <(cosm completion bash)"
	case "fish":
		return "cosm completion fish > ~/.config/fish/completions/cosm.fish"
	default:
		return "see 'cosm completion --help'"
	}
}

// completeNoFile suppresses the shell's default file completion for a command that
// takes no completable positional args.
func completeNoFile(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
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
