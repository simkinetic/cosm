// Package cli wires the cobra command tree to the service layer (§12).
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"cosm/internal/depot"
	"cosm/internal/errs"
	"cosm/internal/ext"
	"cosm/internal/gitx"
	"cosm/internal/manifest"
	"cosm/internal/registry"
	"cosm/internal/semver"
	"cosm/internal/service"
	"cosm/internal/types"
)

// Version is set by the main package (ldflags).
var Version = "dev"

var depotFlag string

// Execute builds and runs the command tree, returning a process exit code.
func Execute() int {
	root := buildRoot()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return errs.ExitCode(err)
	}
	return 0
}

func buildRoot() *cobra.Command {
	var versionFlag bool
	root := &cobra.Command{
		Use:           "cosm",
		Short:         "A language-agnostic package manager",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if versionFlag {
				fmt.Printf("cosm version %s\n", Version)
				return nil
			}
			return cmd.Help()
		},
	}
	root.PersistentFlags().StringVar(&depotFlag, "depot", "", "override the depot path")
	root.Flags().BoolVar(&versionFlag, "version", false, "print the version and exit")

	root.AddCommand(
		setupCmd(), initCmd(), statusCmd(), addCmd(), rmCmd(),
		releaseCmd(), updateCmd(), registryCmd(),
		buildCmd(), runCmd(), envCmd(), testCmd(), shellCmd(),
		developCmd(), freeCmd(), upgradeCmd(), downgradeCmd(),
		publishCmd(),
	)
	return root
}

// ---- helpers ----

func openDepot() (depot.Depot, error) {
	root, err := depot.ResolveRoot(depotFlag)
	if err != nil {
		return depot.Depot{}, err
	}
	return depot.New(root), nil
}

func newService() (*service.Service, error) {
	d, err := openDepot()
	if err != nil {
		return nil, err
	}
	if !d.IsInitialized() {
		return nil, fmt.Errorf("%w: depot not initialized at %s (run 'cosm setup')", errs.ErrUsage, d.Root)
	}
	return service.New(d, gitx.Exec{}), nil
}

func promptLine(prompt string) string {
	fmt.Print(prompt)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	return strings.TrimSpace(s)
}

func confirm(prompt string) bool {
	resp := strings.ToLower(promptLine(prompt))
	return resp == "y" || resp == "yes"
}

func gitAuthors(g gitx.Git) []types.Author {
	name, _ := g.Run("", "config", "user.name")
	email, _ := g.Run("", "config", "user.email")
	if name == "" && email == "" {
		return nil
	}
	return []types.Author{{Name: name, Email: email}}
}

// ---- setup ----

func setupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Initialize the depot",
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := openDepot()
			if err != nil {
				return err
			}
			if err := d.Setup(); err != nil {
				return err
			}
			if store, _ := cmd.Flags().GetString("store"); store != "" {
				cfg, _ := d.LoadConfig()
				cfg.ArtifactStore = store
				if err := d.SaveConfig(cfg); err != nil {
					return err
				}
			}
			fmt.Printf("Depot ready at %s\n", d.Root)
			fmt.Printf("To pin it for this shell: export COSM_DEPOT=%q\n", d.Root)
			return nil
		},
	}
	cmd.Flags().String("store", "", "default artifact store directory for 'cosm publish'")
	return cmd
}

// ---- init ----

func initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init <name> [v<version>]",
		Short: "Initialize a new package (cosm.json)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if name == "" {
				return fmt.Errorf("%w: package name required", errs.ErrUsage)
			}
			version := "v0.1.0"
			if len(args) == 2 {
				version = args[1]
			}
			if err := semver.ValidateExact(version); err != nil {
				return fmt.Errorf("%w: %v", errs.ErrUsage, err)
			}
			build, _ := cmd.Flags().GetString("build")
			if _, err := os.Stat("cosm.json"); err == nil {
				return fmt.Errorf("%w: cosm.json already exists", errs.ErrUsage)
			}
			m := &types.Manifest{
				Name: name, UUID: uuid.NewString(), Version: version,
				Authors: gitAuthors(gitx.Exec{}), Build: build,
			}
			cwd, _ := os.Getwd()
			// When a build extension is set, let it lay down the language-specific
			// source tree and declare the module namespace(s) for the manifest, so
			// the user doesn't hand-write `provides` or create `src/<ns>/`.
			var scaffolded []string
			if build != "" {
				files, err := scaffoldPackage(build, m, cwd)
				if err != nil {
					return err
				}
				scaffolded = files
			}
			if err := manifest.SaveManifest("cosm.json", m); err != nil {
				return err
			}
			fmt.Printf("Initialized package '%s' %s\n", name, version)
			for _, f := range scaffolded {
				fmt.Printf("  created %s\n", f)
			}
			return nil
		},
	}
	cmd.Flags().String("build", "", "build-system extension id (e.g. lua, cmake)")
	return cmd
}

// scaffoldPackage asks the build extension to create the source layout and
// declare the package's module namespace(s), folding the result into m. A missing
// extension is not fatal: the manifest is still written (minus the scaffold).
func scaffoldPackage(build string, m *types.Manifest, dir string) ([]string, error) {
	d, err := openDepot()
	if err != nil {
		return nil, err
	}
	resp, err := ext.NewRunner(d).Scaffold(build, ext.ScaffoldRequest{
		Name: m.Name, UUID: m.UUID, Version: m.Version, Dir: dir,
	})
	if err != nil {
		if errors.Is(err, errs.ErrExtNotFound) {
			fmt.Fprintf(os.Stderr, "note: extension 'cosm-ext-%s' not found; wrote manifest only "+
				"(install it to scaffold sources and 'provides')\n", build)
			return nil, nil
		}
		return nil, err
	}
	if len(resp.Provides) > 0 {
		m.Provides = resp.Provides
	}
	if len(resp.Ext) > 0 {
		m.Ext = resp.Ext
	}
	return resp.Files, nil
}

// ---- status ----

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the project and its resolved build list",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			m, bl, warns, err := s.Resolve(cwd)
			if err != nil {
				return err
			}
			fmt.Printf("%s %s", m.Name, m.Version)
			if m.Build != "" {
				fmt.Printf(" [%s]", m.Build)
			}
			fmt.Println()
			if len(m.Deps) > 0 {
				fmt.Println("Direct dependencies:")
				for _, dep := range sortedDeps(m.Deps) {
					fmt.Printf("  * %s %s\n", dep.Name, dep.Version)
				}
			}
			fmt.Printf("Resolved %d dependencies:\n", len(bl.Dependencies))
			for _, e := range sortedEntries(bl.Dependencies) {
				marker := ""
				if e.Develop {
					marker = " (develop)"
				}
				fmt.Printf("  - %s %s%s\n", e.Name, e.Version, marker)
			}
			for _, w := range warns {
				fmt.Printf("warning[%s]: %s\n", w.Code, w.Message)
			}
			return nil
		},
	}
}

func sortedDeps(deps map[string]types.Dependency) []types.Dependency {
	out := make([]types.Dependency, 0, len(deps))
	for _, d := range deps {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedEntries(m map[string]types.BuildListEntry) []types.BuildListEntry {
	out := make([]types.BuildListEntry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---- add / rm ----

func addCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name> [v<version>]",
		Short: "Add a dependency",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			name := args[0]
			version := ""
			if len(args) == 2 {
				version = args[1]
				if !strings.HasPrefix(version, "v") {
					return fmt.Errorf("%w: version must start with 'v'", errs.ErrUsage)
				}
			}
			var opts service.AddOpts
			opts.Registry, _ = cmd.Flags().GetString("registry")
			opts.Major, _ = cmd.Flags().GetInt("major")
			cwd, _ := os.Getwd()
			ver, reg, err := s.Add(cwd, name, version, opts, chooseRegistry)
			if err != nil {
				return err
			}
			fmt.Printf("Added %s %s from registry '%s'\n", name, ver, reg)
			return nil
		},
	}
	cmd.Flags().String("registry", "", "select a registry when the name is ambiguous (non-interactive)")
	cmd.Flags().Int("major", -1, "select a major version line when the name is ambiguous (non-interactive)")
	return cmd
}

func chooseRegistry(name, version string, locs []registry.Location) (registry.Location, error) {
	fmt.Printf("'%s' has %d candidates — choose one:\n", name, len(locs))
	for i, l := range locs {
		fmt.Printf("  %d. %s %s  [registry '%s', uuid %s]\n       %s\n",
			i+1, name, l.Specs.Version, l.Registry, shortUUID(l.Specs.UUID), l.Specs.GitURL)
	}
	choice := promptLine(fmt.Sprintf("Select 1-%d: ", len(locs)))
	var n int
	if _, err := fmt.Sscanf(choice, "%d", &n); err != nil || n < 1 || n > len(locs) {
		return registry.Location{}, fmt.Errorf("%w: invalid selection %q", errs.ErrUsage, choice)
	}
	return locs[n-1], nil
}

func shortUUID(u string) string {
	if len(u) > 8 {
		return u[:8]
	}
	return u
}

func rmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a dependency",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			if err := s.Rm(cwd, args[0], chooseDep); err != nil {
				return err
			}
			fmt.Printf("Removed %s\n", args[0])
			return nil
		},
	}
}

func chooseDep(name string, keys []string, deps []types.Dependency) (string, error) {
	fmt.Printf("Multiple '%s' dependencies:\n", name)
	for i, d := range deps {
		fmt.Printf("  %d. %s (%s)\n", i+1, d.Version, keys[i])
	}
	choice := promptLine(fmt.Sprintf("Select 1-%d: ", len(deps)))
	var n int
	if _, err := fmt.Sscanf(choice, "%d", &n); err != nil || n < 1 || n > len(keys) {
		return "", fmt.Errorf("%w: invalid selection %q", errs.ErrUsage, choice)
	}
	return keys[n-1], nil
}

// ---- release ----

func releaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release [v<version>]",
		Short: "Publish a new release (bump, tag, push)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			var opts service.ReleaseOpts
			if len(args) == 1 {
				opts.Version = args[0]
			}
			opts.Patch, _ = cmd.Flags().GetBool("patch")
			opts.Minor, _ = cmd.Flags().GetBool("minor")
			opts.Major, _ = cmd.Flags().GetBool("major")
			cwd, _ := os.Getwd()
			ver, err := s.Release(cwd, opts)
			if err != nil {
				return err
			}
			fmt.Printf("Released %s\n", ver)
			return nil
		},
	}
	cmd.Flags().Bool("patch", false, "increment patch")
	cmd.Flags().Bool("minor", false, "increment minor")
	cmd.Flags().Bool("major", false, "increment major")
	return cmd
}

// ---- update ----

func updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update [registry]",
		Short: "Sync registries with their remotes (all by default, or one named)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			// A single named registry, else every registry.
			if len(args) == 1 {
				if err := s.Reg.UpdateRegistry(args[0]); err != nil {
					return err
				}
				fmt.Printf("Updated %s\n", args[0])
				return nil
			}
			updated, failures := s.Reg.UpdateAll()
			for _, n := range updated {
				fmt.Printf("Updated %s\n", n)
			}
			for n, e := range failures {
				fmt.Fprintf(os.Stderr, "warning: update %s: %v\n", n, e)
			}
			if len(updated) == 0 && len(failures) == 0 {
				fmt.Println("No registries to update.")
			}
			if len(failures) > 0 {
				return fmt.Errorf("%w: %d registry update(s) failed", errs.ErrNetwork, len(failures))
			}
			return nil
		},
	}
	return cmd
}
