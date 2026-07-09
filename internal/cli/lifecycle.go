package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"cosm/internal/errs"
)

func developCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "develop <name>",
		Short: "Check out a dependency for co-development and enroll this project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			major, _ := cmd.Flags().GetInt("major")
			branch, _ := cmd.Flags().GetString("branch")
			tag, _ := cmd.Flags().GetString("tag")
			devPath, _ := cmd.Flags().GetString("path")
			if branch != "" && tag != "" {
				return fmt.Errorf("%w: --branch and --tag are mutually exclusive", errs.ErrUsage)
			}
			if devPath != "" && (branch != "" || tag != "") {
				return fmt.Errorf("%w: --path adopts a local checkout as-is; --branch/--tag don't apply", errs.ErrUsage)
			}
			cwd, _ := os.Getwd()
			checkout, err := s.Develop(cwd, args[0], major, branch, tag, devPath)
			if err != nil {
				return err
			}
			fmt.Printf("Developing '%s' at %s\n", args[0], checkout)
			if devPath != "" {
				fmt.Printf("Now declare the dependency: cosm add %s\n", args[0])
			}
			return nil
		},
	}
	cmd.Flags().Int("major", -1, "disambiguate which major to develop")
	cmd.Flags().String("branch", "", "check out a branch")
	cmd.Flags().String("tag", "", "check out a released tag")
	cmd.Flags().String("path", "", "adopt a local package checkout (bootstraps an unpublished sibling)")
	return cmd
}

func freeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "free <name>",
		Short: "Stop developing a dependency for this project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			major, _ := cmd.Flags().GetInt("major")
			purge, _ := cmd.Flags().GetBool("purge")
			cwd, _ := os.Getwd()
			if err := s.Free(cwd, args[0], major, purge); err != nil {
				return err
			}
			fmt.Printf("Freed '%s'\n", args[0])
			return nil
		},
	}
	cmd.Flags().Int("major", -1, "disambiguate which major to free")
	cmd.Flags().Bool("purge", false, "also delete the shared checkout")
	return cmd
}

func upgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade <name> [v<constraint>]",
		Short: "Raise a dependency's floor within its major",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			latest, _ := cmd.Flags().GetBool("latest")
			all, _ := cmd.Flags().GetBool("all")
			cwd, _ := os.Getwd()
			if all {
				m, _, _, err := s.Resolve(cwd)
				if err != nil {
					return err
				}
				seen := map[string]bool{}
				for _, dp := range m.Deps {
					if seen[dp.Name] {
						continue
					}
					seen[dp.Name] = true
					old, nw, err := s.Upgrade(cwd, dp.Name, "", true)
					if err != nil {
						fmt.Fprintf(os.Stderr, "warning: upgrade %s: %v\n", dp.Name, err)
						continue
					}
					if old != nw {
						fmt.Printf("Upgraded %s %s -> %s\n", dp.Name, old, nw)
					}
				}
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("%w: package name or --all required", errs.ErrUsage)
			}
			constraint := ""
			if len(args) == 2 {
				constraint = args[1]
			}
			old, nw, err := s.Upgrade(cwd, args[0], constraint, latest)
			if err != nil {
				return err
			}
			if old == nw {
				fmt.Printf("%s already at %s\n", args[0], nw)
			} else {
				fmt.Printf("Upgraded %s %s -> %s\n", args[0], old, nw)
			}
			return nil
		},
	}
	cmd.Flags().Bool("latest", false, "newest in the major (ignore finer constraint)")
	cmd.Flags().Bool("all", false, "upgrade all direct dependencies")
	return cmd
}

func downgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "downgrade <name> v<version>",
		Short: "Lower a dependency's floor to an existing version",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			warns, err := s.Downgrade(cwd, args[0], args[1])
			if err != nil {
				return err
			}
			for _, w := range warns {
				fmt.Fprintf(os.Stderr, "note: %s\n", w)
			}
			fmt.Printf("Downgraded %s to %s\n", args[0], args[1])
			return nil
		},
	}
}
