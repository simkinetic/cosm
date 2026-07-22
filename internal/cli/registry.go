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
		registrySyncCmd(),
	)
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
	return &cobra.Command{
		Use:   "add <name> <giturl>",
		Short: "Register a package and its released versions (idempotent; picks up new tags)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			name, added, err := s.Reg.AddPackage(args[0], args[1])
			if err != nil {
				return err
			}
			if len(added) == 0 {
				fmt.Printf("Package '%s' already up to date in '%s'\n", name, args[0])
			} else {
				fmt.Printf("Added '%s' (%d version(s)) to '%s'\n", name, len(added), args[0])
			}
			return nil
		},
	}
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
				fmt.Printf("Removed %s@%s from '%s'\n", args[1], args[2], args[0])
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
				vers := strings.Join(p.Versions, ", ")
				if vers == "" {
					vers = "(no versions)"
				}
				fmt.Printf("  - %s: %s\n", p.Name, vers)
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
