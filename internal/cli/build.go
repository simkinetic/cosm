package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"cosm/internal/errs"
	"cosm/internal/ext"
	"cosm/internal/materialize"
	"cosm/internal/resolve"
	"cosm/internal/service"
	"cosm/internal/types"
)

func materializer(s *service.Service, buildType string, jobs int) *materialize.Materializer {
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	return &materialize.Materializer{
		D:     s.D,
		Git:   s.Git,
		Run:   ext.NewRunner(s.D),
		Specs: s.Loader,
		Opt: materialize.Options{
			Platform:  types.Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
			BuildType: buildType,
			Jobs:      jobs,
		},
	}
}

func buildTypeFromFlags(cmd *cobra.Command) string {
	if dbg, _ := cmd.Flags().GetBool("debug"); dbg {
		return "Debug"
	}
	return "Release"
}

func buildFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("release", true, "build in release mode")
	cmd.Flags().Bool("debug", false, "build in debug mode")
	cmd.Flags().Int("jobs", 0, "parallel build jobs (default: CPUs)")
}

func buildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Resolve, materialize, and build the project and its dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			root, bl, warns, err := s.Resolve(cwd)
			if err != nil {
				return err
			}
			printWarns(warns)
			jobs, _ := cmd.Flags().GetInt("jobs")
			m := materializer(s, buildTypeFromFlags(cmd), jobs)
			if _, err := m.EnsureBuilt(root, cwd, bl); err != nil {
				return err
			}
			fmt.Printf("Built %s (%d dependencies)\n", root.Name, len(bl.Dependencies))
			return nil
		},
	}
	buildFlags(cmd)
	return cmd
}

func runCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "run [--] <command> [args...]",
		Short:              "Build, then run a command in the project environment",
		DisableFlagParsing: false,
		Args:               cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := buildAndActivate(cmd)
			if err != nil {
				return err
			}
			c := exec.Command(args[0], args[1:]...)
			c.Env = env
			// Resolve the command against the assembled PATH (not the parent's),
			// so a just-built binary is found.
			if !strings.ContainsRune(args[0], os.PathSeparator) {
				if resolved, ok := lookPathIn(args[0], env); ok {
					c.Path = resolved
					c.Err = nil
				}
			}
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			c.Dir, _ = os.Getwd()
			if err := c.Run(); err != nil {
				return err
			}
			return nil
		},
	}
	buildFlags(cmd)
	return cmd
}

func envCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Print the project environment as shell exports",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			root, bl, warns, err := s.Resolve(cwd)
			if err != nil {
				return err
			}
			printWarns(warns)
			jobs, _ := cmd.Flags().GetInt("jobs")
			m := materializer(s, buildTypeFromFlags(cmd), jobs)
			built, rootBuilt, err := m.BuildProject(root, cwd, bl)
			if err != nil {
				return err
			}
			resp, err := m.Activate(root, cwd, bl, built, rootBuilt)
			if err != nil {
				return err
			}
			expand, _ := cmd.Flags().GetBool("expand")
			sep := string(os.PathListSeparator)
			for k, v := range resp.Env {
				fmt.Printf("export %s=%q\n", k, v)
			}
			for k, vals := range resp.PrependPaths {
				if expand {
					// Fully resolved value (cosm's paths prepended to the inherited
					// var) — no unexpanded ${VAR}, for tools that parse the output.
					val := strings.Join(vals, sep)
					if cur := os.Getenv(k); cur != "" {
						val += sep + cur
					}
					fmt.Printf("export %s=%q\n", k, val)
				} else {
					fmt.Printf("export %s=\"%s%s${%s}\"\n", k, strings.Join(vals, sep), sep, k)
				}
			}
			return nil
		},
	}
	buildFlags(cmd)
	cmd.Flags().Bool("expand", false, "print fully-resolved values (no ${VAR}); for tools parsing the output")
	return cmd
}

func testCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test [-- <runner args>]",
		Short: "Build, then run the project's tests via its extension",
		Long: "Build the project (including test-only dependencies) and run its tests via\n" +
			"the build extension. Fails on a failing test AND on a run that discovers zero\n" +
			"tests (a vacuous-pass guardrail). Arguments after -- are forwarded to the\n" +
			"underlying runner (e.g. ctest): cosm test -- -R mytag --output-on-failure.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			// Tests resolve with the project's test-only dependencies (§7.6).
			root, bl, warns, err := s.ResolveWithTests(cwd)
			if err != nil {
				return err
			}
			printWarns(warns)
			if root.Build == "" {
				return fmt.Errorf("%w: project has no build system", errs.ErrUsage)
			}
			jobs, _ := cmd.Flags().GetInt("jobs")
			verbose, _ := cmd.Flags().GetBool("verbose")
			keepBuild, _ := cmd.Flags().GetBool("keep-build")
			cxxFlags, _ := cmd.Flags().GetString("cxxflags")
			ldFlags, _ := cmd.Flags().GetString("ldflags")
			m := materializer(s, buildTypeFromFlags(cmd), jobs)
			// Build the whole test closure (regular deps + testDeps) so their install
			// prefixes exist; the extension configures the project's tests against
			// them (the root itself is (re)configured from source by the test verb).
			built, err := m.BuildAll(bl)
			if err != nil {
				return err
			}
			var deps []ext.DepCtx
			for key, e := range bl.Dependencies {
				b := built[key]
				deps = append(deps, ext.DepCtx{Name: e.Name, UUID: e.UUID, Version: e.Version, Prefix: b.Prefix, Descriptor: b.Descriptor})
			}
			resp, err := ext.NewRunner(s.D).Test(root.Build, ext.TestRequest{
				Project:  ext.PackageCtx{Name: root.Name, UUID: root.UUID, Version: root.Version, Source: cwd, Provides: root.Provides},
				Platform: m.Opt.Platform,
				Deps:     deps,
				Config:    m.Opt.ConfigJSON(),
				Jobs:      m.Opt.Jobs,
				Verbose:   verbose,
				KeepBuild: keepBuild,
				CxxFlags:  cxxFlags,
				LdFlags:   ldFlags,
				Args:      args,
			})
			if err != nil {
				return err
			}
			// Surface the captured output on failure, or when asked.
			if resp.Log != "" && (verbose || resp.Status != "ok") {
				if data, rerr := os.ReadFile(resp.Log); rerr == nil {
					fmt.Print(string(data))
				}
			}
			if keepBuild && resp.BuildDir != "" {
				fmt.Printf("Build tree kept at %s\n", resp.BuildDir)
			}
			if resp.Status != "ok" {
				return &errs.BuildFailedError{Package: root.Name, Phase: "test", LogPath: resp.Log}
			}
			// Guardrail: a run that discovered zero tests is a vacuous pass — treat it
			// as a test failure (same exit code), not a silent success.
			if resp.Tests == 0 {
				return &errs.BuildFailedError{Package: root.Name, Phase: "test", LogPath: resp.Log,
					Err: errors.New("no tests were discovered or run")}
			}
			if resp.Tests > 0 {
				fmt.Printf("Tests ok (%d run)\n", resp.Tests)
			} else {
				fmt.Printf("Tests %s\n", resp.Status)
			}
			return nil
		},
	}
	buildFlags(cmd)
	cmd.Flags().Bool("verbose", false, "print full test output (not just on failure)")
	cmd.Flags().Bool("keep-build", false, "keep the test build tree and print its path (for coverage tools)")
	cmd.Flags().String("cxxflags", "", "extra compile flags for this test run (e.g. coverage instrumentation)")
	cmd.Flags().String("ldflags", "", "extra link flags for this test run")
	return cmd
}


func shellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "shell",
		Aliases: []string{"activate"},
		Short:   "Build, then open an interactive shell in the project environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := buildAndActivate(cmd)
			if err != nil {
				return err
			}
			sh := os.Getenv("SHELL")
			if sh == "" {
				sh = "bash"
			}
			c := exec.Command(sh)
			c.Env = append(env, "COSM_PROMPT=1")
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
			c.Dir, _ = os.Getwd()
			fmt.Println("cosm environment active. Type 'exit' to leave.")
			_ = c.Run()
			return nil
		},
	}
	buildFlags(cmd)
	return cmd
}

func publishCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish --registry <r> --store <dir>",
		Short: "Build and publish a binary artifact to a binary/mixed registry",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newService()
			if err != nil {
				return err
			}
			reg, _ := cmd.Flags().GetString("registry")
			store, _ := cmd.Flags().GetString("store")
			cwd, _ := os.Getwd()
			ver, err := s.Publish(cwd, service.PublishOpts{
				Registry: reg, Store: store, BuildType: buildTypeFromFlags(cmd),
			})
			if err != nil {
				return err
			}
			fmt.Printf("Published %s to registry '%s'\n", ver, reg)
			return nil
		},
	}
	cmd.Flags().String("registry", "", "target binary/mixed registry")
	cmd.Flags().String("store", "", "artifact store directory")
	cmd.Flags().Bool("debug", false, "debug build")
	return cmd
}

// buildAndActivate resolves, builds, and returns the assembled environment.
func buildAndActivate(cmd *cobra.Command) ([]string, error) {
	s, err := newService()
	if err != nil {
		return nil, err
	}
	cwd, _ := os.Getwd()
	root, bl, warns, err := s.Resolve(cwd)
	if err != nil {
		return nil, err
	}
	printWarns(warns)
	jobs, _ := cmd.Flags().GetInt("jobs")
	m := materializer(s, buildTypeFromFlags(cmd), jobs)
	built, rootBuilt, err := m.BuildProject(root, cwd, bl)
	if err != nil {
		return nil, err
	}
	resp, err := m.Activate(root, cwd, bl, built, rootBuilt)
	if err != nil {
		return nil, err
	}
	return materialize.AssembleEnv(os.Environ(), resp), nil
}

// lookPathIn resolves an executable name against the PATH found in env.
func lookPathIn(name string, env []string) (string, bool) {
	var pathVal string
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			pathVal = kv[len("PATH="):]
		}
	}
	for _, dir := range filepath.SplitList(pathVal) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return cand, true
		}
	}
	return "", false
}

func printWarns(warns []resolve.Warning) {
	for _, w := range warns {
		fmt.Fprintf(os.Stderr, "warning[%s]: %s\n", w.Code, w.Message)
	}
}
