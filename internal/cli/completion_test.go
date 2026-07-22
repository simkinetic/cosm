package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"cosm/internal/depot"
	"cosm/internal/manifest"
	"cosm/internal/types"
)

func TestCompletionHelpers(t *testing.T) {
	home := setupEnv(t)
	if _, err := runCLI(t, home, "setup"); err != nil {
		t.Fatal(err)
	}
	d := depot.New(filepath.Join(home, ".cosm"))
	if err := manifest.SaveRegistryRefs(d.RegistriesFile(), []types.RegistryRef{
		{Name: "cosmcpp", UUID: "u2", GitURL: "g2"},
		{Name: "cosmlua", UUID: "u1", GitURL: "g1"},
	}); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(home, "p")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SaveManifest(filepath.Join(proj, "cosm.json"), &types.Manifest{
		Name: "p", UUID: "up", Version: "v0.1.0", Build: "lua",
		Deps:     map[string]types.Dependency{"ua@v1": {Name: "alpha", Version: "v1.0.0"}},
		TestDeps: map[string]types.Dependency{"uc@v1": {Name: "charlie", Version: "v1.0.0"}},
	}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(proj)
	depotFlag = ""

	deps, _ := completeDeps(nil, nil, "")
	if strings.Join(deps, ",") != "alpha,charlie" {
		t.Errorf("dep completions = %v, want [alpha charlie]", deps)
	}
	// A second positional arg gets no dep suggestions.
	if got, _ := completeDeps(nil, []string{"alpha"}, ""); got != nil {
		t.Errorf("expected no completions for second arg, got %v", got)
	}

	regs, _ := completeRegistries(nil, nil, "")
	if strings.Join(regs, ",") != "cosmcpp,cosmlua" {
		t.Errorf("registry completions = %v, want [cosmcpp cosmlua]", regs)
	}
}

func TestCompletionHint(t *testing.T) {
	cases := map[string]string{"/bin/zsh": "zsh", "/usr/bin/bash": "bash", "/usr/bin/fish": "fish", "": "cosm completion --help"}
	for shell, want := range cases {
		t.Setenv("SHELL", shell)
		if got := completionHint(); !strings.Contains(got, want) {
			t.Errorf("SHELL=%q hint = %q, want it to mention %q", shell, got, want)
		}
	}
}

// TestCompletionNoFileFallback: commands with no completable positional args must
// suppress the shell's default file completion (so `cosm status <TAB>` doesn't list
// the directory).
func TestCompletionNoFileFallback(t *testing.T) {
	root := buildRoot()
	for _, name := range []string{"status", "build", "env", "test", "setup"} {
		c, _, err := root.Find([]string{name})
		if err != nil || c == nil {
			t.Fatalf("command %q not found", name)
		}
		if c.ValidArgsFunction == nil {
			t.Fatalf("%q has no completion function (would file-complete)", name)
		}
		if _, dir := c.ValidArgsFunction(c, nil, ""); dir != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%q completion directive = %v, want NoFileComp", name, dir)
		}
	}
}
