// SPDX-FileCopyrightText: 2026 Simkinetic
//
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"cosm/internal/errs"
	"cosm/internal/manifest"
	"cosm/internal/resolve"
)

func verifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <pkg>[@v<version>]",
		Short: "Build and test a released version in a throwaway depot, from the registries alone",
		Long: "Check that a released version works for a consumer starting from nothing: in a\n" +
			"fresh temporary depot, clone your registries from their remotes, fetch the\n" +
			"version's source at its registered commit (verifying its content hash), resolve\n" +
			"its dependencies from the registries alone — no develop checkouts, no --source —\n" +
			"then build it and run its tests. Your depot, $COSM_DEPOT, and the global default\n" +
			"are never touched; the temporary depot is deleted afterwards (--keep to inspect).\n" +
			"With no version, the newest non-yanked one.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, version, _ := strings.Cut(args[0], "@")
			if name == "" {
				return fmt.Errorf("%w: usage: cosm verify <pkg>[@v<version>]", errs.ErrUsage)
			}
			s, err := newService()
			if err != nil {
				return err
			}
			local, _ := cmd.Flags().GetBool("local")
			keep, _ := cmd.Flags().GetBool("keep")
			noTest, _ := cmd.Flags().GetBool("no-test")
			jobs, _ := cmd.Flags().GetInt("jobs")
			verbose, _ := cmd.Flags().GetBool("verbose")

			t, dir, cleanup, err := s.ThrowawayDepot(local)
			if err != nil {
				return err
			}
			if keep {
				defer fmt.Printf("Throwaway depot kept at %s\n", dir)
			} else {
				defer cleanup()
			}
			from := "registries cloned from their remotes"
			if local {
				from = "copies of your local registry clones"
			}
			fmt.Printf("Throwaway depot %s (%s)\n", dir, from)

			entry, reg, err := t.RegisteredEntry(name, version)
			if err != nil {
				return err
			}
			fmt.Printf("Verifying %s %s from registry '%s'\n", entry.Name, entry.Version, reg)
			m := materializer(t, buildTypeFromFlags(cmd), jobs, verbose, true)
			src, err := m.EnsureSource(entry)
			if err != nil {
				return err
			}
			short := entry.Commit
			if len(short) > 12 {
				short = short[:12]
			}
			fmt.Printf("  source: commit %s, content hash verified\n", short)

			root, err := manifest.LoadManifestFromDir(src)
			if err != nil {
				return err
			}
			resolveFn := resolve.ResolveWithTests
			if noTest {
				resolveFn = resolve.Resolve
			}
			bl, warns, err := resolveFn(root, t.Loader, resolve.NoDev)
			if err != nil {
				return fmt.Errorf("%w (resolved from the registries alone)", err)
			}
			printWarns(warns)
			fmt.Printf("  resolved %d dependencies from the registries alone\n", len(bl.Dependencies))

			if _, err := m.EnsureBuilt(root, src, bl); err != nil {
				return err
			}
			if !noTest {
				if err := runTests(m, root, src, bl, testRun{verbose: verbose}); err != nil {
					return err
				}
			}
			what := "built and tested"
			if noTest {
				what = "built"
			}
			fmt.Printf("Verified %s %s: %s from the registries alone.\n", entry.Name, entry.Version, what)
			return nil
		},
	}
	// Not buildFlags: --source would defeat the point (registries alone).
	cmd.Flags().Bool("release", true, "build in release mode")
	cmd.Flags().Bool("debug", false, "build in debug mode")
	cmd.Flags().Int("jobs", 0, "parallel build jobs (default: CPUs)")
	cmd.Flags().BoolP("verbose", "v", false, "stream the extension's build/test output live")
	cmd.Flags().Bool("local", false, "use copies of your local registry clones and mirrors instead of cloning (offline)")
	cmd.Flags().Bool("keep", false, "keep the throwaway depot and print its path")
	cmd.Flags().Bool("no-test", false, "build only; don't run the tests")
	return cmd
}
