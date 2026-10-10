// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/registry"
	"cosm/internal/service"
	"cosm/internal/types"
)

func registryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Manage package registries",
		RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(
		registryInitCmd(), registryCloneCmd(), registryAddCmd(),
		registryRmCmd(), registryStatusCmd(), registryDeleteCmd(),
		registrySyncCmd(), registryCheckCmd(), registryYankCmd(),
	)
	return cmd
}

func registryCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check [name]",
		Short: "Check that every registered version's dependencies resolve within the registries",
		Long: "Check every registered version (of one registry, or of all of them): each declared\n" +
			"dependency minimum must be registered, and the version's whole dependency closure\n" +
			"must resolve from the registries alone — exactly what a consumer needs. Reads the\n" +
			"local clones as they are; run 'cosm update' first to check the latest. Exits\n" +
			"non-zero when anything fails, so it can gate a registry's CI.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			latest, _ := cmd.Flags().GetBool("latest")
			res, err := s.CheckRegistries(name, latest)
			if err != nil {
				return err
			}
			scope := "every version"
			if latest {
				scope = "the latest version of each major line"
			}
			skipped := ""
			if res.Yanked > 0 {
				skipped = fmt.Sprintf("; %d yanked version(s) skipped", res.Yanked)
			}
			fmt.Printf("Checked %s: %d versions of %d packages in %s (local clones%s).\n",
				scope, res.Versions, res.Packages, strings.Join(res.Registries, ", "), skipped)
			for _, p := range res.Problems {
				v := p.Version
				if v != "" {
					v = " " + v
				}
				fmt.Printf("  FAIL  %s%s (%s): %v\n", p.Package, v, p.Registry, p.Err)
			}
			if len(res.Problems) == 0 {
				fmt.Println("All resolve.")
				return nil
			}
			bad := map[string]bool{}
			for _, p := range res.Problems {
				bad[p.Registry+"\x00"+p.Package+"\x00"+p.Version] = true
			}
			return service.KindError(errs.ErrPackageNotFound,
				fmt.Sprintf("%d of %d registered versions don't resolve within the registries", len(bad), res.Versions))
		},
	}
	cmd.Flags().Bool("latest", false, "check only the newest version of each package's major line")
	return cmd
}

func registryInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init <name> <giturl>",
		Short: "Initialize a new registry from an empty remote",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			kind, _ := cmd.Flags().GetString("kind")
			if kind != "source" && kind != "binary" && kind != "mixed" {
				return fmt.Errorf("%w: --kind must be source, binary, or mixed", errs.ErrUsage)
			}
			if err := s.Reg.InitRegistry(args[0], args[1], types.RegistryKind(kind)); err != nil {
				return err
			}
			fmt.Printf("Initialized %s registry '%s'\n", kind, args[0])
			return nil
		},
	}
	cmd.Flags().String("kind", "source", "registry kind: source | binary | mixed")
	return cmd
}

func registryCloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone <giturl>",
		Short: "Clone an existing registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			name, err := s.Reg.CloneRegistry(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Cloned registry '%s'\n", name)
			return nil
		},
	}
}

func registryAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name> <giturl>",
		Short: "Register a package and its released versions (idempotent; picks up new tags)",
		Long: "Register a package and every released version its registration policy allows;\n" +
			"on an already-registered package, pick up new tags. By default that is every\n" +
			"vX.Y.Z tag in the repository. --from sets a floor that this and every later add\n" +
			"or sync respect; --tag registers just the named tags, this once.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			var opts registry.AddOptions
			opts.From, _ = cmd.Flags().GetString("from")
			opts.Tags, _ = cmd.Flags().GetStringArray("tag")
			res, err := s.Reg.AddPackageWith(args[0], args[1], opts)
			if err != nil {
				return err
			}
			switch {
			case len(res.Added) > 0:
				fmt.Printf("Added '%s' (%d version(s): %s) to '%s'\n", res.Name, len(res.Added), strings.Join(res.Added, ", "), args[0])
			default:
				fmt.Printf("Package '%s' already up to date in '%s'\n", res.Name, args[0])
			}
			if opts.From != "" {
				fmt.Printf("Registration floor for '%s' is now %s: older tags won't be registered by add or sync.\n", res.Name, opts.From)
			}
			for _, sk := range res.Skipped {
				fmt.Printf("  not registered: %s\n", sk)
			}
			if len(res.BelowFloor) > 0 {
				fmt.Printf("  already registered below the floor (kept; remove with 'cosm registry rm %s %s <version>'): %s\n",
					args[0], res.Name, strings.Join(res.BelowFloor, ", "))
			}
			return nil
		},
	}
	cmd.Flags().String("from", "", "register only tags at or above this version, now and in every later add/sync")
	cmd.Flags().StringArray("tag", nil, "register only this tag (repeatable; one-off)")
	return cmd
}

func registryYankCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "yank <name> <pkg> v<version>",
		Short: "Mark a version as yanked: still resolves where required, never newly selected",
		Long: "Yank a bad release without breaking anyone. A yanked version stays registered and\n" +
			"keeps resolving for every project that already requires it, so builds stay\n" +
			"reproducible; resolving it prints a warning. It is never picked by 'cosm add' or\n" +
			"'cosm upgrade', nor suggested. Use 'registry rm' only to remove a version entirely.",
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			if undo, _ := cmd.Flags().GetBool("undo"); undo {
				if err := s.Reg.Unyank(args[0], args[1], args[2]); err != nil {
					return err
				}
				fmt.Printf("Unyanked %s@%s in '%s'\n", args[1], args[2], args[0])
				return nil
			}
			reason, _ := cmd.Flags().GetString("reason")
			if err := s.Reg.Yank(args[0], args[1], args[2], reason); err != nil {
				return err
			}
			fmt.Printf("Yanked %s@%s in '%s'\n", args[1], args[2], args[0])
			return nil
		},
	}
	cmd.Flags().String("reason", "", "why it was yanked (shown in the warning)")
	cmd.Flags().Bool("undo", false, "clear the yank")
	return cmd
}

func registryRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <name> <pkg> [v<version>]",
		Short: "Remove a package or a version from a registry",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			target := fmt.Sprintf("package '%s'", args[1])
			if len(args) == 3 {
				target = fmt.Sprintf("version '%s' of '%s'", args[2], args[1])
			}
			if !force && !confirm(fmt.Sprintf("Remove %s from '%s'? [y/N]: ", target, args[0])) {
				return fmt.Errorf("%w: cancelled", errs.ErrUsage)
			}
			if len(args) == 3 {
				if err := s.Reg.RemoveVersion(args[0], args[1], args[2]); err != nil {
					return err
				}
				fmt.Printf("Removed %s@%s from '%s'. add/sync won't register it again "+
					"(undo with 'cosm registry add %s <giturl> --tag %s').\n", args[1], args[2], args[0], args[0], args[2])
				return nil
			}
			if err := s.Reg.RemovePackage(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Removed package '%s' from '%s'\n", args[1], args[0])
			return nil
		},
	}
	cmd.Flags().BoolP("force", "f", false, "skip confirmation")
	return cmd
}

func registryStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Overview of a registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			reg, pkgs, err := s.Reg.Status(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Registry '%s' [%s] (%d packages)\n", reg.Name, reg.Kind, len(pkgs))
			for _, p := range pkgs {
				pol, _ := manifest.LoadPolicy(registry.PolicyFile(s.D.Registry(args[0]), p.Name))
				shown := make([]string, 0, len(p.Versions))
				for _, v := range p.Versions {
					if _, y := pol.Yanked[v]; y {
						v += " (yanked)"
					}
					shown = append(shown, v)
				}
				vers := strings.Join(shown, ", ")
				if vers == "" {
					vers = "(no versions)"
				}
				var extra []string
				if pol.Since != "" {
					extra = append(extra, "registers from "+pol.Since)
				}
				if len(pol.Excluded) > 0 {
					extra = append(extra, "removed: "+strings.Join(pol.Excluded, ", "))
				}
				policy := ""
				if len(extra) > 0 {
					policy = "  [" + strings.Join(extra, "; ") + "]"
				}
				fmt.Printf("  - %s: %s%s\n", p.Name, vers, policy)
			}
			return nil
		},
	}
}

func registrySyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync <name>",
		Short: "Scan every package's remote for new tags and register them (for registry CI)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			added, warns, err := s.Reg.SyncRegistry(args[0])
			for _, w := range warns {
				fmt.Fprintf(os.Stderr, "warning: %s\n", w)
			}
			if err != nil {
				return err
			}
			total := 0
			for name, vers := range added {
				for _, v := range vers {
					fmt.Printf("Registered %s@%s\n", name, v)
					total++
				}
			}
			if total == 0 {
				fmt.Printf("Registry '%s' is already up to date.\n", args[0])
			}
			return nil
		},
	}
}

func registryDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a registry from the local depot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			if !force && !confirm(fmt.Sprintf("Delete registry '%s'? [y/N]: ", args[0])) {
				return fmt.Errorf("%w: cancelled", errs.ErrUsage)
			}
			if err := s.Reg.DeleteRegistryLocal(args[0]); err != nil {
				return err
			}
			fmt.Printf("Deleted registry '%s'\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolP("force", "f", false, "skip confirmation")
	return cmd
}
